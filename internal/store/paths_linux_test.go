//go:build linux

package store

import (
	"path/filepath"
	"testing"
)

func TestDefaultDirXDG(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-data")
	if got, want := DefaultDir(), filepath.Join("/tmp/xdg-data", "llmp2p"); got != want {
		t.Fatalf("DefaultDir() = %q, want %q", got, want)
	}
}
