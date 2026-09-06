package pull

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	anadht "github.com/anacrolix/dht/v2"

	"github.com/log0u7/llmp2p/internal/dht"
	"github.com/log0u7/llmp2p/internal/engine"
	"github.com/log0u7/llmp2p/internal/signing"
	"github.com/log0u7/llmp2p/internal/store"
)

// testDHTNode binds an in-process DHT node and returns its udp address
// (127.0.0.1:port) for use as opts.DHTAddrs.
func testDHTNode(t *testing.T) string {
	t.Helper()
	conn, err := dht.ListenUDP()
	if err != nil {
		t.Fatal(err)
	}
	srv, err := dht.NewServer(conn)
	if err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		srv.Close()
		_ = conn.Close()
	})
	return fmt.Sprintf("127.0.0.1:%d", srv.Addr().(*net.UDPAddr).Port)
}

// udpAddrOf converts a "127.0.0.1:port" string into a node address.
func udpAddrOf(addr string) anadht.Addr {
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		panic(err)
	}
	return anadht.NewAddr(ua)
}

// storedManifestAndSig reads the content-addressed manifest and its
// signature sidecar from the store, ready to be served by a bootstrap
// origin.
func storedManifestAndSig(t *testing.T, st *store.Store, msha string) ([]byte, []byte) {
	t.Helper()
	mpath, err := st.ManifestPath(msha)
	if err != nil {
		t.Fatal(err)
	}
	mBytes, err := os.ReadFile(mpath)
	if err != nil {
		t.Fatal(err)
	}
	spath, err := st.SignaturePath(msha)
	if err != nil {
		t.Fatal(err)
	}
	sigBytes, err := os.ReadFile(spath)
	if err != nil {
		t.Fatal(err)
	}
	return mBytes, sigBytes
}

func mustPublisherKey(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	p, _, err := signing.LoadOrCreate(filepath.Join(t.TempDir(), signing.DefaultKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	return p, signing.PublicKeyHex(p)
}

// dhtClientNode binds a second DHT node used purely to query the first
// one (a get against the storing node itself works too; two nodes mirror
// the real topology).
func dhtClientNode(t *testing.T) (srv *anadht.Server) {
	t.Helper()
	conn, err := dht.ListenUDP()
	if err != nil {
		t.Fatal(err)
	}
	srv, err = dht.NewServer(conn)
	if err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		srv.Close()
		_ = conn.Close()
	})
	return srv
}

// TestDHTPublishAndDiscover runs the full v0.2 loop: a first pull
// publishes a signed record to a local DHT node, a second pull into a
// fresh store discovers the swarm through that record alone.
func TestDHTPublishAndDiscover(t *testing.T) {
	var hits atomic.Int64
	hub := fakeHub(t, &hits)
	nodeAddr := testDHTNode(t)

	// Seed store with a publisher key: the pull publishes a DHT record.
	seedStore, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	priv, _, err := signing.LoadOrCreate(filepath.Join(seedStore.Root(), signing.DefaultKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	pubHex := signing.PublicKeyHex(priv)

	first, err := Run(context.Background(), pullRef(t), Options{
		Store:         seedStore,
		HF:            hubAt(hub.URL),
		BootstrapURLs: []string{emptyBootstrap(t)},
		HTTPClient:    http.DefaultClient,
		EngineCfg:     engine.Config{NoDHT: true},
		DHT:           true,
		DHTAddrs:      []string{nodeAddr},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Mode != ModeHTTP {
		t.Fatalf("seed pull mode = %q", first.Mode)
	}

	// The record is discoverable and pins the published artifacts.
	client, err := dht.NewClient([]string{pubHex})
	if err != nil {
		t.Fatal(err)
	}
	dhtSrv := dhtClientNode(t)
	rec, err := client.Get(context.Background(), dhtSrv, udpAddrOf(nodeAddr), "org/model")
	if err != nil {
		t.Fatalf("record not discoverable: %v", err)
	}
	if rec.Revision != "cafe123" || hex.EncodeToString(rec.InfoHash) != first.InfoHash ||
		hex.EncodeToString(rec.ManifestSHA256) != first.ManifestSHA256 {
		t.Fatalf("record = %+v, want infohash %s manifest %s", rec, first.InfoHash, first.ManifestSHA256)
	}

	// Bootstrap origin serves manifest + signature sidecar (bytes still
	// flow over HTTPS origins).
	mBytes, sigBytes := storedManifestAndSig(t, seedStore, first.ManifestSHA256)
	boot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.json":
			_, _ = fmt.Fprintf(w, `{"entries":{}}`) // index empty: discovery is DHT-only
		case "/manifests/" + first.ManifestSHA256 + ".json":
			_, _ = w.Write(mBytes)
		case "/manifests/" + first.ManifestSHA256 + ".json.sig":
			_, _ = w.Write(sigBytes)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer boot.Close()

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

	// Leech store: DHT-only discovery (empty index), signed record.
	leechStore, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	leechDir, err := leechStore.ModelDir("org/model")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(ctx, pullRef(t), Options{
		Store:          leechStore,
		HF:             hubAt(hub.URL),
		BootstrapURLs:  []string{boot.URL},
		HTTPClient:     http.DefaultClient,
		EngineCfg:      engine.Config{NoDHT: true, DisableUTP: true},
		EngineAddrs:    []string{fmt.Sprintf("127.0.0.1:%d", seederPort)},
		DHT:            true,
		DHTAddrs:       []string{nodeAddr},
		AllowedSigners: []string{pubHex},
		P2PGrace:       30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != ModeP2P {
		t.Fatalf("mode = %q, want p2p via DHT discovery", res.Mode)
	}
	got, err := os.ReadFile(filepath.Join(leechDir, "model.gguf"))
	if err != nil || string(got) != ggufContent {
		t.Fatalf("p2p file mismatch: err=%v", err)
	}
}

func TestDHTFallsBackWithoutAllowlist(t *testing.T) {
	var hits atomic.Int64
	hub := fakeHub(t, &hits)
	nodeAddr := testDHTNode(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// DHT is on but no allowlist: no trusted target can be derived, the
	// bootstrap index is empty, so the pull falls back to HTTP.
	res, err := Run(context.Background(), pullRef(t), Options{
		Store:         st,
		HF:            hubAt(hub.URL),
		BootstrapURLs: []string{emptyBootstrap(t)},
		HTTPClient:    http.DefaultClient,
		EngineCfg:     engine.Config{NoDHT: true},
		DHT:           true,
		DHTAddrs:      []string{nodeAddr},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != ModeHTTP {
		t.Fatalf("mode = %q, want http fallback", res.Mode)
	}
}

func TestDHTRecordRevisionMismatchFallsBack(t *testing.T) {
	var hits atomic.Int64
	hub := fakeHub(t, &hits)
	nodeAddr := testDHTNode(t)

	// Publish a record pinned to a stale revision.
	priv, pubHex := mustPublisherKey(t)
	dhtSrv := dhtClientNode(t)
	pub := dht.NewPublisher(priv, filepath.Join(t.TempDir(), "seq.json"))
	stale := dht.Record{
		InfoHash:       []byte(strings.Repeat("\xaa", 20)),
		ManifestSHA256: []byte(strings.Repeat("\xbb", 32)),
		Revision:       "oldrev",
		Size:           1,
	}
	if err := pub.Put(context.Background(), dhtSrv, udpAddrOf(nodeAddr), "org/model", stale); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), pullRef(t), Options{
		Store:          st,
		HF:             hubAt(hub.URL),
		BootstrapURLs:  []string{emptyBootstrap(t)},
		HTTPClient:     http.DefaultClient,
		EngineCfg:      engine.Config{NoDHT: true},
		DHT:            true,
		DHTAddrs:       []string{nodeAddr},
		AllowedSigners: []string{pubHex},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != ModeHTTP {
		t.Fatalf("mode = %q, want http fallback on revision mismatch", res.Mode)
	}
}

func TestPublishToDHTWithoutKeyIsBestEffort(t *testing.T) {
	var hits atomic.Int64
	hub := fakeHub(t, &hits)
	nodeAddr := testDHTNode(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// No keygen: publication is skipped, the pull still succeeds.
	res, err := Run(context.Background(), pullRef(t), Options{
		Store:         st,
		HF:            hubAt(hub.URL),
		BootstrapURLs: []string{emptyBootstrap(t)},
		HTTPClient:    http.DefaultClient,
		EngineCfg:     engine.Config{NoDHT: true},
		DHT:           true,
		DHTAddrs:      []string{nodeAddr},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != ModeHTTP {
		t.Fatalf("mode = %q", res.Mode)
	}
}
