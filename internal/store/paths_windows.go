//go:build windows

package store

import (
	"os"
	"path/filepath"
)

// DefaultDir returns the Windows default store root:
// %LOCALAPPDATA%\llmp2p (fallback %USERPROFILE%\AppData\Local\llmp2p).
func DefaultDir() string {
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		return filepath.Join(local, "llmp2p")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".llmp2p"
	}
	return filepath.Join(home, "AppData", "Local", "llmp2p")
}
