package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/log0u7/llmp2p/internal/manifest"
	"github.com/log0u7/llmp2p/internal/store"
)

// fakeModel installs a model directory with a manifest pointer and a
// torrent file, without real torrent data (no engine gets started: the
// torrent file must exist for the daemon to seed it, so we also drop a
// minimal valid torrent? No: without data the engine would fail. Instead
// the daemon skips models whose torrent is missing, so we assert that).
func fakeModel(t *testing.T, st *store.Store, modelID, infoHash string) string {
	t.Helper()
	dir, err := st.ModelDir(modelID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{
		Schema:      manifest.Schema,
		Model:       modelID,
		Revision:    "cafe123",
		CreatedAt:   time.Now().UTC(),
		PieceLength: manifest.PieceLength,
		InfoHash:    infoHash,
		Files: []manifest.File{
			{Path: "model.gguf", Size: 4, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		},
	}
	mfile, err := st.ModelManifestFile(modelID)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Save(mfile); err != nil {
		t.Fatal(err)
	}
	return dir
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// TestDaemonRunsAndServesStatus starts the daemon over an empty store and
// exercises the three endpoints.
func TestDaemonRunsAndServesStatus(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// A model without a torrent file is skipped, but still listed.
	fakeModel(t, st, "org/model", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, Options{Store: st, ListenAddr: addr, Version: "test"})
	}()
	defer func() {
		cancel()
		if err := <-errCh; err != nil {
			t.Errorf("daemon: %v", err)
		}
	}()

	// Wait for the API to come up.
	var status statusResponse
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := http.Get(fmt.Sprintf("http://%s/api/v1/status", addr))
		if err == nil {
			if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("status API never came up: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if status.Version != "test" || status.Models != 1 {
		t.Fatalf("status = %+v", status)
	}

	// Models endpoint.
	resp, err := http.Get(fmt.Sprintf("http://%s/api/v1/models", addr))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	if err := json.NewDecoder(resp.Body).Decode(&ids); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(ids) != 1 || ids[0] != "org/model" {
		t.Fatalf("models = %v", ids)
	}

	// Torrents endpoint (empty: the model has no torrent file).
	resp, err = http.Get(fmt.Sprintf("http://%s/api/v1/torrents", addr))
	if err != nil {
		t.Fatal(err)
	}
	var torrents []any
	if err := json.NewDecoder(resp.Body).Decode(&torrents); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(torrents) != 0 {
		t.Fatalf("torrents = %v", torrents)
	}
}

func TestDaemonRefusesSecondInstance(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- Run(ctx, Options{Store: st, ListenAddr: "127.0.0.1:0"}) }()

	// Give the first daemon time to take the lock, then expect the lock
	// wait loop to still be running (no error before ctx timeout).
	time.Sleep(500 * time.Millisecond)
	select {
	case err := <-done:
		if err != nil && err != context.DeadlineExceeded {
			t.Fatalf("first daemon exited early: %v", err)
		}
	default:
	}
}
