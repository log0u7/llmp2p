// Package engine wraps the BitTorrent client used to pull and seed model
// swarms. One Engine owns one torrent client; the pull flow is:
//
//	add torrent (from file, or magnet by infohash)
//	  -> wait for metadata (BEP 9 from peers)
//	  -> download all pieces (verified against piece hashes)
//	  -> wait for completion, reporting progress
package engine

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

// Config configures an Engine.
type Config struct {
	// DataDir is the parent directory that contains one subdirectory per
	// torrent (named after the torrent root name, e.g. org__model).
	DataDir string
	// NoDHT disables the mainline DHT (tests, offline use). When
	// enabled, the standard public bootstrap routers are used.
	NoDHT bool
	// ListenPort is the BitTorrent listen port; 0 picks a random one.
	ListenPort int
	// NoUpload refuses to send chunks to peers.
	NoUpload bool
	// Seed keeps uploading after completion.
	Seed bool
	// DisableUTP is useful on flaky systems; TCP only then.
	DisableUTP bool
	// Debug enables verbose logging.
	Debug bool
}

// Engine is a running torrent client.
type Engine struct {
	cfg Config
	cl  *torrent.Client
}

// Progress describes the state of one download.
type Progress struct {
	Completed int64
	Total     int64
	Peers     int
	Complete  bool
}

// Status is a snapshot of one torrent in the client.
type Status struct {
	Name       string
	InfoHash   string
	Completed  int64
	Total      int64
	Peers      int
	Complete   bool
	Seeding    bool
	Downloaded int64
	Uploaded   int64
}

// New starts a torrent client.
func New(cfg Config, logger *slog.Logger) (*Engine, error) {
	if cfg.DataDir == "" {
		return nil, errors.New("engine: DataDir is required")
	}
	// Start from upstream defaults: building a zero-value ClientConfig
	// leaves connection pool fields empty, which silently prevents
	// dialing peers.
	cc := torrent.NewDefaultClientConfig()
	cc.DataDir = cfg.DataDir
	cc.NoDHT = cfg.NoDHT
	cc.DisableTrackers = cfg.NoDHT
	cc.NoDefaultPortForwarding = cfg.NoDHT
	cc.ListenPort = cfg.ListenPort
	cc.NoUpload = cfg.NoUpload
	cc.Seed = cfg.Seed
	cc.DisableUTP = cfg.DisableUTP
	cc.Debug = cfg.Debug
	cc.Slogger = logger

	cl, err := torrent.NewClient(cc)
	if err != nil {
		return nil, fmt.Errorf("engine: new client: %w", err)
	}
	return &Engine{cfg: cfg, cl: cl}, nil
}

// listenPort is the actual bound listen port (useful when configured as 0).
func (e *Engine) listenPort() int { return e.cl.LocalPort() }

// hashFromHex parses a 40-char v1 infohash without panicking on bad input.
func hashFromHex(s string) (metainfo.Hash, error) {
	var ih metainfo.Hash
	b, err := hex.DecodeString(s)
	if err != nil {
		return ih, fmt.Errorf("engine: infohash %q: %w", s, err)
	}
	if len(b) != len(ih) {
		return ih, fmt.Errorf("engine: infohash %q: want %d bytes", s, len(ih))
	}
	copy(ih[:], b)
	return ih, nil
}

// Close stops the client and all transfers.
func (e *Engine) Close() error {
	errs := e.cl.Close()
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// TorrentStatuses snapshots every torrent in the client.
func (e *Engine) TorrentStatuses() []Status {
	ts := e.cl.Torrents()
	out := make([]Status, 0, len(ts))
	for _, t := range ts {
		st := Status{
			Name:      t.Name(),
			InfoHash:  t.InfoHash().HexString(),
			Completed: t.BytesCompleted(),
			Total:     t.Length(),
			Peers:     len(t.PeerConns()),
			Complete:  t.Complete().Bool(),
			Seeding:   t.Seeding(),
		}
		cs := t.Stats()
		st.Downloaded = cs.BytesReadData.Int64()
		st.Uploaded = cs.BytesWrittenData.Int64()
		out = append(out, st)
	}
	return out
}

// pull waits for info (metadata), then downloads every piece until
// complete, emitting progress until done or ctx is cancelled.
func (e *Engine) pull(ctx context.Context, t *torrent.Torrent, onProgress func(Progress)) error {
	select {
	case <-t.GotInfo():
	case <-ctx.Done():
		return ctx.Err()
	}
	t.AllowDataDownload()
	t.DownloadAll()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		p := Progress{
			Completed: t.BytesCompleted(),
			Total:     t.Length(),
			Peers:     len(t.PeerConns()),
			Complete:  t.Complete().Bool(),
		}
		if onProgress != nil {
			onProgress(p)
		}
		if p.Complete {
			return nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("engine: pull %s interrupted: %w", t.Name(), ctx.Err())
		}
	}
}

// PullTorrentFile downloads the torrent described by a .torrent file.
func (e *Engine) PullTorrentFile(ctx context.Context, torrentPath string, onProgress func(Progress)) error {
	t, err := e.cl.AddTorrentFromFile(torrentPath)
	if err != nil {
		return fmt.Errorf("engine: add %s: %w", torrentPath, err)
	}
	return e.pull(ctx, t, onProgress)
}

// PrepareMagnet registers a torrent by infohash without starting any
// transfer, allowing peers to be injected before pulling.
func (e *Engine) PrepareMagnet(infoHashHex string) error {
	if len(infoHashHex) != 40 {
		return fmt.Errorf("engine: bad infohash %q", infoHashHex)
	}
	_, err := e.cl.AddMagnet("magnet:?xt=urn:btih:" + strings.ToLower(infoHashHex))
	return err
}

// PullMagnet downloads the torrent identified by a v1 infohash hex string,
// discovering metadata (BEP 9) and data from peers.
func (e *Engine) PullMagnet(ctx context.Context, infoHashHex string, onProgress func(Progress)) error {
	if len(infoHashHex) != 40 {
		return fmt.Errorf("engine: bad infohash %q", infoHashHex)
	}
	t, err := e.cl.AddMagnet("magnet:?xt=urn:btih:" + strings.ToLower(infoHashHex))
	if err != nil {
		return fmt.Errorf("engine: add magnet %s: %w", infoHashHex, err)
	}
	return e.pull(ctx, t, onProgress)
}

// AddPeers injects peer addresses for an infohash (used with
// PullMagnet when DHT is disabled or the swarm is private).
func (e *Engine) AddPeers(infoHashHex string, addrs []string) error {
	ih, err := hashFromHex(infoHashHex)
	if err != nil {
		return err
	}
	t, ok := e.cl.Torrent(ih)
	if !ok {
		return fmt.Errorf("engine: torrent %s not added", infoHashHex)
	}
	peers := make([]torrent.PeerInfo, 0, len(addrs))
	for _, a := range addrs {
		peers = append(peers, torrent.PeerInfo{Addr: peerAddr(a)})
	}
	t.AddPeers(peers)
	return nil
}

// peerAddr adapts a host:port string to the PeerRemoteAddr interface.
type peerAddr string

func (a peerAddr) String() string { return string(a) }

// SeedTorrentFile adds a torrent whose data already lives under DataDir,
// waits until every piece is verified, and leaves it seeding. Download is
// not started: the data must already be complete.
func (e *Engine) SeedTorrentFile(ctx context.Context, torrentPath string) error {
	t, err := e.cl.AddTorrentFromFile(torrentPath)
	if err != nil {
		return fmt.Errorf("engine: add %s: %w", torrentPath, err)
	}
	select {
	case <-t.GotInfo():
	case <-ctx.Done():
		return ctx.Err()
	}
	t.DisallowDataDownload()
	if !t.Complete().Bool() {
		// The client verifies pieces against storage asynchronously;
		// force a verification pass to be sure.
		if err := t.VerifyDataContext(ctx); err != nil {
			return fmt.Errorf("engine: verify %s: %w", t.Name(), err)
		}
	}
	select {
	case <-t.Complete().On():
		return nil
	case <-ctx.Done():
		return fmt.Errorf("engine: seed %s interrupted: %w", t.Name(), ctx.Err())
	}
}
