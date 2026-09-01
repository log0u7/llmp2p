// Package manifest defines the llmp2p/v1 content-addressed manifest: the
// bridge between a Hugging Face repository and a BitTorrent swarm.
//
// The manifest pins the revision, every file sha256, and the v1 torrent
// infohash built from those files. It is canonical JSON: struct field order
// and no maps, so identical inputs always serialize identically.
package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Schema is the manifest format version.
const Schema = "llmp2p/v1"

// PieceLength is fixed so that identical file sets always produce identical
// metainfos (and therefore identical infohashes, i.e. the same swarm).
const PieceLength = 4 << 20 // 4 MiB

// File describes one artifact pinned by the manifest.
type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"` // lowercase hex
}

// Manifest is the content-addressed description of a model repository.
type Manifest struct {
	Schema      string    `json:"schema"`
	Model       string    `json:"model"`
	Revision    string    `json:"revision"`
	CreatedAt   time.Time `json:"createdAt"`
	PieceLength int64     `json:"pieceLength"`
	// InfoHash is the v1 (BEP 3) infohash, 40 lowercase hex chars.
	InfoHash string `json:"infoHash"`
	Files    []File `json:"files"`
}

var (
	sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)
	infoRe   = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// Validate enforces internal consistency before the manifest is trusted.
func (m *Manifest) Validate() error {
	if err := m.validateContent(); err != nil {
		return err
	}
	if !infoRe.MatchString(m.InfoHash) {
		return fmt.Errorf("manifest infohash %q is not 40 lowercase hex chars", m.InfoHash)
	}
	return nil
}

// validateContent checks everything except the infohash, which is only
// known after metainfo generation.
func (m *Manifest) validateContent() error {
	if m.Schema != Schema {
		return fmt.Errorf("manifest schema %q, want %q", m.Schema, Schema)
	}
	if m.Model == "" || m.Revision == "" {
		return errors.New("manifest model and revision are required")
	}
	if m.PieceLength <= 0 {
		return errors.New("manifest piece length must be positive")
	}
	if len(m.Files) == 0 {
		return errors.New("manifest has no files")
	}
	for i, f := range m.Files {
		if f.Path == "" {
			return fmt.Errorf("file %d: empty path", i)
		}
		if f.Size < 0 {
			return fmt.Errorf("file %q: negative size", f.Path)
		}
		if !sha256Re.MatchString(f.SHA256) {
			return fmt.Errorf("file %q: sha256 %q is not 64 lowercase hex chars", f.Path, f.SHA256)
		}
		if i > 0 && f.Path <= m.Files[i-1].Path {
			return fmt.Errorf("files not strictly sorted by path at %q", f.Path)
		}
	}
	return nil
}

// FileByPath returns the pinned entry for path.
func (m *Manifest) FileByPath(path string) (File, bool) {
	for _, f := range m.Files {
		if f.Path == path {
			return f, true
		}
	}
	return File{}, false
}

// Bytes returns the canonical JSON encoding.
func (m *Manifest) Bytes() ([]byte, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// SHA256 is the content hash of the canonical manifest encoding.
func (m *Manifest) SHA256() (string, error) {
	b, err := m.Bytes()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// Save writes the canonical manifest to path.
func (m *Manifest) Save(path string) error {
	b, err := m.Bytes()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// Load reads and validates a manifest from path.
func Load(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Parse decodes and validates manifest bytes.
func Parse(b []byte) (*Manifest, error) {
	m := &Manifest{}
	if err := json.Unmarshal(b, m); err != nil {
		return nil, fmt.Errorf("manifest: decode: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return m, nil
}

// VerifyDir checks every file of m against root: existence, size and
// streaming sha256.
func (m *Manifest) VerifyDir(root string) error {
	for _, f := range m.Files {
		p := filepath.Join(root, filepath.FromSlash(f.Path))
		info, err := os.Stat(p)
		if err != nil {
			return fmt.Errorf("verify %s: %w", f.Path, err)
		}
		if info.Size() != f.Size {
			return fmt.Errorf("verify %s: size %d, want %d", f.Path, info.Size(), f.Size)
		}
		got, err := hashFile(p)
		if err != nil {
			return fmt.Errorf("verify %s: %w", f.Path, err)
		}
		if got != f.SHA256 {
			return fmt.Errorf("verify %s: sha256 %s, want %s", f.Path, got, f.SHA256)
		}
	}
	return nil
}

// hashFile streams a file through sha256.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// HashContent is a small public helper hashing one file under root.
func HashContent(root, relPath string) (string, int64, error) {
	p := filepath.Join(root, filepath.FromSlash(relPath))
	info, err := os.Stat(p)
	if err != nil {
		return "", 0, err
	}
	sum, err := hashFile(p)
	if err != nil {
		return "", 0, err
	}
	return sum, info.Size(), nil
}
