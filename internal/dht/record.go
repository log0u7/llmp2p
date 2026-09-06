// Package dht implements BEP 44 mutable records for manifest discovery:
// every publisher signs a small pointer record (infohash, manifest sha256,
// revision, size) and clients resolve it by model id without the HTTPS
// bootstrap index. Records never carry model bytes.
package dht

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"

	bencode "github.com/anacrolix/torrent/bencode"
)

// MaxEmbeddedManifest caps the canonical manifest JSON embedded in a
// record: the total signed value must stay under the BEP 44 limit (1000
// bytes) and the pointer part plus salt overhead consume the rest. A
// manifest larger than the cap is pointer-only: its bytes keep coming
// from HTTPS origins.
const MaxEmbeddedManifest = 700

// Record is the mutable DHT payload: a signed pointer to one model's
// distribution artifacts. Keep it small: BEP 44 limits the bencoded value
// to 1000 bytes and the salt to 64 bytes; the pointer-only form marshals
// to ~110 bytes.
type Record struct {
	// InfoHash is the v1 torrent infohash (20 raw bytes).
	InfoHash []byte `bencode:"i"`
	// ManifestSHA256 is the content-addressed manifest digest (32 raw
	// bytes); the manifest itself is fetched over HTTPS origins and must
	// match this digest.
	ManifestSHA256 []byte `bencode:"m"`
	// Revision is the pinned commit sha this record describes.
	Revision string `bencode:"r"`
	// Size is the total artifact size in bytes.
	Size int64 `bencode:"s"`
	// Manifest optionally carries the canonical manifest JSON itself
	// (digest-checked on decode), so a client can proceed without HTTPS
	// origins. Empty: pointer-only record.
	Manifest []byte `bencode:"j,omitempty"`
}

// SaltFor derives the BEP 44 salt for a model id: 32 bytes of sha256,
// deterministic on both publisher and client sides. One mutable record
// (and one sequence counter) exists per (publisher key, model id) pair.
func SaltFor(modelID string) []byte {
	sum := sha256.Sum256([]byte(modelID))
	return sum[:]
}

// Encode returns the canonical bencoded value that gets signed and stored.
func (r Record) Encode() ([]byte, error) {
	b, err := bencode.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("dht: encode record: %w", err)
	}
	return b, nil
}

// DecodeRecord strictly parses a raw bencoded record value. Anything that
// is not a dict with exactly the expected shape (a string, a list, a dict
// with wrong field types) is rejected.
func DecodeRecord(v []byte) (*Record, error) {
	var raw struct {
		InfoHash       []byte `bencode:"i"`
		ManifestSHA256 []byte `bencode:"m"`
		Revision       string `bencode:"r"`
		Size           int64  `bencode:"s"`
		Manifest       []byte `bencode:"j"`
	}
	if err := bencode.Unmarshal(v, &raw); err != nil {
		return nil, fmt.Errorf("dht: record decode: %w", err)
	}
	if len(raw.InfoHash) != 20 {
		return nil, fmt.Errorf("dht: record infohash is %d bytes, want 20", len(raw.InfoHash))
	}
	if len(raw.ManifestSHA256) != 32 {
		return nil, fmt.Errorf("dht: record manifest sha256 is %d bytes, want 32", len(raw.ManifestSHA256))
	}
	if raw.Revision == "" {
		return nil, errors.New("dht: record without revision")
	}
	if len(raw.Manifest) > MaxEmbeddedManifest {
		return nil, fmt.Errorf("dht: embedded manifest is %d bytes, cap is %d", len(raw.Manifest), MaxEmbeddedManifest)
	}
	rec := &Record{
		InfoHash:       raw.InfoHash,
		ManifestSHA256: raw.ManifestSHA256,
		Revision:       raw.Revision,
		Size:           raw.Size,
		Manifest:       raw.Manifest,
	}
	if len(rec.Manifest) > 0 {
		sum := sha256.Sum256(rec.Manifest)
		if !bytes.Equal(sum[:], rec.ManifestSHA256) {
			return nil, errors.New("dht: embedded manifest does not match the pinned digest")
		}
	}
	return rec, nil
}
