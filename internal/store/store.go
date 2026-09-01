// Package store owns the on-disk llmp2p layout:
//
//	<root>/store/<owner>/<repo>/   model files as laid out in the HF repo
//	<root>/manifests/<sha256>.json content-addressed manifests
//	<root>/torrents/<infohash>.torrent
//	<root>/index.json              locally published index entries
//	<root>/llmp2p.lock             exclusive engine lock
//
// The default root is $XDG_DATA_HOME/llmp2p (i.e. ~/.local/share/llmp2p).
package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"github.com/log0u7/llmp2p/internal/ref"
)

// Store is the on-disk model store.
type Store struct {
	root string
}

// DefaultDir returns the platform default store root, honoring
// XDG_DATA_HOME.
func DefaultDir() string {
	if data := os.Getenv("XDG_DATA_HOME"); data != "" {
		return filepath.Join(data, "llmp2p")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".llmp2p"
	}
	return filepath.Join(home, ".local", "share", "llmp2p")
}

// Open prepares the directory layout and returns the store.
func Open(root string) (*Store, error) {
	s := &Store{root: root}
	for _, d := range []string{s.storeDir(), s.manifestsDir(), s.torrentsDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("store: mkdir %s: %w", d, err)
		}
	}
	return s, nil
}

func (s *Store) storeDir() string     { return filepath.Join(s.root, "store") }
func (s *Store) manifestsDir() string { return filepath.Join(s.root, "manifests") }
func (s *Store) torrentsDir() string  { return filepath.Join(s.root, "torrents") }

// Root is the store root directory.
func (s *Store) Root() string { return s.root }

// ModelDir is where a model's files live. The model id must be a validated
// "owner/repo" pair: it is used in filesystem paths.
func (s *Store) ModelDir(modelID string) (string, error) {
	if !ref.ValidModelID(modelID) {
		return "", fmt.Errorf("store: invalid model id %q", modelID)
	}
	owner, repo, _ := strings.Cut(modelID, "/")
	return filepath.Join(s.storeDir(), owner, repo), nil
}

// ManifestPath maps a manifest sha256 (hex) to its file path.
func (s *Store) ManifestPath(sha256Hex string) (string, error) {
	if !isHex(sha256Hex, 64) {
		return "", fmt.Errorf("store: invalid manifest hash %q", sha256Hex)
	}
	return filepath.Join(s.manifestsDir(), sha256Hex+".json"), nil
}

// TorrentPath maps an infohash (hex) to its torrent file path.
func (s *Store) TorrentPath(infoHashHex string) (string, error) {
	if !isHex(infoHashHex, 40) {
		return "", fmt.Errorf("store: invalid infohash %q", infoHashHex)
	}
	return filepath.Join(s.torrentsDir(), infoHashHex+".torrent"), nil
}

// LocalIndexPath is the locally published index file.
func (s *Store) LocalIndexPath() string { return filepath.Join(s.root, "index.json") }

// ModelManifestFile is the manifest pointer inside a model directory,
// pinning the revision the local files belong to.
func (s *Store) ModelManifestFile(modelID string) (string, error) {
	dir, err := s.ModelDir(modelID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ".llmp2p-manifest.json"), nil
}

// Manifests lists the sha256 of every stored manifest.
func (s *Store) Manifests() ([]string, error) {
	entries, err := os.ReadDir(s.manifestsDir())
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".json") {
			out = append(out, strings.TrimSuffix(name, ".json"))
		}
	}
	return out, nil
}

// Models lists the model ids present in the store.
func (s *Store) Models() ([]string, error) {
	owners, err := os.ReadDir(s.storeDir())
	if err != nil {
		return nil, err
	}
	var out []string
	for _, owner := range owners {
		if !owner.IsDir() {
			continue
		}
		repos, err := os.ReadDir(filepath.Join(s.storeDir(), owner.Name()))
		if err != nil {
			return nil, err
		}
		for _, repo := range repos {
			if repo.IsDir() {
				out = append(out, owner.Name()+"/"+repo.Name())
			}
		}
	}
	return out, nil
}

func isHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Lock acquires the exclusive engine lock with a timeout. The returned
// release function must be called when done.
func (s *Store) Lock(timeout time.Duration) (func(), error) {
	fl := flock.New(filepath.Join(s.root, "llmp2p.lock"))
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	locked, err := fl.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("store: lock %s: %w", s.root, err)
	}
	if !locked {
		return nil, fmt.Errorf("store: lock %s: timed out", s.root)
	}
	release := func() { fl.Unlock() }
	return release, nil
}
