//go:build linux

package store

import (
	"os"
	"path/filepath"
)

// DefaultDir returns the platform default store root, honoring
// XDG_DATA_HOME (i.e. ~/.local/share/llmp2p).
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
