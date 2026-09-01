// Package daemon implements llmp2pd: a background seeder that keeps every
// model in the store available to the swarm and exposes a local status
// API. The HTTP API binds to loopback only: it is for local tooling.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/log0u7/llmp2p/internal/engine"
	"github.com/log0u7/llmp2p/internal/manifest"
	"github.com/log0u7/llmp2p/internal/pull"
	"github.com/log0u7/llmp2p/internal/store"
)

// defaultPullTemplate builds the delegated pull configuration from the
// daemon options. NoLock is forced: the daemon already owns the store lock.
func defaultPullTemplate(opts Options) pull.Options {
	template := pull.Options{Store: opts.Store, NoLock: true}
	if opts.PullTemplate != nil {
		template = *opts.PullTemplate
		template.NoLock = true
		if template.Store == nil {
			template.Store = opts.Store
		}
	}
	return template
}

// DefaultAddr is the loopback status API address.
const DefaultAddr = "127.0.0.1:8347"

// Server runs the seeder engines and the status API.
type Server struct {
	st        *store.Store
	engines   map[string]*engine.Engine // keyed by owner dir
	startedAt time.Time
	version   string
	pulls     *pullQueue

	mu        sync.Mutex
	pullStats map[string]int // pull jobs by outcome, read by /metrics
}

// Options configures Run.
type Options struct {
	Store      *store.Store
	ListenAddr string // default DefaultAddr
	// EngineOverrides applied to every seeder engine (ports, debug...).
	EngineOverrides func(*engine.Config)
	// PullTemplate is the base configuration for delegated pull jobs;
	// nil builds a default one from the store (NoLock is forced).
	PullTemplate *pull.Options
	Log          *slog.Logger
	Version      string
}

type statusResponse struct {
	Version        string `json:"version"`
	UptimeSeconds  int64  `json:"uptimeSeconds"`
	Models         int    `json:"models"`
	Torrents       int    `json:"torrents"`
	UploadedBytes  int64  `json:"uploadedBytes"`
	SeedingEngines int    `json:"seedingEngines"`
}

// Run starts seeding every stored model and serves the status API until
// ctx is cancelled.
func Run(ctx context.Context, opts Options) error {
	st := opts.Store
	// The daemon owns the store: wait for any competing engine to finish.
	var release func()
	for {
		var err error
		release, err = st.Lock(2 * time.Second)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		logf(opts.Log, "waiting for store lock", "err", err)
	}
	defer release()

	srv := &Server{
		st:        st,
		engines:   map[string]*engine.Engine{},
		startedAt: time.Now(),
		version:   opts.Version,
	}
	if srv.version == "" {
		srv.version = "0.0.0"
	}
	srv.pulls = newPullQueue(srv, defaultPullTemplate(opts), opts.Log)
	if err := srv.startSeeders(ctx, opts); err != nil {
		return err
	}
	defer func() {
		for _, e := range srv.engines {
			_ = e.Close()
		}
	}()

	addr := opts.ListenAddr
	if addr == "" {
		addr = DefaultAddr
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/status", srv.handleStatus)
	mux.HandleFunc("GET /api/v1/models", srv.handleModels)
	mux.HandleFunc("GET /api/v1/torrents", srv.handleTorrents)
	mux.HandleFunc("GET /metrics", srv.writeMetrics)
	mux.HandleFunc("POST /api/v1/pulls", srv.handlePullCreate)
	mux.HandleFunc("GET /api/v1/pulls/{id}", srv.handlePullGet)
	mux.HandleFunc("GET /api/v1/pulls", srv.handlePullList)
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("daemon: listen %s: %w", addr, err)
	}
	logf(opts.Log, "llmp2pd listening", "addr", addr, "engines", len(srv.engines))

	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.Serve(ln) }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			logf(opts.Log, "shutdown error", "err", err)
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// startSeeders groups stored models by owner directory (torrent data
// lands under <owner>/<repo>) and launches one engine per owner.
func (s *Server) startSeeders(ctx context.Context, opts Options) error {
	// model id -> (owner dir, torrent path)
	type target struct{ ownerDir, torrentPath string }
	targets := map[string]target{}

	owners, err := os.ReadDir(filepath.Join(s.st.Root(), "store"))
	if err != nil {
		return nil // empty store
	}
	for _, owner := range owners {
		if !owner.IsDir() {
			continue
		}
		ownerDir := filepath.Join(s.st.Root(), "store", owner.Name())
		repos, err := os.ReadDir(ownerDir)
		if err != nil {
			continue
		}
		for _, repo := range repos {
			if !repo.IsDir() {
				continue
			}
			modelID := owner.Name() + "/" + repo.Name()
			mfile := filepath.Join(ownerDir, repo.Name(), ".llmp2p-manifest.json")
			m, err := manifest.Load(mfile)
			if err != nil {
				logf(opts.Log, "skipping model without manifest", "model", modelID)
				continue
			}
			tpath, err := s.st.TorrentPath(m.InfoHash)
			if err != nil {
				continue
			}
			if _, err := os.Stat(tpath); err != nil {
				logf(opts.Log, "skipping model without torrent", "model", modelID)
				continue
			}
			targets[modelID] = target{ownerDir: ownerDir, torrentPath: tpath}
		}
	}

	for modelID, t := range targets {
		e, ok := s.engines[t.ownerDir]
		if !ok {
			cfg := engine.Config{
				DataDir:    t.ownerDir,
				Seed:       true,
				ListenPort: 0,
			}
			if opts.EngineOverrides != nil {
				opts.EngineOverrides(&cfg)
			}
			e, err = engine.New(cfg, opts.Log)
			if err != nil {
				return err
			}
			s.engines[t.ownerDir] = e
		}
		seedCtx, cancel := context.WithTimeout(ctx, time.Minute)
		if err := e.SeedTorrentFile(seedCtx, t.torrentPath); err != nil {
			cancel()
			return fmt.Errorf("daemon: seed %s: %w", modelID, err)
		}
		cancel()
		logf(opts.Log, "seeding", "model", modelID)
	}
	return nil
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	var torrents, models int
	var uploaded int64
	for _, e := range s.engines {
		for _, st := range e.TorrentStatuses() {
			torrents++
			uploaded += st.Uploaded
		}
	}
	models = s.countModels()
	writeJSON(w, statusResponse{
		Version:        s.version,
		UptimeSeconds:  int64(time.Since(s.startedAt).Seconds()),
		Models:         models,
		Torrents:       torrents,
		UploadedBytes:  uploaded,
		SeedingEngines: len(s.engines),
	})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	ids, _ := s.st.Models()
	writeJSON(w, ids)
}

func (s *Server) handleTorrents(w http.ResponseWriter, r *http.Request) {
	all := []engine.Status{}
	for _, e := range s.engines {
		all = append(all, e.TorrentStatuses()...)
	}
	writeJSON(w, all)
}

func (s *Server) countModels() int {
	ids, err := s.st.Models()
	if err != nil {
		return 0
	}
	return len(ids)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func logf(l *slog.Logger, msg string, args ...any) {
	if l != nil {
		l.Info(msg, args...)
	}
}
