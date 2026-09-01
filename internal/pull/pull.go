// Package pull orchestrates a model download: resolve on the Hub, P2P
// swarm when a bootstrap index entry exists, HTTP fallback otherwise,
// manifest generation, and local publication.
package pull

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/log0u7/llmp2p/internal/engine"
	"github.com/log0u7/llmp2p/internal/hf"
	"github.com/log0u7/llmp2p/internal/index"
	"github.com/log0u7/llmp2p/internal/manifest"
	"github.com/log0u7/llmp2p/internal/ref"
	"github.com/log0u7/llmp2p/internal/store"
)

// Modes reported in Result.
const (
	ModeCache = "cache"
	ModeP2P   = "p2p"
	ModeHTTP  = "http"
)

// DefaultP2PGrace is how long a P2P attempt with zero downloaded bytes is
// tolerated before falling back to HTTP.
const DefaultP2PGrace = 90 * time.Second

// Options configures Run.
type Options struct {
	Store *store.Store
	// HF resolves and downloads from the Hub; nil uses hf.New().
	HF *hf.Client
	// BootstrapURLs are base URLs of index origins, tried in order.
	BootstrapURLs []string
	// HTTPClient is used for index and manifest fetching.
	HTTPClient *http.Client
	// EngineCfg configures the torrent client used for P2P pulls.
	EngineCfg engine.Config
	// EngineAddrs injects static peer addresses (used in tests and
	// private swarms).
	EngineAddrs []string
	// HTTPOnly skips the bootstrap index and P2P entirely.
	HTTPOnly bool
	// P2PGrace overrides DefaultP2PGrace.
	P2PGrace time.Duration
	// OnProgress receives periodic download progress.
	OnProgress func(mode string, p engine.Progress)
	// Log receives structured progress lines; nil disables logging.
	Log *slog.Logger
}

// Result summarizes a completed pull.
type Result struct {
	Model          string `json:"model"`
	Revision       string `json:"revision"`
	Mode           string `json:"mode"`
	Files          int    `json:"files"`
	Size           int64  `json:"size"`
	InfoHash       string `json:"infoHash"`
	ManifestSHA256 string `json:"manifestSha256"`
}

// Run performs the pull. The store is locked for the duration.
func Run(ctx context.Context, r *ref.Ref, opts Options) (Result, error) {
	if opts.Store == nil {
		return Result{}, fmt.Errorf("pull: store is required")
	}
	release, err := opts.Store.Lock(15 * time.Second)
	if err != nil {
		return Result{}, err
	}
	defer release()

	hfc := opts.HF
	if hfc == nil {
		hfc = hf.New()
	}
	grace := opts.P2PGrace
	if grace <= 0 {
		grace = DefaultP2PGrace
	}

	res, err := hfc.Resolve(ctx, r.ID(), r.Revision)
	if err != nil {
		return Result{}, err
	}
	files := res.Files
	if r.Path != "" {
		files = filterPath(files, r.Path)
		if len(files) == 0 {
			return Result{}, fmt.Errorf("pull: %s has no file %q at revision %s", r.ID(), r.Path, res.Revision)
		}
	}
	var total int64
	for _, f := range files {
		total += f.Size
	}

	modelDir, err := opts.Store.ModelDir(r.ID())
	if err != nil {
		return Result{}, err
	}
	entries := make([]manifest.File, 0, len(files))
	for _, f := range files {
		entries = append(entries, manifest.File{Path: f.Path, Size: f.Size})
	}

	// Cache: local files already match the pinned revision.
	if cached, ok, cerr := checkCache(opts.Store, r.ID(), res.Revision, modelDir); cerr != nil {
		return Result{}, cerr
	} else if ok {
		return Result{Model: r.ID(), Revision: res.Revision, Mode: ModeCache,
			Files: len(cached.Files), Size: total, InfoHash: cached.InfoHash,
			ManifestSHA256: mustSHA(cached)}, nil
	}

	if !opts.HTTPOnly && r.Path == "" {
		res2, err := pullP2P(ctx, r, opts, res.Revision, files, modelDir, grace)
		if err != nil {
			logf(opts.Log, "p2p pull failed, falling back to http", "err", err)
		} else {
			return res2, nil
		}
	}

	res2, err := pullHTTP(ctx, r, opts, res.Revision, files, modelDir, entries)
	if err != nil {
		return Result{}, err
	}
	return res2, nil
}

func filterPath(files []hf.FileInfo, path string) []hf.FileInfo {
	for _, f := range files {
		if f.Path == path {
			return []hf.FileInfo{f}
		}
	}
	return nil
}

// checkCache returns the local manifest when it pins the requested
// revision and the files still verify.
func checkCache(s *store.Store, modelID, revision, modelDir string) (*manifest.Manifest, bool, error) {
	manifestFile, err := s.ModelManifestFile(modelID)
	if err != nil {
		return nil, false, err
	}
	m, err := manifest.Load(manifestFile)
	if err != nil {
		return nil, false, nil // not cached
	}
	if m.Revision != revision {
		return nil, false, nil
	}
	if err := m.VerifyDir(modelDir); err != nil {
		return nil, false, fmt.Errorf("pull: cached files corrupt, re-pulling: %w", err)
	}
	return m, true, nil
}

// pullP2P joins the swarm advertised by the bootstrap index. It fails
// (without side effects on the store) when there is no entry, when the
// manifest cannot be fetched, or when no data arrives within grace.
func pullP2P(ctx context.Context, r *ref.Ref, opts Options, revision string, files []hf.FileInfo, modelDir string, grace time.Duration) (Result, error) {
	fetchRes, err := index.Fetch(opts.HTTPClient, opts.BootstrapURLs)
	if err != nil {
		return Result{}, err
	}
	entry, ok := fetchRes.Index.Get(r.ID())
	if !ok {
		return Result{}, fmt.Errorf("pull: no bootstrap index entry for %s", r.ID())
	}
	if entry.Revision != revision {
		return Result{}, fmt.Errorf("pull: swarm pinned to revision %s, want %s", entry.Revision, revision)
	}

	m, err := fetchManifest(opts.HTTPClient, opts.BootstrapURLs, entry, revision)
	if err != nil {
		return Result{}, err
	}

	engCfg := opts.EngineCfg
	engCfg.DataDir = filepath.Dir(modelDir)
	eng, err := engine.New(engCfg, opts.Log)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = eng.Close() }()

	if err := eng.PrepareMagnet(entry.InfoHash); err != nil {
		return Result{}, err
	}
	if len(opts.EngineAddrs) > 0 {
		if err := eng.AddPeers(entry.InfoHash, opts.EngineAddrs); err != nil {
			return Result{}, err
		}
	}

	pctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- eng.PullMagnet(pctx, entry.InfoHash, func(p engine.Progress) {
			if opts.OnProgress != nil {
				opts.OnProgress(ModeP2P, p)
			}
		})
	}()

	// Give up on the swarm when no byte arrives within grace.
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var lastBytes int64
	started := time.Now()
	for {
		select {
		case err := <-done:
			if err != nil {
				return Result{}, err
			}
			if err := m.VerifyDir(modelDir); err != nil {
				return Result{}, fmt.Errorf("pull: swarm data failed verification: %w", err)
			}
			return publish(opts, r, revision, files, modelDir, m, ModeP2P)
		case <-ticker.C:
			cur := engBytes(eng, entry.InfoHash)
			if cur != lastBytes {
				lastBytes = cur
				started = time.Now()
			}
			if time.Since(started) > grace {
				cancel()
				<-done
				return Result{}, fmt.Errorf("pull: no swarm data within %s", grace)
			}
		}
	}
}

func engBytes(e *engine.Engine, infoHash string) int64 {
	for _, st := range e.TorrentStatuses() {
		if st.InfoHash == infoHash {
			return st.Completed
		}
	}
	return 0
}

// fetchManifest retrieves the manifest from bootstrap origins and checks
// it against the pinned index entry.
func fetchManifest(client *http.Client, bases []string, entry index.Entry, revision string) (*manifest.Manifest, error) {
	if client == nil {
		client = http.DefaultClient
	}
	for _, base := range bases {
		url := index.ManifestURL(base, entry.ManifestSHA256)
		resp, err := client.Get(url)
		if err != nil {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			continue
		}
		m, err := manifest.Parse(body)
		if err != nil {
			continue
		}
		sum, err := m.SHA256()
		if err != nil || sum != entry.ManifestSHA256 || m.InfoHash != entry.InfoHash || m.Revision != revision {
			continue
		}
		return m, nil
	}
	return nil, fmt.Errorf("pull: manifest %s unreachable from bootstrap origins", entry.ManifestSHA256[:12])
}

// pullHTTP downloads every file from the Hub, then builds and publishes
// the manifest.
func pullHTTP(ctx context.Context, r *ref.Ref, opts Options, revision string, files []hf.FileInfo, modelDir string, entries []manifest.File) (Result, error) {
	hfc := opts.HF
	if hfc == nil {
		hfc = hf.New()
	}
	for _, f := range files {
		dst := filepath.Join(modelDir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return Result{}, err
		}
		want := f.LFSOID
		if info, err := os.Stat(dst); err == nil && info.Size() == f.Size && want != "" {
			// Keep existing LFS files that already match their
			// Hub-published sha256; non-LFS files are always refreshed.
			if sum, _, err := manifest.HashContent(filepath.Dir(dst), filepath.Base(dst)); err == nil && sum == want {
				continue
			}
		}
		if _, err := hfc.DownloadFile(ctx, r.ID(), revision, f.Path, dst, want); err != nil {
			return Result{}, fmt.Errorf("pull: download %s: %w", f.Path, err)
		}
		if opts.OnProgress != nil {
			opts.OnProgress(ModeHTTP, engine.Progress{})
		}
	}

	m, err := manifest.Create(r.ID(), revision, entries, modelDir)
	if err != nil {
		return Result{}, err
	}
	// LFS files must match the Hub-published sha256 exactly.
	for i, f := range files {
		if f.LFSOID != "" && m.Files[i].SHA256 != f.LFSOID {
			return Result{}, fmt.Errorf("pull: %s sha256 %s does not match Hub %s", f.Path, m.Files[i].SHA256, f.LFSOID)
		}
	}
	return publish(opts, r, revision, files, modelDir, m, ModeHTTP)
}

// publish stores the manifest, torrent, and local index entry.
func publish(opts Options, r *ref.Ref, revision string, files []hf.FileInfo, modelDir string, m *manifest.Manifest, mode string) (Result, error) {
	var total int64
	for _, f := range m.Files {
		total += f.Size
	}
	msha, err := m.SHA256()
	if err != nil {
		return Result{}, err
	}

	// Content-addressed manifest copy.
	mpath, err := opts.Store.ManifestPath(msha)
	if err != nil {
		return Result{}, err
	}
	if err := m.Save(mpath); err != nil {
		return Result{}, err
	}
	// Model dir pointer (pins the local revision).
	mfile, err := opts.Store.ModelManifestFile(r.ID())
	if err != nil {
		return Result{}, err
	}
	if err := m.Save(mfile); err != nil {
		return Result{}, err
	}
	// Torrent for seeders.
	torrentBytes, err := m.MetaInfoBytes(modelDir)
	if err != nil {
		return Result{}, err
	}
	tpath, err := opts.Store.TorrentPath(m.InfoHash)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(tpath), 0o755); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(tpath, torrentBytes, 0o644); err != nil {
		return Result{}, err
	}

	// Local index publication (whole-repo pulls only).
	if r.Path == "" {
		if err := publishLocalIndex(opts, r.ID(), m, msha, total); err != nil {
			logf(opts.Log, "local index publish failed", "err", err)
		}
	}

	return Result{
		Model:          r.ID(),
		Revision:       revision,
		Mode:           mode,
		Files:          len(m.Files),
		Size:           total,
		InfoHash:       m.InfoHash,
		ManifestSHA256: msha,
	}, nil
}

func publishLocalIndex(opts Options, modelID string, m *manifest.Manifest, msha string, size int64) error {
	local := &index.Index{Entries: map[string]index.Entry{}}
	if b, err := os.ReadFile(opts.Store.LocalIndexPath()); err == nil {
		parsed, _, err := index.Parse(b)
		if err == nil {
			local = parsed
		}
	}
	err := local.Add(index.Entry{
		Model:          modelID,
		InfoHash:       m.InfoHash,
		ManifestSHA256: msha,
		Revision:       m.Revision,
		Size:           size,
		AddedAt:        time.Now().UTC().Truncate(time.Second),
	})
	if err != nil {
		return err
	}
	return local.Save(opts.Store.LocalIndexPath())
}

func mustSHA(m *manifest.Manifest) string {
	s, err := m.SHA256()
	if err != nil {
		return ""
	}
	return s
}

func logf(l *slog.Logger, msg string, args ...any) {
	if l != nil {
		l.Info(msg, args...)
	}
}
