package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultDirXDG(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-data")
	if got, want := DefaultDir(), filepath.Join("/tmp/xdg-data", "llmp2p"); got != want {
		t.Fatalf("DefaultDir() = %q, want %q", got, want)
	}
}

func TestOpenLayout(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"store", "manifests", "torrents"} {
		fi, err := os.Stat(filepath.Join(root, sub))
		if err != nil || !fi.IsDir() {
			t.Fatalf("missing dir %s: %v", sub, err)
		}
	}
	if s.Root() != root {
		t.Fatalf("Root() = %q", s.Root())
	}
}

func TestModelDirValidation(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"Qwen/Qwen3", "meta-llama/Llama-3.2-1B"} {
		if _, err := s.ModelDir(id); err != nil {
			t.Errorf("ModelDir(%q): %v", id, err)
		}
	}
	for _, id := range []string{"", "Qwen", "Qwen/Qwen3/extra", "../etc", "Qwen/../..", "/abs", "a/b/../c"} {
		if _, err := s.ModelDir(id); err == nil {
			t.Errorf("ModelDir(%q) accepted, want rejection", id)
		}
	}
}

func TestContentAddressedPaths(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	ih := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	mp, err := s.ManifestPath(sha)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(mp) != sha+".json" {
		t.Fatalf("ManifestPath = %q", mp)
	}
	tp, err := s.TorrentPath(ih)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(tp) != ih+".torrent" {
		t.Fatalf("TorrentPath = %q", tp)
	}
	for _, bad := range []string{"", "xyz", "AAAA", sha + "0", ih + "0", strings_upper(ih)} {
		if _, err := s.TorrentPath(bad); err == nil {
			t.Errorf("TorrentPath(%q) accepted", bad)
		}
		if _, err := s.ManifestPath(bad); err == nil {
			t.Errorf("ManifestPath(%q) accepted", bad)
		}
	}
}

func strings_upper(s string) string {
	out := []rune(s)
	for i, r := range out {
		if r >= 'a' && r <= 'f' {
			out[i] = r - 32
		}
	}
	return string(out)
}

func TestLockExclusivity(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	release, err := s.Lock(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lock(100 * time.Millisecond); err == nil {
		t.Fatal("second lock must fail while held")
	}
	release()
	release2, err := s.Lock(time.Second)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	release2()
}
