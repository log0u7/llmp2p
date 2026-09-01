//go:build darwin

package store

import (
	"os"
	"path/filepath"
)

// DefaultDir returns the macOS default store root:
// ~/Library/Application Support/llmp2p.
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".llmp2p"
	}
	return filepath.Join(home, "Library", "Application Support", "llmp2p")
}
