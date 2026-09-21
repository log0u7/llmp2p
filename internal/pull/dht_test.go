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

// bindDHTNode binds an in-process DHT node. Used both as an
// opts.DHTAddrs entry (via its udp address) and as a second node purely
// to query the first one (a get against the storing node itself works
// too; two nodes mirror the real topology).
func bindDHTNode(t *testing.T) (*anadht.Server, string) {
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
	return srv, fmt.Sprintf("127.0.0.1:%d", srv.Addr().(*net.UDPAddr).Port)
}

// testDHTNode binds a DHT node and returns its udp address
// (127.0.0.1:port) for use as opts.DHTAddrs.
func testDHTNode(t *testing.T) string {
	_, addr := bindDHTNode(t)
	return addr
}

// dhtClientNode binds a second DHT node used purely to query the first
// one.
func dhtClientNode(t *testing.T) (srv *anadht.Server) {
	s, _ := bindDHTNode(t)
	return s
}

// udpAddrOf converts a "127.0.0.1:port" string into a node address.
func udpAddrOf(addr string) anadht.Addr {
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		panic(err)
	}
	return anadht.NewAddr(ua)
}

func mustPublisherKey(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	p, _, err := signing.LoadOrCreate(filepath.Join(t.TempDir(), signing.DefaultKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	return p, signing.PublicKeyHex(p)
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

	// The record is discoverable and pins the published artifacts; the
	// small fixture manifest fits the embed cap.
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
	if len(rec.Manifest) == 0 {
		t.Fatal("record must embed the manifest bytes for small manifests")
	}

	// Bootstrap origin serves only an empty index: no manifest bytes, no
	// signature sidecar. The pull below must succeed on the embedded
	// manifest alone.
	boot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"entries":{}}`))
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

func TestDHTInvalidInputsFallBack(t *testing.T) {
	var hits atomic.Int64
	hub := fakeHub(t, &hits)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Malformed allowlist entry: discovery aborts before any query.
	res, err := Run(context.Background(), pullRef(t), Options{
		Store:          st,
		HF:             hubAt(hub.URL),
		BootstrapURLs:  []string{emptyBootstrap(t)},
		HTTPClient:     http.DefaultClient,
		EngineCfg:      engine.Config{NoDHT: true},
		DHT:            true,
		DHTAddrs:       []string{testDHTNode(t)},
		AllowedSigners: []string{"nothex"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != ModeHTTP {
		t.Fatalf("mode = %q, want http fallback", res.Mode)
	}

	// Unusable node addresses: same outcome.
	st2, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, pubHex := mustPublisherKey(t)
	res2, err := Run(context.Background(), pullRef(t), Options{
		Store:          st2,
		HF:             hubAt(hub.URL),
		BootstrapURLs:  []string{emptyBootstrap(t)},
		HTTPClient:     http.DefaultClient,
		EngineCfg:      engine.Config{NoDHT: true},
		DHT:            true,
		DHTAddrs:       []string{"!!!not-a-addr"},
		AllowedSigners: []string{pubHex},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Mode != ModeHTTP {
		t.Fatalf("mode = %q, want http fallback", res2.Mode)
	}
}

func TestDHTPointerOnlyRecordFallsBackToHTTP(t *testing.T) {
	var hits atomic.Int64
	hub := fakeHub(t, &hits)
	nodeAddr := testDHTNode(t)

	// Pointer-only record (what an oversized manifest produces): signed,
	// discoverable, but no embedded bytes.
	priv, pubHex := mustPublisherKey(t)
	dhtSrv := dhtClientNode(t)
	pointerOnly := dht.Record{
		InfoHash:       []byte(strings.Repeat("\xaa", 20)),
		ManifestSHA256: []byte(strings.Repeat("\xbb", 32)),
		Revision:       "cafe123",
		Size:           1,
	}
	pub := dht.NewPublisher(priv, filepath.Join(t.TempDir(), "seq.json"))
	if err := pub.Put(context.Background(), dhtSrv, udpAddrOf(nodeAddr), "org/model", pointerOnly); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Discovery finds a pointer-only record: no origins are configured,
	// so the manifest bytes cannot be fetched and the pull falls back to
	// the Hub over HTTP.
	res, err := Run(context.Background(), pullRef(t), Options{
		Store:          st,
		HF:             hubAt(hub.URL),
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
		t.Fatalf("mode = %q, want http fallback for a pointer-only record without origins", res.Mode)
	}
}

func TestNoHTTPFallbackBlocksFallback(t *testing.T) {
	var hits atomic.Int64
	hub := fakeHub(t, &hits)
	nodeAddr := testDHTNode(t)
	priv, pubHex := mustPublisherKey(t)
	dhtSrv := dhtClientNode(t)
	// Pointer-only record: discovery succeeds but the manifest bytes are
	// nowhere to be found (no origins configured).
	pointerOnly := dht.Record{
		InfoHash:       []byte(strings.Repeat("\xaa", 20)),
		ManifestSHA256: []byte(strings.Repeat("\xbb", 32)),
		Revision:       "cafe123",
		Size:           1,
	}
	pub := dht.NewPublisher(priv, filepath.Join(t.TempDir(), "seq.json"))
	if err := pub.Put(context.Background(), dhtSrv, udpAddrOf(nodeAddr), "org/model", pointerOnly); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The Hub resolve works, but the p2p attempt cannot fetch the
	// manifest: with NoHTTPFallback the run must fail instead of
	// silently downloading from the Hub.
	_, err = Run(context.Background(), pullRef(t), Options{
		Store:          st,
		HF:             hubAt(hub.URL),
		HTTPClient:     http.DefaultClient,
		EngineCfg:      engine.Config{NoDHT: true},
		DHT:            true,
		DHTAddrs:       []string{nodeAddr},
		AllowedSigners: []string{pubHex},
		NoHTTPFallback: true,
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP fallback is disabled") {
		t.Fatalf("err = %v, want disabled-fallback error", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("hub hits = %d, want 1 (resolve only, no downloads)", hits.Load())
	}
}
