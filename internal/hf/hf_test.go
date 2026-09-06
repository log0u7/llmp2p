package hf

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
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

func TestNewUsesHFEndpoint(t *testing.T) {
	t.Setenv("HF_ENDPOINT", "https://mirror.example")
	if got := New().BaseURL; got != "https://mirror.example" {
		t.Fatalf("BaseURL = %q, want mirror", got)
	}
	t.Setenv("HF_ENDPOINT", "")
	if got := New().BaseURL; got != DefaultBaseURL {
		t.Fatalf("BaseURL = %q, want default", got)
	}
}

func TestResolveServerError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/models/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	c := newTestServer(t, mux)
	_, err := c.Resolve(context.Background(), "org/model", "main")
	var herr *HTTPError
	if !errors.As(err, &herr) || herr.Status != http.StatusServiceUnavailable {
		t.Fatalf("err = %v, want HTTPError 503", err)
	}
}

func TestResolveBadJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/models/org/model/revision/main", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "not json")
	})
	c := newTestServer(t, mux)
	_, err := c.Resolve(context.Background(), "org/model", "main")
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("err = %v, want decode failure", err)
	}
}

func TestResolveListFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/models/org/model/revision/main", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"sha":"commitsha"}`)
	})
	mux.HandleFunc("/api/models/org/model/tree/commitsha", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	c := newTestServer(t, mux)
	_, err := c.Resolve(context.Background(), "org/model", "main")
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "list org/model@main") {
		t.Fatalf("err = %v, want wrapped ErrNotFound from tree listing", err)
	}
}

func TestListTreeBadLinkHeader(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/models/org/model/revision/main", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"sha":"commitsha"}`)
	})
	mux.HandleFunc("/api/models/org/model/tree/commitsha", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", `<http://[bad>; rel="next"`)
		_, _ = fmt.Fprint(w, `[]`)
	})
	c := newTestServer(t, mux)
	_, err := c.Resolve(context.Background(), "org/model", "main")
	if err == nil || !strings.Contains(err.Error(), "bad Link header") {
		t.Fatalf("err = %v, want bad Link header failure", err)
	}
}

func TestLinkNext(t *testing.T) {
	cases := []struct {
		header string
		want   string
	}{
		{"", ""},
		{`</tree?page=2>; rel="next"`, "/tree?page=2"},
		{`</tree?page=1>; rel="prev", </tree?page=2>; rel="next"`, "/tree?page=2"},
		{`</tree?page=1>; rel="prev"`, ""},
		{"garbage", ""},
	}
	for _, tc := range cases {
		if got := linkNext(tc.header); got != tc.want {
			t.Errorf("linkNext(%q) = %q, want %q", tc.header, got, tc.want)
		}
	}
}

func TestArtifactURL(t *testing.T) {
	c := &Client{BaseURL: "https://hub.example"}
	got := c.artifactURL("org/model", "main", "dir/file gguf.bin")
	want := "https://hub.example/org/model/resolve/main/dir/file%20gguf.bin"
	if got != want {
		t.Fatalf("artifactURL = %q, want %q", got, want)
	}
	if got := (&Client{}).baseURL(); got != DefaultBaseURL {
		t.Fatalf("baseURL = %q, want default", got)
	}
}

func TestDownloadNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/org/model/resolve/main/file.gguf", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	c := newTestServer(t, mux)
	if _, _, err := c.Download(context.Background(), "org/model", "main", "file.gguf", io.Discard); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestDownloadHTTPError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/org/model/resolve/main/file.gguf", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	c := newTestServer(t, mux)
	_, _, err := c.Download(context.Background(), "org/model", "main", "file.gguf", io.Discard)
	var herr *HTTPError
	if !errors.As(err, &herr) || herr.Status != http.StatusForbidden {
		t.Fatalf("err = %v, want HTTPError 403", err)
	}
}

func TestDownloadRequestError(t *testing.T) {
	c := &Client{BaseURL: "http://bad\x7fhost", HTTP: &http.Client{}}
	if _, _, err := c.Download(context.Background(), "org/model", "main", "f", io.Discard); err == nil {
		t.Fatal("want request creation failure")
	}
}

func TestDownloadDoError(t *testing.T) {
	body := testPayload(t, 64)
	mux := http.NewServeMux()
	mux.HandleFunc("/org/model/resolve/main/f", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	})
	c := newTestServer(t, mux)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := c.Download(ctx, "org/model", "main", "f", io.Discard); err == nil {
		t.Fatal("want transport failure on canceled context")
	}
}

func TestDownloadClientDefault(t *testing.T) {
	if (&Client{}).downloadClient() == nil {
		t.Fatal("nil download client")
	}
}

func TestDownloadFileRangeNotSatisfiableRestarts(t *testing.T) {
	body := testPayload(t, 512)
	served := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/org/model/resolve/main/file.gguf", func(w http.ResponseWriter, r *http.Request) {
		served++
		if r.Header.Get("Range") != "" {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		_, _ = w.Write(body)
	})
	c := newTestServer(t, mux)

	dst := filepath.Join(t.TempDir(), "file.gguf")
	if err := os.WriteFile(dst+".llmp2p.part", body[:100], 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := c.DownloadFile(context.Background(), "org/model", "main", "file.gguf", dst, sha256Hex(body))
	if err != nil {
		t.Fatal(err)
	}
	if served != 2 {
		t.Fatalf("server hit %d times, want 2 (416 then restart)", served)
	}
	if n != int64(len(body)) {
		t.Fatalf("size = %d, want %d", n, len(body))
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != string(body) {
		t.Fatalf("content mismatch after restart: %v", err)
	}
}

func TestDownloadFileServerIgnoresRange(t *testing.T) {
	body := testPayload(t, 512)
	mux := http.NewServeMux()
	mux.HandleFunc("/org/model/resolve/main/file.gguf", func(w http.ResponseWriter, r *http.Request) {
		// Full 200 response despite the Range header.
		_, _ = w.Write(body)
	})
	c := newTestServer(t, mux)

	dst := filepath.Join(t.TempDir(), "file.gguf")
	if err := os.WriteFile(dst+".llmp2p.part", body[:100], 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := c.DownloadFile(context.Background(), "org/model", "main", "file.gguf", dst, sha256Hex(body))
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(len(body)) {
		t.Fatalf("size = %d, want %d (full rewrite)", n, len(body))
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != string(body) {
		t.Fatalf("content mismatch after rewrite: %v", err)
	}
}

func TestDownloadFileNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/org/model/resolve/main/file.gguf", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	c := newTestServer(t, mux)
	dst := filepath.Join(t.TempDir(), "file.gguf")
	if _, err := c.DownloadFile(context.Background(), "org/model", "main", "file.gguf", dst, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatal("destination must not exist on 404")
	}
}

func TestDownloadFileHTTPError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/org/model/resolve/main/file.gguf", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	c := newTestServer(t, mux)
	dst := filepath.Join(t.TempDir(), "file.gguf")
	_, err := c.DownloadFile(context.Background(), "org/model", "main", "file.gguf", dst, "")
	var herr *HTTPError
	if !errors.As(err, &herr) || herr.Status != http.StatusInternalServerError {
		t.Fatalf("err = %v, want HTTPError 500", err)
	}
}

func TestDownloadFileResumeStateError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/org/model/resolve/main/file.gguf", func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server must not be reached when resume state fails")
	})
	c := newTestServer(t, mux)

	dir := t.TempDir()
	dst := filepath.Join(dir, "file.gguf")
	// A directory at the partial-file path makes OpenFile(O_RDWR) fail.
	if err := os.Mkdir(dst+".llmp2p.part", 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DownloadFile(context.Background(), "org/model", "main", "file.gguf", dst, ""); err == nil {
		t.Fatal("want resume state failure")
	}
}

func TestDownloadFileStaleSymlink(t *testing.T) {
	c := newTestServer(t, http.NewServeMux())
	dst := filepath.Join(t.TempDir(), "file.gguf")
	// Stat follows the link and reports ENOENT, then OpenFile(O_EXCL)
	// fails because the link itself exists.
	if err := os.Symlink(filepath.Join(t.TempDir(), "gone"), dst+".llmp2p.part"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DownloadFile(context.Background(), "org/model", "main", "file.gguf", dst, ""); err == nil {
		t.Fatal("want resume state failure on stale symlink")
	}
}

func TestDownloadFileRequestError(t *testing.T) {
	c := &Client{BaseURL: "http://bad\x7fhost", HTTP: &http.Client{}}
	if _, err := c.DownloadFile(context.Background(), "org/model", "main", "f", filepath.Join(t.TempDir(), "f"), ""); err == nil {
		t.Fatal("want request creation failure")
	}
}

func TestDownloadFileDoError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/org/model/resolve/main/f", func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server must not be reached on canceled context")
	})
	c := newTestServer(t, mux)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.DownloadFile(ctx, "org/model", "main", "f", filepath.Join(t.TempDir(), "f"), ""); err == nil {
		t.Fatal("want transport failure on canceled context")
	}
}

func TestIsLFS(t *testing.T) {
	if (FileInfo{}).IsLFS() {
		t.Fatal("empty FileInfo must not be LFS")
	}
	if !(FileInfo{LFSOID: "abc"}).IsLFS() {
		t.Fatal("FileInfo with OID must be LFS")
	}
}

func TestDecodeResponseDiscardsBody(t *testing.T) {
	res := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
	}
	if err := decodeResponse(res, nil); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	res = &http.Response{
		StatusCode: http.StatusNotFound,
		Body:       io.NopCloser(strings.NewReader("")),
	}
	if err := decodeResponse(res, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
