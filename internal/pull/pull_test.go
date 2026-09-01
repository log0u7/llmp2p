package pull

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
