package dht

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	anadht "github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/bep44"
	bencode "github.com/anacrolix/torrent/bencode"
)

func testRecord(t *testing.T) Record {
	t.Helper()
	return Record{
		InfoHash:       mustBytes(t, strings.Repeat("\xaa", 20)),
		ManifestSHA256: mustBytes(t, strings.Repeat("\xbb", 32)),
		Revision:       "cafe123",
		Size:           12345,
	}
}

func mustBytes(t *testing.T, s string) []byte {
	t.Helper()
	return []byte(s)
}

func testKey(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return priv, fmt.Sprintf("%x", pub)
}

// newTestNode binds a DHT server on localhost with an in-memory store.
func newTestNode(t *testing.T) *anadht.Server {
	t.Helper()
	conn, err := ListenUDP()
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(conn)
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

func filepathSeqState(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "dht-seq.json")
}

// nodeAddr rewrites the server's wildcard listen address to the loopback
// IP: writing to [::]:port fails with sendto EINVAL.
func nodeAddr(srv *anadht.Server) anadht.Addr {
	port := srv.Addr().(*net.UDPAddr).Port
	return anadht.NewAddr(&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
}

func TestRecordRoundTrip(t *testing.T) {
	rec := testRecord(t)
	b, err := rec.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > 1000 {
		t.Fatalf("record marshals to %d bytes, must stay under the BEP 44 limit", len(b))
	}
	got, err := DecodeRecord(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.InfoHash) != string(rec.InfoHash) ||
		string(got.ManifestSHA256) != string(rec.ManifestSHA256) ||
		got.Revision != rec.Revision || got.Size != rec.Size {
		t.Fatalf("round trip = %+v, want %+v", got, rec)
	}
}

// Trap 2 regression: a string-shaped value must be rejected by the strict
// decoder, and dict values must be parsed from the raw reply bytes.
func TestDecodeRecordStrictness(t *testing.T) {
	if _, err := DecodeRecord([]byte("a plain string")); err == nil {
		t.Fatal("string-shaped value accepted")
	}
	if _, err := DecodeRecord([]byte("le")); err == nil {
		t.Fatal("list-shaped value accepted")
	}
	if _, err := DecodeRecord([]byte(`d1:i20:short1:m32:` + strings.Repeat("a", 32) + `1:r7:cafe1231:si5ee`)); err == nil {
		t.Fatal("short infohash accepted")
	}
	badRev, err := bencode.Marshal(Record{InfoHash: mustBytes(t, strings.Repeat("a", 20)),
		ManifestSHA256: mustBytes(t, strings.Repeat("b", 32)), Size: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRecord(badRev); err == nil {
		t.Fatal("record without revision accepted")
	}
}

// Trap 1 regression: a zero expiration expires every stored record
// instantly; the wrapper with an explicit expiration serves them.
func TestStoreExpirationMustBeExplicit(t *testing.T) {
	item, err := bep44.NewItem("value", nil, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	zero := bep44.NewWrapper(bep44.NewMemory(), 0)
	if err := zero.Put(item); err != nil {
		t.Fatal(err)
	}
	if _, err := zero.Get(item.Target()); !errors.Is(err, bep44.ErrItemNotFound) {
		t.Fatalf("zero expiration err = %v, want instant expiration", err)
	}

	fresh := bep44.NewWrapper(bep44.NewMemory(), DefaultRecordExpiration)
	if err := fresh.Put(item); err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.Get(item.Target()); err != nil {
		t.Fatalf("explicit expiration err = %v", err)
	}
}

// Trap 3 regression: the signature covers the salt; verifying with a
// different salt (or storing a put without its salt) fails.
func TestSignatureCoversSalt(t *testing.T) {
	priv, pubHex := testKey(t)
	rec := testRecord(t)
	salt := SaltFor("org/model")
	item, err := bep44.NewItem(rec, salt, 1, 0, priv)
	if err != nil {
		t.Fatal(err)
	}
	if !bep44.Verify(ed25519.PublicKey(item.K[:]), salt, item.Seq, rec.encodeFor(t), item.Sig[:]) {
		t.Fatal("signature does not verify with its own salt")
	}
	otherSalt := SaltFor("org/other")
	if bep44.Verify(ed25519.PublicKey(item.K[:]), otherSalt, item.Seq, rec.encodeFor(t), item.Sig[:]) {
		t.Fatal("signature verified with the wrong salt")
	}
	if pubHex == "" {
		t.Fatal("empty public key hex")
	}
}

func (r Record) encodeFor(t *testing.T) []byte {
	t.Helper()
	b, err := r.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestPutGetMutable runs a full publisher -> client exchange between two
// in-process DHT nodes: the publisher node stores, the client node queries.
func TestPutGetMutable(t *testing.T) {
	storeNode := newTestNode(t)
	queryNode := newTestNode(t)
	priv, pubHex := testKey(t)
	rec := testRecord(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pub := NewPublisher(priv, filepathSeqState(t))
	if err := pub.Put(ctx, queryNode, nodeAddr(storeNode), "org/model", rec); err != nil {
		t.Fatalf("put: %v", err)
	}

	client, err := NewClient([]string{pubHex})
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.Get(ctx, queryNode, nodeAddr(storeNode), "org/model")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got.InfoHash) != string(rec.InfoHash) || got.Revision != rec.Revision || got.Size != rec.Size {
		t.Fatalf("record = %+v, want %+v", got, rec)
	}

	// An untrusted key derives a different target: nothing is found.
	clientOther, err := NewClient([]string{func() string {
		_, otherHex := testKey(t)
		return otherHex
	}()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clientOther.Get(ctx, queryNode, nodeAddr(storeNode), "org/model"); err == nil {
		t.Fatal("record found under an untrusted key")
	}
}

func TestSequenceNumbersMustIncrease(t *testing.T) {
	storeNode := newTestNode(t)
	priv, _ := testKey(t)
	rec := testRecord(t)
	salt := SaltFor("org/model")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	put := func(seq int64) error {
		item, err := bep44.NewItem(rec, salt, seq, 0, priv)
		if err != nil {
			t.Fatal(err)
		}
		qr := storeNode.Get(ctx, nodeAddr(storeNode), item.Target(), nil, anadht.QueryRateLimiting{})
		if err := qr.ToError(); err != nil {
			return fmt.Errorf("token: %w", err)
		}
		qr = storeNode.Put(ctx, nodeAddr(storeNode), item.ToPut(), *qr.Reply.R.Token, anadht.QueryRateLimiting{})
		return qr.ToError()
	}

	if err := put(1); err != nil {
		t.Fatalf("first put: %v", err)
	}
	if err := put(2); err != nil {
		t.Fatalf("second put: %v", err)
	}
	if err := put(1); err == nil {
		t.Fatal("stale sequence number accepted")
	}
}

func TestPublisherSequencePersists(t *testing.T) {
	priv, _ := testKey(t)
	path := filepathSeqState(t)
	if _, err := NewPublisher(priv, path).seqs.next(SaltFor("org/model")); err != nil {
		t.Fatal(err)
	}
	p2 := NewPublisher(priv, path)
	s1, err := p2.seqs.next(SaltFor("org/model"))
	if err != nil {
		t.Fatal(err)
	}
	if s1 != 2 {
		t.Fatalf("reloaded seq = %d, want 2", s1)
	}
	s2, err := p2.seqs.next(SaltFor("org/other"))
	if err != nil {
		t.Fatal(err)
	}
	if s2 != 1 {
		t.Fatalf("fresh salt seq = %d, want 1", s2)
	}
}

func TestNewClientValidatesKeys(t *testing.T) {
	if _, err := NewClient([]string{"nothex"}); err == nil {
		t.Fatal("accepted non-hex key")
	}
	if _, err := NewClient([]string{"aabb"}); err == nil {
		t.Fatal("accepted short key")
	}
	priv, pubHex := testKey(t)
	if priv == nil || len(pubHex) != 64 {
		t.Fatal("bad test key")
	}
	if _, err := NewClient([]string{pubHex}); err != nil {
		t.Fatalf("rejected a valid key: %v", err)
	}
}

// manifestJSON returns a realistic canonical manifest payload (small
// enough to embed).
func manifestJSON(t *testing.T, model string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"schema": "llmp2p/v1", "model": model, "revision": "cafe123",
		"files": []map[string]any{{"path": "model.gguf", "size": 5,
			"sha256": strings.Repeat("a", 64)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func recordWithManifest(t *testing.T, model string) Record {
	t.Helper()
	mj := manifestJSON(t, model)
	sum := sha256.Sum256(mj)
	return Record{
		InfoHash:       mustBytes(t, strings.Repeat("\xaa", 20)),
		ManifestSHA256: sum[:],
		Revision:       "cafe123",
		Size:           5,
		Manifest:       mj,
	}
}

func TestRecordEmbedsManifest(t *testing.T) {
	rec := recordWithManifest(t, "org/model")
	b, err := rec.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > 1000 {
		t.Fatalf("embedded record marshals to %d bytes, over the BEP 44 limit", len(b))
	}
	// The wire shape must carry the manifest field when present...
	if !strings.Contains(string(b), "1:j") {
		t.Fatalf("wire shape missing j field: %q", b)
	}
	got, err := DecodeRecord(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Manifest) != string(rec.Manifest) {
		t.Fatalf("embedded manifest = %q, want %q", got.Manifest, rec.Manifest)
	}

	// ...and omit it entirely for pointer-only records.
	ptrOnly := testRecord(t)
	ptrOnly.Manifest = nil
	ptrBytes, err := ptrOnly.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ptrBytes), "1:j") {
		t.Fatalf("pointer-only record must not carry a j field: %q", ptrBytes)
	}
}

func TestRecordRejectsDigestMismatch(t *testing.T) {
	rec := recordWithManifest(t, "org/model")
	other := manifestJSON(t, "org/other")
	rec.Manifest = other
	b, err := rec.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRecord(b); err == nil {
		t.Fatal("DecodeRecord accepted an embedded manifest with a foreign digest")
	}
}

func TestRecordRejectsOversizedManifest(t *testing.T) {
	rec := testRecord(t)
	rec.Manifest = make([]byte, MaxEmbeddedManifest+1)
	if _, err := rec.Encode(); err != nil {
		t.Fatal(err)
	}
	b, _ := rec.Encode()
	if _, err := DecodeRecord(b); err == nil {
		t.Fatal("DecodeRecord accepted an oversized embedded manifest")
	}
}

func TestPublisherRejectsOversizedManifest(t *testing.T) {
	priv, _ := testKey(t)
	p := NewPublisher(priv, filepathSeqState(t))
	rec := testRecord(t)
	rec.Manifest = make([]byte, MaxEmbeddedManifest+1)
	node := newTestNode(t)
	err := p.Put(context.Background(), node, nodeAddr(node), "org/model", rec)
	if err == nil || !strings.Contains(err.Error(), "cap") {
		t.Fatalf("err = %v, want cap enforcement before any network I/O", err)
	}
}
