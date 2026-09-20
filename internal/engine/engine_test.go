package engine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
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
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

// seedFixture builds a fake model repo laid out the way torrent storage
// expects it: <seedDataDir>/<torrentName>/<relpath>.
func seedFixture(t *testing.T) (seedDataDir, torrentPath string, m *manifest.Manifest) {
	t.Helper()
	seedDataDir = t.TempDir()
	modelDir := filepath.Join(seedDataDir, "model")
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
	torrentPath = filepath.Join(seedDataDir, "model.torrent")
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
	defer func() { _ = srv.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := srv.SeedTorrentFile(ctx, torrentPath); err != nil {
		t.Fatalf("seeder: %v", err)
	}
	srvAddr := fmt.Sprintf("127.0.0.1:%d", srv.listenPort())

	// Leecher A: prepare by infohash, inject peers, then pull (the
	// production delegated-pull sequence).
	cliA, err := New(Config{DataDir: t.TempDir(), NoDHT: true, ListenPort: freePort(t), DisableUTP: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cliA.Close() }()
	if err := cliA.PrepareMagnet(m.InfoHash); err != nil {
		t.Fatalf("prepare magnet A: %v", err)
	}
	if err := cliA.AddPeers(m.InfoHash, []string{srvAddr}); err != nil {
		t.Fatalf("add peer A: %v", err)
	}
	if err := cliA.PullMagnet(ctx, m.InfoHash, nil); err != nil {
		t.Fatalf("leecher A: %v", err)
	}
	if err := m.VerifyDir(filepath.Join(cliA.cfg.DataDir, "model")); err != nil {
		t.Fatalf("leecher A data: %v", err)
	}

	// Leecher B: from the infohash only (metadata from BEP 9).
	cliB, err := New(Config{DataDir: t.TempDir(), NoDHT: true, ListenPort: freePort(t), DisableUTP: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cliB.Close() }()
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
	if err := m.VerifyDir(filepath.Join(cliB.cfg.DataDir, "model")); err != nil {
		t.Fatalf("leecher B data: %v", err)
	}
}

func TestNewRequiresDataDir(t *testing.T) {
	if _, err := New(Config{}, nil); err == nil {
		t.Fatal("New accepted an empty DataDir")
	}
}

func TestHashFromHex(t *testing.T) {
	const good = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	ih, err := hashFromHex(good)
	if err != nil || ih.HexString() != good {
		t.Fatalf("hashFromHex(good) = %v, %v", ih, err)
	}
	if _, err := hashFromHex("zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"); err == nil {
		t.Fatal("hashFromHex accepted non-hex input")
	}
	if _, err := hashFromHex("aaaa"); err == nil {
		t.Fatal("hashFromHex accepted a short infohash")
	}
}

func TestPrepareMagnetValidation(t *testing.T) {
	e, err := New(Config{DataDir: t.TempDir(), NoDHT: true, DisableUTP: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Close() }()
	if err := e.PrepareMagnet("tooshort"); err == nil {
		t.Fatal("PrepareMagnet accepted a short infohash")
	}
	const ih = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := e.PrepareMagnet(ih); err != nil {
		t.Fatalf("PrepareMagnet(valid): %v", err)
	}
	if err := e.PrepareMagnet(strings.Repeat("A", 40)); err != nil {
		t.Fatalf("PrepareMagnet(uppercase): %v", err)
	}
}

func TestAddPeersErrors(t *testing.T) {
	e, err := New(Config{DataDir: t.TempDir(), NoDHT: true, DisableUTP: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Close() }()
	if err := e.AddPeers("nothex!!", []string{"127.0.0.1:1"}); err == nil {
		t.Fatal("AddPeers accepted non-hex infohash")
	}
	const ih = "abababababababababababababababababababab"
	if err := e.AddPeers(ih, []string{"127.0.0.1:1"}); err == nil {
		t.Fatal("AddPeers accepted an unregistered torrent")
	}
}

func TestTorrentStatuses(t *testing.T) {
	seedDataDir, torrentPath, m := seedFixture(t)
	e, err := New(Config{
		DataDir:    seedDataDir,
		NoDHT:      true,
		ListenPort: freePort(t),
		Seed:       true,
		DisableUTP: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := e.SeedTorrentFile(ctx, torrentPath); err != nil {
		t.Fatal(err)
	}

	stats := e.TorrentStatuses()
	if len(stats) != 1 {
		t.Fatalf("TorrentStatuses = %d entries, want 1", len(stats))
	}
	st := stats[0]
	if st.Name != "model" {
		t.Errorf("Name = %q, want model", st.Name)
	}
	if st.InfoHash != m.InfoHash {
		t.Errorf("InfoHash = %q, want %q", st.InfoHash, m.InfoHash)
	}
	if st.Total == 0 || !st.Complete || !st.Seeding {
		t.Errorf("status = %+v, want complete and seeding", st)
	}
}

func TestPullMagnetBadInfoHash(t *testing.T) {
	e, err := New(Config{DataDir: t.TempDir(), NoDHT: true, DisableUTP: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Close() }()
	if err := e.PullMagnet(context.Background(), "abc", nil); err == nil {
		t.Fatal("PullMagnet accepted a short infohash")
	}
}

func TestPullMagnetCanceledContext(t *testing.T) {
	e, err := New(Config{DataDir: t.TempDir(), NoDHT: true, DisableUTP: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = e.PullMagnet(ctx, strings.Repeat("ab", 20), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
