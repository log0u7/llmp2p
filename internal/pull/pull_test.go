package pull

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/log0u7/llmp2p/internal/engine"
	"github.com/log0u7/llmp2p/internal/hf"
	"github.com/log0u7/llmp2p/internal/index"
	"github.com/log0u7/llmp2p/internal/manifest"
	"github.com/log0u7/llmp2p/internal/ref"
	"github.com/log0u7/llmp2p/internal/signing"
	"github.com/log0u7/llmp2p/internal/store"
)

const (
	ggufContent = "GGUF-FAKE-PAYLOAD-0123456789"
	configData  = `{"model_type":"pull-test"}`
)

var ggufSHA = func() string {
	sum := sha256.Sum256([]byte(ggufContent))
	return hex.EncodeToString(sum[:])
}()

// fakeHub emulates the Hub endpoints used by pull: revision pinning,
// tree listing with an LFS entry, and artifact downloads.
func fakeHub(t *testing.T, hits *atomic.Int64) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/models/org/model/revision/main", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = fmt.Fprint(w, `{"sha":"cafe123"}`)
	})
	mux.HandleFunc("/api/models/org/model/tree/cafe123", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `[{"type":"file","path":"config.json","size":%d},
			{"type":"file","path":"model.gguf","size":%d,"lfs":{"oid":"%s"}}]`,
			len(configData), len(ggufContent), ggufSHA)
	})
	mux.HandleFunc("/org/model/resolve/cafe123/", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch filepath.Base(r.URL.Path) {
		case "config.json":
			_, _ = w.Write([]byte(configData))
		case "model.gguf":
			_, _ = w.Write([]byte(ggufContent))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func emptyBootstrap(t *testing.T) string {
	mux := http.NewServeMux()
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"entries":{}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func pullRef(t *testing.T) *ref.Ref {
	t.Helper()
	r, err := ref.Parse("hf:org/model")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func hubAt(url string) *hf.Client {
	c := hf.New()
	c.BaseURL = url
	c.HTTP = http.DefaultClient
	return c
}

func TestPullHTTPFallbackAndPublishes(t *testing.T) {
	var hits atomic.Int64
	hub := fakeHub(t, &hits)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	res, err := Run(context.Background(), pullRef(t), Options{
		Store:         st,
		HF:            hubAt(hub.URL),
		BootstrapURLs: []string{emptyBootstrap(t)},
		HTTPClient:    http.DefaultClient,
		EngineCfg:     engine.Config{NoDHT: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != ModeHTTP {
		t.Fatalf("mode = %q, want http", res.Mode)
	}
	if res.Revision != "cafe123" || res.Files != 2 {
		t.Fatalf("result = %+v", res)
	}

	// Files on disk with correct content.
	modelDir, err := st.ModelDir("org/model")
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"config.json": configData,
		"model.gguf":  ggufContent,
	} {
		got, err := os.ReadFile(filepath.Join(modelDir, path))
		if err != nil || string(got) != want {
			t.Fatalf("file %s: err=%v content mismatch", path, err)
		}
	}

	// Manifest published content-addressed + model pointer + torrent.
	m, err := filepath.Glob(filepath.Join(st.Root(), "manifests", "*.json"))
	if err != nil || len(m) != 1 {
		t.Fatalf("manifests = %v, err = %v", m, err)
	}
	ptr, err := st.ModelManifestFile("org/model")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ptr); err != nil {
		t.Fatalf("model manifest pointer: %v", err)
	}
	tpath, err := st.TorrentPath(res.InfoHash)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tpath); err != nil {
		t.Fatalf("torrent: %v", err)
	}

	// Local index entry published.
	b, err := os.ReadFile(st.LocalIndexPath())
	if err != nil {
		t.Fatal(err)
	}
	ix, _, err := index.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := ix.Get("org/model")
	if !ok || e.InfoHash != res.InfoHash || e.ManifestSHA256 != res.ManifestSHA256 {
		t.Fatalf("local index entry = %+v ok=%v", e, ok)
	}

	// Second pull must be a cache hit with zero Hub downloads.
	downloadHits := hits.Load()
	res2, err := Run(context.Background(), pullRef(t), Options{
		Store:         st,
		HF:            hubAt(hub.URL),
		BootstrapURLs: []string{emptyBootstrap(t)},
		HTTPClient:    http.DefaultClient,
		EngineCfg:     engine.Config{NoDHT: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Mode != ModeCache {
		t.Fatalf("second mode = %q, want cache", res2.Mode)
	}
	if got, want := hits.Load(), downloadHits+1; got != want {
		t.Fatalf("cache hit hub requests = %d, want %d (revision pin only)", got, want)
	}
}

// TestPullP2P seeds a store with a first HTTP pull, publishes the
// manifest via a bootstrap origin, then pulls into a second store
// through the local swarm.
func TestPullP2P(t *testing.T) {
	var hits atomic.Int64
	hub := fakeHub(t, &hits)

	// First pull: HTTP fallback populates the seeder store.
	seedStore, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := Run(context.Background(), pullRef(t), Options{
		Store:         seedStore,
		HF:            hubAt(hub.URL),
		BootstrapURLs: []string{emptyBootstrap(t)},
		HTTPClient:    http.DefaultClient,
		EngineCfg:     engine.Config{NoDHT: true},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Seeder engine serves the swarm.
	seedModelDir, err := seedStore.ModelDir("org/model")
	if err != nil {
		t.Fatal(err)
	}
	tpath, err := seedStore.TorrentPath(first.InfoHash)
	if err != nil {
		t.Fatal(err)
	}
	seederPort := freePort(t)
	seeder, err := engine.New(engine.Config{
		DataDir:    filepath.Dir(seedModelDir),
		NoDHT:      true,
		Seed:       true,
		DisableUTP: true,
		ListenPort: seederPort,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = seeder.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := seeder.SeedTorrentFile(ctx, tpath); err != nil {
		t.Fatal(err)
	}

	// Bootstrap origin serves the index and the manifest.
	mBytes, err := os.ReadFile(func() string {
		p, _ := seedStore.ManifestPath(first.ManifestSHA256)
		return p
	}())
	if err != nil {
		t.Fatal(err)
	}
	boot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.json":
			_, _ = fmt.Fprintf(w, `{"entries":{"org/model":{"model":"org/model","infoHash":"%s","manifestSha256":"%s","revision":"cafe123","size":%d}}}`,
				first.InfoHash, first.ManifestSHA256, first.Size)
		case "/manifests/" + first.ManifestSHA256 + ".json":
			_, _ = w.Write(mBytes)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer boot.Close()

	// Second pull: P2P from the seeder.
	leechStore, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	leechModelDir, err := leechStore.ModelDir("org/model")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(ctx, pullRef(t), Options{
		Store:         leechStore,
		HF:            hubAt(hub.URL),
		BootstrapURLs: []string{boot.URL},
		HTTPClient:    http.DefaultClient,
		EngineCfg:     engine.Config{NoDHT: true, DisableUTP: true},
		EngineAddrs:   []string{fmt.Sprintf("127.0.0.1:%d", seederPort)},
		P2PGrace:      30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != ModeP2P {
		t.Fatalf("mode = %q, want p2p", res.Mode)
	}
	got, err := os.ReadFile(filepath.Join(leechModelDir, "model.gguf"))
	if err != nil || string(got) != ggufContent {
		t.Fatalf("p2p file mismatch: err=%v", err)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

// TestManifestSignatureLifecycle pulls with a publisher key present: the
// signature sidecar must be published next to the manifest and verify;
// the allowlist then governs acceptance on the fetching side.
func TestManifestSignatureLifecycle(t *testing.T) {
	var hits atomic.Int64
	hub := fakeHub(t, &hits)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// Publisher key pre-created: signing is opt-in via keygen.
	priv, created, err := signing.LoadOrCreate(filepath.Join(st.Root(), signing.DefaultKeyFile))
	if err != nil || !created {
		t.Fatalf("keygen: created=%v err=%v", created, err)
	}
	pubHex := signing.PublicKeyHex(priv)

	res, err := Run(context.Background(), pullRef(t), Options{
		Store:         st,
		HF:            hubAt(hub.URL),
		BootstrapURLs: []string{emptyBootstrap(t)},
		HTTPClient:    http.DefaultClient,
		EngineCfg:     engine.Config{NoDHT: true},
	})
	if err != nil {
		t.Fatal(err)
	}

	sigPath, err := st.SignaturePath(res.ManifestSHA256)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(sigPath)
	if err != nil {
		t.Fatalf("signature sidecar missing: %v", err)
	}
	var sig struct {
		Signature string `json:"signature"`
		PublicKey string `json:"publicKey"`
	}
	if err := json.Unmarshal(b, &sig); err != nil {
		t.Fatal(err)
	}
	if sig.PublicKey != pubHex {
		t.Fatalf("sidecar signer = %s, want %s", sig.PublicKey, pubHex)
	}
	m, err := st.ManifestPath(res.ManifestSHA256)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := signing.Verify(sig.PublicKey, sig.Signature, content); err != nil {
		t.Fatal(err)
	}

	// Allowlist verification on the fetching side.
	opts := Options{AllowedSigners: []string{pubHex}, HTTPClient: http.DefaultClient}
	mServed, err := manifest.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyManifestSignature(opts, mServed, []string{emptyBootstrap(t)}); err == nil {
		// emptyBootstrap serves no .sig: with a non-empty allowlist this
		// must be rejected.
		t.Fatal("unsigned manifest must be rejected when allowlist is set")
	}
}

func TestRunRequiresStore(t *testing.T) {
	if _, err := Run(context.Background(), pullRef(t), Options{}); err == nil {
		t.Fatal("Run accepted a nil store")
	}
}

func TestRunResolveFailure(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/models/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	_, err = Run(context.Background(), pullRef(t), Options{
		Store: st,
		HF:    hubAt(srv.URL),
	})
	if !errors.Is(err, hf.ErrNotFound) {
		t.Fatalf("err = %v, want wrapped ErrNotFound", err)
	}
}

func TestFilterPath(t *testing.T) {
	files := []hf.FileInfo{{Path: "a.gguf"}, {Path: "b.gguf"}}
	if got := filterPath(files, "b.gguf"); len(got) != 1 || got[0].Path != "b.gguf" {
		t.Fatalf("filterPath = %+v", got)
	}
	if got := filterPath(files, "z.gguf"); got != nil {
		t.Fatalf("filterPath(missing) = %+v, want nil", got)
	}
}

func TestCheckCachePaths(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	modelDir, err := st.ModelDir("org/model")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "gguf-bytes"
	if err := os.WriteFile(filepath.Join(modelDir, "model.gguf"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	entries := []manifest.File{{Path: "model.gguf", Size: int64(len(content))}}

	// No pointer file: not cached.
	if m, ok, cerr := checkCache(st, "org/model", "cafe123", modelDir); m != nil || ok || cerr != nil {
		t.Fatalf("no pointer: (%v, %v, %v)", m, ok, cerr)
	}

	mOther, err := manifest.Create("org/model", "other-rev", entries, modelDir)
	if err != nil {
		t.Fatal(err)
	}
	ptr, err := st.ModelManifestFile("org/model")
	if err != nil {
		t.Fatal(err)
	}
	if err := mOther.Save(ptr); err != nil {
		t.Fatal(err)
	}
	// Pointer pins a different revision: not cached.
	if _, ok, cerr := checkCache(st, "org/model", "cafe123", modelDir); ok || cerr != nil {
		t.Fatalf("revision mismatch: (%v, %v)", ok, cerr)
	}

	mMatch, err := manifest.Create("org/model", "cafe123", entries, modelDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := mMatch.Save(ptr); err != nil {
		t.Fatal(err)
	}
	// Matching revision, intact files: cache hit.
	m, ok, cerr := checkCache(st, "org/model", "cafe123", modelDir)
	if !ok || cerr != nil || m == nil || m.InfoHash != mMatch.InfoHash {
		t.Fatalf("cache hit: (%v, %v, %v)", m != nil, ok, cerr)
	}

	// Corrupted file: reported as an error (triggers re-pull upstream).
	if err := os.WriteFile(filepath.Join(modelDir, "model.gguf"), []byte("tampered!!"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, cerr = checkCache(st, "org/model", "cafe123", modelDir)
	if cerr == nil || !strings.Contains(cerr.Error(), "corrupt") {
		t.Fatalf("corrupt cache err = %v, want corruption report", cerr)
	}
}

func TestPullP2PNoIndexEntry(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	modelDir, err := st.ModelDir("org/model")
	if err != nil {
		t.Fatal(err)
	}
	_, err = pullP2P(context.Background(), pullRef(t), Options{
		HTTPClient:    http.DefaultClient,
		BootstrapURLs: []string{emptyBootstrap(t)},
	}, "cafe123", nil, modelDir, time.Second)
	if err == nil || !strings.Contains(err.Error(), "no bootstrap index entry") {
		t.Fatalf("err = %v, want missing entry", err)
	}
}

func TestPullP2PRevisionMismatch(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	modelDir, err := st.ModelDir("org/model")
	if err != nil {
		t.Fatal(err)
	}
	boot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"entries":{"org/model":{"model":"org/model","infoHash":"%s","manifestSha256":"%s","revision":"old-rev"}}}`,
			strings.Repeat("a", 40), strings.Repeat("b", 64))
	}))
	t.Cleanup(boot.Close)

	_, err = pullP2P(context.Background(), pullRef(t), Options{
		HTTPClient:    http.DefaultClient,
		BootstrapURLs: []string{boot.URL},
	}, "cafe123", nil, modelDir, time.Second)
	if err == nil || !strings.Contains(err.Error(), "swarm pinned") {
		t.Fatalf("err = %v, want revision mismatch", err)
	}
}

func TestFetchManifestErrors(t *testing.T) {
	// Origin serves a manifest whose hash does not match the pinned entry.
	boot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"schema":"llmp2p/v1","model":"x","revision":"y"}`))
	}))
	t.Cleanup(boot.Close)
	entry := index.Entry{Model: "org/model", InfoHash: strings.Repeat("a", 40),
		ManifestSHA256: strings.Repeat("b", 64), Revision: "cafe123"}
	if _, err := fetchManifest(http.DefaultClient, []string{boot.URL}, entry, "cafe123"); err == nil {
		t.Fatal("fetchManifest accepted a mismatching manifest")
	}

	// Dead origin: connection refused.
	if _, err := fetchManifest(http.DefaultClient, []string{"http://127.0.0.1:1"}, entry, "cafe123"); err == nil {
		t.Fatal("fetchManifest succeeded against a dead origin")
	}
}

// manifestFixture builds a one-file model dir plus its manifest and index
// entry, ready to be served by a bootstrap origin.
func manifestFixture(t *testing.T, revision string) (dir string, m *manifest.Manifest, entry index.Entry) {
	t.Helper()
	dir = t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "gguf-bytes"
	if err := os.WriteFile(filepath.Join(dir, "model.gguf"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Create("org/model", revision, []manifest.File{
		{Path: "model.gguf", Size: int64(len(content))},
	}, dir)
	if err != nil {
		t.Fatal(err)
	}
	msha, err := m.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	return dir, m, index.Entry{Model: "org/model", InfoHash: m.InfoHash,
		ManifestSHA256: msha, Revision: revision, Size: int64(len(content))}
}

func TestFetchManifestSignatureBadJSON(t *testing.T) {
	_, m, entry := manifestFixture(t, "cafe123")
	boot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	t.Cleanup(boot.Close)
	mBytes, err := m.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	// The manifest must be fetchable for verifyManifestSignature to reach
	// the sidecar; here we exercise the sidecar parser directly.
	if _, _, err := fetchManifestSignature(http.DefaultClient, []string{boot.URL}, entry.ManifestSHA256); err == nil {
		t.Fatal("bad sidecar JSON accepted")
	}
	_ = mBytes
}

func TestVerifyManifestSignatureCases(t *testing.T) {
	modelDir, m, entry := manifestFixture(t, "cafe123")
	msha := entry.ManifestSHA256
	canonical, err := m.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), signing.DefaultKeyFile)
	priv, _, err := signing.LoadOrCreate(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	pubHex := signing.PublicKeyHex(priv)
	goodSig := signing.Sign(priv, canonical)

	serve := func(body string) *httptest.Server {
		boot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(boot.Close)
		return boot
	}

	// Invalid signature bytes.
	bad, _ := json.Marshal(manifestSignature{ManifestSHA256: msha,
		Signature: hex.EncodeToString(make([]byte, 64)), PublicKey: pubHex})
	bootBad := serve(string(bad))
	opts := Options{HTTPClient: http.DefaultClient}
	if err := verifyManifestSignature(opts, m, []string{bootBad.URL}); err == nil {
		t.Fatal("invalid signature accepted")
	}

	// Valid signature, signer outside the allowlist.
	bootGood := serve(mustSidecarJSON(t, msha, goodSig, pubHex))
	strict := Options{HTTPClient: http.DefaultClient, AllowedSigners: []string{strings.Repeat("f", 64)}}
	if err := verifyManifestSignature(strict, m, []string{bootGood.URL}); err == nil {
		t.Fatal("non-allowlisted signer accepted")
	}

	// Valid signature, empty allowlist: accepted with a warning.
	if err := verifyManifestSignature(opts, m, []string{bootGood.URL}); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}

	// Valid signature, signer on the allowlist: accepted.
	allow := Options{HTTPClient: http.DefaultClient, AllowedSigners: []string{pubHex}}
	if err := verifyManifestSignature(allow, m, []string{bootGood.URL}); err != nil {
		t.Fatalf("allowlisted signer rejected: %v", err)
	}
	_ = modelDir
}

func mustSidecarJSON(t *testing.T, msha, sig, pubHex string) string {
	t.Helper()
	b, err := json.Marshal(manifestSignature{ManifestSHA256: msha, Signature: sig, PublicKey: pubHex})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRunSingleFilePull(t *testing.T) {
	var hits atomic.Int64
	hub := fakeHub(t, &hits)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r, err := ref.Parse("hf:org/model#model.gguf")
	if err != nil {
		t.Fatal(err)
	}
	var progressModes []string
	res, err := Run(context.Background(), r, Options{
		Store:         st,
		HF:            hubAt(hub.URL),
		BootstrapURLs: []string{emptyBootstrap(t)},
		HTTPClient:    http.DefaultClient,
		EngineCfg:     engine.Config{NoDHT: true},
		OnProgress: func(mode string, p engine.Progress) {
			progressModes = append(progressModes, mode)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != ModeHTTP || res.Files != 1 {
		t.Fatalf("result = %+v", res)
	}
	if len(progressModes) == 0 || progressModes[0] != ModeHTTP {
		t.Fatalf("progress modes = %v", progressModes)
	}
	// Path pulls never publish a local index entry.
	if _, err := os.Stat(st.LocalIndexPath()); !os.IsNotExist(err) {
		t.Fatal("single-file pull must not publish a local index")
	}
}

func TestRunNoFileAtPath(t *testing.T) {
	var hits atomic.Int64
	hub := fakeHub(t, &hits)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r, err := ref.Parse("hf:org/model#missing.gguf")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Run(context.Background(), r, Options{
		Store:         st,
		HF:            hubAt(hub.URL),
		BootstrapURLs: []string{emptyBootstrap(t)},
		HTTPClient:    http.DefaultClient,
	})
	if err == nil || !strings.Contains(err.Error(), `no file "missing.gguf"`) {
		t.Fatalf("err = %v, want missing file at path", err)
	}
}

func TestP2PGraceFallsBackToHTTP(t *testing.T) {
	var hits atomic.Int64
	hub := fakeHub(t, &hits)
	_, m, entry := manifestFixture(t, "cafe123")
	mBytes, err := m.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	boot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.json":
			_, _ = fmt.Fprintf(w, `{"entries":{"org/model":{"model":"org/model","infoHash":"%s","manifestSha256":"%s","revision":"cafe123","size":%d}}}`,
				entry.InfoHash, entry.ManifestSHA256, entry.Size)
		case "/manifests/" + entry.ManifestSHA256 + ".json":
			_, _ = w.Write(mBytes)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(boot.Close)

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), pullRef(t), Options{
		Store:         st,
		HF:            hubAt(hub.URL),
		BootstrapURLs: []string{boot.URL},
		HTTPClient:    http.DefaultClient,
		EngineCfg:     engine.Config{NoDHT: true, DisableUTP: true},
		P2PGrace:      1100 * time.Millisecond,
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != ModeHTTP {
		t.Fatalf("mode = %q, want http after grace timeout", res.Mode)
	}
}

func TestSignManifestWithBrokenKeyStillPublishes(t *testing.T) {
	var hits atomic.Int64
	hub := fakeHub(t, &hits)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Root(), signing.DefaultKeyFile), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), pullRef(t), Options{
		Store:         st,
		HF:            hubAt(hub.URL),
		BootstrapURLs: []string{emptyBootstrap(t)},
		HTTPClient:    http.DefaultClient,
		EngineCfg:     engine.Config{NoDHT: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	sigPath, err := st.SignaturePath(res.ManifestSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if _, serr := os.Stat(sigPath); !os.IsNotExist(serr) {
		t.Fatal("no sidecar must be written when the key is unusable")
	}
}

func TestPullHTTPReusesMatchingLFSFile(t *testing.T) {
	var hits atomic.Int64
	hub := fakeHub(t, &hits)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	opts := func() Options {
		return Options{
			Store:         st,
			HF:            hubAt(hub.URL),
			BootstrapURLs: []string{emptyBootstrap(t)},
			HTTPClient:    http.DefaultClient,
			EngineCfg:     engine.Config{NoDHT: true},
		}
	}
	if _, err := Run(context.Background(), pullRef(t), opts()); err != nil {
		t.Fatal(err)
	}
	// Break the manifest pointer so the cache misses, but keep the files.
	ptr, err := st.ModelManifestFile("org/model")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(ptr); err != nil {
		t.Fatal(err)
	}
	before := hits.Load()
	res, err := Run(context.Background(), pullRef(t), opts())
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != ModeHTTP {
		t.Fatalf("mode = %q, want http (cache broken)", res.Mode)
	}
	// Only the revision resolve + the non-LFS config.json are re-fetched;
	// the LFS gguf matching its Hub sha256 is kept.
	if got := hits.Load() - before; got != 2 {
		t.Fatalf("hub requests after re-pull = %d, want 2 (LFS file reused)", got)
	}
}

func TestPullHTTPDownloadFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/models/org/model/revision/main", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"sha":"cafe123"}`)
	})
	mux.HandleFunc("/api/models/org/model/tree/cafe123", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `[{"type":"file","path":"model.gguf","size":%d,"lfs":{"oid":"%s"}}]`,
			len(ggufContent), ggufSHA)
	})
	mux.HandleFunc("/org/model/resolve/cafe123/model.gguf", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = Run(context.Background(), pullRef(t), Options{
		Store:      st,
		HF:         hubAt(srv.URL),
		HTTPClient: http.DefaultClient,
		EngineCfg:  engine.Config{NoDHT: true},
	})
	if err == nil || !strings.Contains(err.Error(), "download model.gguf") {
		t.Fatalf("err = %v, want download failure", err)
	}
}

func TestPublishLocalIndexParseErrorRecovers(t *testing.T) {
	var hits atomic.Int64
	hub := fakeHub(t, &hits)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.LocalIndexPath(), []byte("{garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), pullRef(t), Options{
		Store:         st,
		HF:            hubAt(hub.URL),
		BootstrapURLs: []string{emptyBootstrap(t)},
		HTTPClient:    http.DefaultClient,
		EngineCfg:     engine.Config{NoDHT: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(st.LocalIndexPath())
	if err != nil {
		t.Fatal(err)
	}
	ix, _, err := index.Parse(b)
	if err != nil {
		t.Fatalf("index not rewritten after parse failure: %v", err)
	}
	if e, ok := ix.Get("org/model"); !ok || e.InfoHash != res.InfoHash {
		t.Fatalf("local index entry = %+v ok=%v", e, ok)
	}
}

func TestEngBytesUnknownTorrent(t *testing.T) {
	e, err := engine.New(engine.Config{DataDir: t.TempDir(), NoDHT: true, DisableUTP: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Close() }()
	const ih = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := e.PrepareMagnet(ih); err != nil {
		t.Fatal(err)
	}
	if got := engBytes(e, strings.Repeat("b", 40)); got != 0 {
		t.Fatalf("engBytes(mismatch) = %d, want 0", got)
	}
	if got := engBytes(e, ih); got != 0 {
		t.Fatalf("engBytes(empty torrent) = %d, want 0", got)
	}
}

func TestPullP2PBadInfoHash(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	modelDir, err := st.ModelDir("org/model")
	if err != nil {
		t.Fatal(err)
	}
	boot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"entries":{"org/model":{"model":"org/model","infoHash":"tooshort","manifestSha256":"%s","revision":"cafe123"}}}`,
			strings.Repeat("b", 64))
	}))
	t.Cleanup(boot.Close)

	// The bootstrap index validates entries strictly: an entry with a
	// malformed infohash is dropped before the engine ever sees it.
	_, err = pullP2P(context.Background(), pullRef(t), Options{
		HTTPClient:    http.DefaultClient,
		BootstrapURLs: []string{boot.URL},
	}, "cafe123", nil, modelDir, time.Second)
	if err == nil || !strings.Contains(err.Error(), "no bootstrap index entry") {
		t.Fatalf("err = %v, want strict index validation to drop the entry", err)
	}
}
