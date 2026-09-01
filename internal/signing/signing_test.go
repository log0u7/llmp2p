package signing

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "publisher.key")

	priv, created, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("first load must create")
	}

	priv2, created2, err := LoadOrCreate(path)
	if err != nil || created2 {
		t.Fatalf("second load: created=%v err=%v", created2, err)
	}
	if !bytes.Equal(priv, priv2) {
		t.Fatal("key changed between loads")
	}
}

func TestSignVerify(t *testing.T) {
	priv, _, err := LoadOrCreate(filepath.Join(t.TempDir(), "publisher.key"))
	if err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"schema":"llmp2p/v1"}`)
	sig := Sign(priv, data)

	if err := Verify(PublicKeyHex(priv), sig, data); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	tampered := append([]byte(nil), data...)
	tampered[0] ^= 0xff
	if err := Verify(PublicKeyHex(priv), sig, tampered); err == nil {
		t.Fatal("tampered data accepted")
	}
	if err := Verify(PublicKeyHex(priv), sig, []byte("other")); err == nil {
		t.Fatal("wrong data accepted")
	}
	if err := Verify("zz", sig, data); err == nil {
		t.Fatal("bad public key accepted")
	}
	if err := Verify(PublicKeyHex(priv), "nothex", data); err == nil {
		t.Fatal("bad signature accepted")
	}
}
