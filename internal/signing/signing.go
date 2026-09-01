// Package signing manages the publisher key and ed25519 signatures over
// canonical manifest bytes. The private key never leaves the machine; the
// public key is what gets allowlisted or published in index entries.
package signing

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

// DefaultKeyFile is the store-relative publisher key path.
const DefaultKeyFile = "publisher.key"

// LoadOrCreate loads the ed25519 publisher key from path, generating and
// saving one (0600) when the file does not exist. The second return value
// reports whether a new key was created.
func LoadOrCreate(path string) (ed25519.PrivateKey, bool, error) {
	if b, err := os.ReadFile(path); err == nil {
		seed, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
		if derr != nil || len(seed) != ed25519.SeedSize {
			return nil, false, fmt.Errorf("signing: bad key file %s", path)
		}
		return ed25519.NewKeyFromSeed(seed), false, nil
	} else if !os.IsNotExist(err) {
		return nil, false, err
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, false, err
	}
	encoded := base64.StdEncoding.EncodeToString(priv.Seed()) + "\n"
	if err := os.WriteFile(path, []byte(encoded), 0o600); err != nil {
		return nil, false, err
	}
	return priv, true, nil
}

// PublicKeyHex returns the hex-encoded public key of the pair.
func PublicKeyHex(priv ed25519.PrivateKey) string {
	return hex.EncodeToString(priv.Public().(ed25519.PublicKey))
}

// Sign returns the ed25519 signature over data.
func Sign(priv ed25519.PrivateKey, data []byte) string {
	sig := ed25519.Sign(priv, data)
	return hex.EncodeToString(sig)
}

// Verify checks a hex signature over data for a hex public key.
func Verify(pubHex, sigHex string, data []byte) error {
	pub, err := hex.DecodeString(pubHex)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("signing: bad public key")
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("signing: bad signature")
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), data, sig) {
		return errors.New("signing: signature mismatch")
	}
	return nil
}
