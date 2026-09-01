package engine

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/log0u7/llmp2p/internal/manifest"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// seedFixture builds a fake model repo laid out the way torrent storage
// expects it: <seedDataDir>/<torrentName>/<relpath>.
func seedFixture(t *testing.T) (seedDataDir, torrentPath string, m *manifest.Manifest) {
	t.Helper()
	seedDataDir = t.TempDir()
	modelDir := filepath.Join(seedDataDir, "org__model")
	files := map[string]string{
		"config.json":        `{"model_type":"swarm-test"}`,
		"model.gguf":         fmt.Sprintf("%s%s", string(make([]byte, 1024*1024)), "ggufpayload"),
		"sub/tokenizer.json": `{"tokens":["a","b","c"]}`,
	}
	var entries []manifest.File
	for path, content := range files {
		p := filepath.Join(modelDir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, manifest.File{Path: path, Size: int64(len(content))})
	}
	// Deterministic sort by path.
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].Path < entries[j-1].Path; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
	m, err := manifest.Create("org/model", "rev1", entries, modelDir)
	if err != nil {
		t.Fatal(err)
	}
	torrentBytes, err := m.MetaInfoBytes(modelDir)
	if err != nil {
		t.Fatal(err)
	}
	torrentPath = filepath.Join(seedDataDir, "org__model.torrent")
	if err := os.WriteFile(torrentPath, torrentBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	return seedDataDir, torrentPath, m
}

// TestLocalSwarm runs a full seeder -> leecher exchange on localhost with
// DHT and trackers disabled: the seeder shares pre-existing data, one
// leecher pulls from the .torrent and another from the bare infohash
// (metadata via BEP 9).
func TestLocalSwarm(t *testing.T) {
	seedDataDir, torrentPath, m := seedFixture(t)

	srv, err := New(Config{
		DataDir:    seedDataDir,
		NoDHT:      true,
		ListenPort: freePort(t),
		Seed:       true,
		DisableUTP: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := srv.SeedTorrentFile(ctx, torrentPath); err != nil {
		t.Fatalf("seeder: %v", err)
	}
	srvAddr := fmt.Sprintf("127.0.0.1:%d", srv.listenPort())

	// Leecher A: from the .torrent file.
	cliA, err := New(Config{DataDir: t.TempDir(), NoDHT: true, ListenPort: freePort(t), DisableUTP: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cliA.Close()
	errA := make(chan error, 1)
	go func() {
		errA <- cliA.PullTorrentFile(ctx, torrentPath, nil)
	}()
	// Peers can be injected any time after the torrent is registered.
	time.Sleep(300 * time.Millisecond)
	if err := cliA.AddPeers(m.InfoHash, []string{srvAddr}); err != nil {
		t.Fatalf("add peer A: %v", err)
	}
	if err := <-errA; err != nil {
		t.Fatalf("leecher A: %v", err)
	}
	if err := m.VerifyDir(filepath.Join(cliA.cfg.DataDir, "org__model")); err != nil {
		t.Fatalf("leecher A data: %v", err)
	}

	// Leecher B: from the infohash only (metadata from BEP 9).
	cliB, err := New(Config{DataDir: t.TempDir(), NoDHT: true, ListenPort: freePort(t), DisableUTP: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cliB.Close()
	var lastProgress Progress
	errB := make(chan error, 1)
	go func() {
		errB <- cliB.PullMagnet(ctx, m.InfoHash, func(p Progress) { lastProgress = p })
	}()
	time.Sleep(300 * time.Millisecond)
	if err := cliB.AddPeers(m.InfoHash, []string{srvAddr}); err != nil {
		t.Fatalf("add peer B: %v", err)
	}
	if err := <-errB; err != nil {
		t.Fatalf("leecher B: %v", err)
	}
	if !lastProgress.Complete || lastProgress.Total == 0 {
		t.Fatalf("final progress = %+v", lastProgress)
	}
	if err := m.VerifyDir(filepath.Join(cliB.cfg.DataDir, "org__model")); err != nil {
		t.Fatalf("leecher B data: %v", err)
	}
}
