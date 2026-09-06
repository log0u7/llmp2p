// Package dht implements BEP 44 mutable records for manifest discovery:
// every publisher signs a small pointer record (infohash, manifest sha256,
// revision, size) and clients resolve it by model id without the HTTPS
// bootstrap index. Records never carry model bytes.
package dht

import (
	"crypto/sha256"
	"errors"
	"fmt"

	bencode "github.com/anacrolix/torrent/bencode"
)

// Record is the mutable DHT payload: a signed pointer to one model's
// distribution artifacts. Keep it small: BEP 44 limits the bencoded value
// to 1000 bytes and the salt to 64 bytes; this record marshals to ~110.
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
	return &Record{
		InfoHash:       raw.InfoHash,
		ManifestSHA256: raw.ManifestSHA256,
		Revision:       raw.Revision,
		Size:           raw.Size,
	}, nil
}
