package hf

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// newTestServer spins up a fake Hub and returns a client pointing at it.
func newTestServer(t *testing.T, mux *http.ServeMux) *Client {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := New()
	c.BaseURL = srv.URL
	c.HTTP = srv.Client()
	return c
}

func testPayload(t *testing.T, size int) []byte {
	t.Helper()
	body := make([]byte, size)
	for i := range body {
		body[i] = byte(i % 251)
	}
	return body
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestResolve(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/models/org/model/revision/main", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("missing token header, got %q", got)
		}
		_, _ = fmt.Fprint(w, `{"sha":"commitsha"}`)
	})
	mux.HandleFunc("/api/models/org/model/tree/commitsha", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			_, _ = fmt.Fprint(w, `[{"type":"file","path":"model.gguf","size":4,"lfs":{"oid":"lfs256"}}]`)
			return
		}
		if r.URL.Query().Get("recursive") != "true" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Link", `</api/models/org/model/tree/commitsha?page=2>; rel="next"`)
		_, _ = fmt.Fprint(w, `[{"type":"file","path":"config.json","size":10},
			{"type":"directory","path":"sub"},
			{"type":"file","path":"weights.bin","size":4,"lfs":{"oid":"other256"}}]`)
	})
	c := newTestServer(t, mux)
	c.Token = "tok"

	info, err := c.Resolve(context.Background(), "org/model", "main")
	if err != nil {
		t.Fatal(err)
	}
	if info.Revision != "commitsha" {
		t.Fatalf("Revision = %q, want commitsha", info.Revision)
	}
	want := []FileInfo{
		{Path: "config.json", Size: 10},
		{Path: "model.gguf", Size: 4, LFSOID: "lfs256"},
		{Path: "weights.bin", Size: 4, LFSOID: "other256"},
	}
	if len(info.Files) != len(want) {
		t.Fatalf("Files = %+v, want %+v", info.Files, want)
	}
	for i, f := range info.Files {
		if f != want[i] {
			t.Errorf("Files[%d] = %+v, want %+v", i, f, want[i])
		}
	}
}

func TestResolveNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/models/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	c := newTestServer(t, mux)
	if _, err := c.Resolve(context.Background(), "org/missing", "main"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestDownload(t *testing.T) {
	body := testPayload(t, 1024)
	mux := http.NewServeMux()
	mux.HandleFunc("/org/model/resolve/main/dir/file.gguf", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/org/model/resolve/main/dir/file.gguf" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write(body)
	})
	c := newTestServer(t, mux)

	var out strings.Builder
	got, n, err := c.Download(context.Background(), "org/model", "main", "dir/file.gguf", &out)
	if err != nil {
		t.Fatal(err)
	}
	if want := sha256Hex(body); got != want {
		t.Fatalf("sha = %s, want %s", got, want)
	}
	if n != int64(len(body)) {
		t.Fatalf("size = %d, want %d", n, len(body))
	}
}

func TestDownloadFile(t *testing.T) {
	body := testPayload(t, 4096)
	// Two chunks so the resume path downloads the second one.
	chunk := 1024
	served := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/org/model/resolve/main/file.gguf", func(w http.ResponseWriter, r *http.Request) {
		served++
		if rng := r.Header.Get("Range"); rng != "" {
			var start int
			if _, err := fmt.Sscanf(rng, "bytes=%d-", &start); err != nil {
				t.Errorf("bad Range %q: %v", rng, err)
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(body)-1, len(body)))
			w.Header().Set("Content-Length", strconv.Itoa(len(body)-start))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(body[start:])
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	})
	c := newTestServer(t, mux)

	dir := t.TempDir()
	dst := filepath.Join(dir, "file.gguf")
	partial := body[:chunk]
	if err := os.WriteFile(dst+".llmp2p.part", partial, 0o644); err != nil {
		t.Fatal(err)
	}

	n, err := c.DownloadFile(context.Background(), "org/model", "main", "file.gguf", dst, sha256Hex(body))
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(len(body)) {
		t.Fatalf("size = %d, want %d", n, len(body))
	}
	if served != 1 {
		t.Fatalf("server hit %d times, want 1 (single ranged request)", served)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatal("resumed file content mismatch")
	}
	if _, err := os.Stat(dst + ".llmp2p.part"); !os.IsNotExist(err) {
		t.Fatal("partial file still present after success")
	}
}

func TestDownloadFileChecksumMismatch(t *testing.T) {
	body := testPayload(t, 128)
	mux := http.NewServeMux()
	mux.HandleFunc("/org/model/resolve/main/file.gguf", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	})
	c := newTestServer(t, mux)

	dst := filepath.Join(t.TempDir(), "file.gguf")
	if _, err := c.DownloadFile(context.Background(), "org/model", "main", "file.gguf", dst, strings.Repeat("0", 64)); !errors.Is(err, ErrChecksum) {
		t.Fatalf("err = %v, want ErrChecksum", err)
	}
	if _, err := os.Stat(dst + ".llmp2p.part"); !os.IsNotExist(err) {
		t.Fatal("partial file must be removed on checksum mismatch")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatal("destination must not exist on checksum mismatch")
	}
}
