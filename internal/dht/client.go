package dht

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"

	anadht "github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/bep44"
)

func decodeHexKey(h string) ([]byte, error) {
	b, err := hex.DecodeString(h)
	if err != nil {
		return nil, fmt.Errorf("bad hex key: %w", err)
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("key is %d bytes, want %d", len(b), ed25519.PublicKeySize)
	}
	return b, nil
}

// Client resolves mutable records by model id for a set of trusted
// publisher keys. The target is derived from (public key, salt), so only
// records signed by a known key can be found: the allowlist is the trust
// root, exactly as for manifest signature sidecars.
type Client struct {
	// PubKeys are the trusted ed25519 publisher keys.
	PubKeys []ed25519.PublicKey
}

// NewClient takes hex-encoded ed25519 public keys (the same allowlist
// format as pull.Options.AllowedSigners).
func NewClient(pubKeysHex []string) (*Client, error) {
	c := &Client{}
	for i, h := range pubKeysHex {
		b, err := decodeHexKey(h)
		if err != nil {
			return nil, fmt.Errorf("dht: allowlist entry %d: %w", i, err)
		}
		c.PubKeys = append(c.PubKeys, ed25519.PublicKey(b))
	}
	return c, nil
}

// Get queries node for the record of modelID and returns the first valid
// one signed by a trusted key. Nodes that fail or return nothing are
// skipped; the raw reply value is parsed by us, never trusted to the
// library decoder.
func (c *Client) Get(ctx context.Context, srv *anadht.Server, node anadht.Addr, modelID string) (*Record, error) {
	if len(c.PubKeys) == 0 {
		return nil, fmt.Errorf("dht: no trusted publisher keys")
	}
	salt := SaltFor(modelID)
	var lastErr = fmt.Errorf("dht: record for %s not found", modelID)
	for _, pub := range c.PubKeys {
		var target = bep44.MakeMutableTarget([32]byte(pub), salt)
		qr := srv.Get(ctx, node, target, nil, anadht.QueryRateLimiting{})
		if err := qr.ToError(); err != nil {
			lastErr = err
			continue
		}
		r := qr.Reply.R
		if r == nil || len(r.V) == 0 {
			lastErr = fmt.Errorf("dht: empty reply for %s", modelID)
			continue
		}
		var keyArr [32]byte
		copy(keyArr[:], r.K[:])
		if keyArr != [32]byte(pub) {
			lastErr = fmt.Errorf("dht: reply signed by an untrusted key")
			continue
		}
		if r.Seq == nil {
			lastErr = fmt.Errorf("dht: reply without sequence number")
			continue
		}
		// Verify the signature over the raw stored bytes before
		// interpreting them (BEP 44: sig covers salt+seq+v).
		if !bep44.Verify(ed25519.PublicKey(keyArr[:]), salt, *r.Seq, r.V, r.Sig[:]) {
			lastErr = fmt.Errorf("dht: record signature invalid")
			continue
		}
		rec, err := DecodeRecord(r.V)
		if err != nil {
			lastErr = err
			continue
		}
		return rec, nil
	}
	return nil, lastErr
}
