package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/log0u7/llmp2p/internal/index"
	"github.com/log0u7/llmp2p/internal/manifest"
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

// fakeStoredModel fabricates a fully stored model: file on disk, manifest
// pointer, content-addressed manifest copy, torrent file, and index entry.
func fakeStoredModel(t *testing.T, s *Store, modelID, infoHash string) {
	t.Helper()
	dir, err := s.ModelDir(modelID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model.gguf"), []byte("model-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{
		Schema: "llmp2p/v1", Model: modelID, Revision: "cafe123",
		PieceLength: 4 << 20, InfoHash: infoHash,
		Files: []manifest.File{{Path: "model.gguf", Size: 11,
			SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
	}
	mfile, err := s.ModelManifestFile(modelID)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Save(mfile); err != nil {
		t.Fatal(err)
	}
	msha, err := m.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	mpath, err := s.ManifestPath(msha)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Save(mpath); err != nil {
		t.Fatal(err)
	}
	tpath, err := s.TorrentPath(infoHash)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tpath, []byte("d8:announce0:e"), 0o644); err != nil {
		t.Fatal(err)
	}
	ix := &index.Index{Entries: map[string]index.Entry{}}
	if err := ix.Add(index.Entry{Model: modelID, InfoHash: infoHash,
		ManifestSHA256: msha, Revision: "cafe123", Size: 11}); err != nil {
		t.Fatal(err)
	}
	if err := ix.Save(s.LocalIndexPath()); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveDeletesEverything(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const ih = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	fakeStoredModel(t, s, "org/model", ih)

	dir, err := s.ModelDir("org/model")
	if err != nil {
		t.Fatal(err)
	}
	tpath, _ := s.TorrentPath(ih)
	msha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	mpath, _ := s.ManifestPath(msha)

	sum, err := s.Remove("org/model")
	if err != nil {
		t.Fatal(err)
	}
	if !sum.DirRemoved || !sum.TorrentRemoved || !sum.ManifestRemoved || !sum.IndexEntryRemoved {
		t.Fatalf("summary = %+v", sum)
	}
	if sum.Files != 1 || sum.Size != 11 {
		t.Fatalf("summary counts = %+v", sum)
	}
	for _, p := range []string{dir, tpath, mpath} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists", p)
		}
	}
	ib, err := os.ReadFile(s.LocalIndexPath())
	if err != nil {
		t.Fatal(err)
	}
	ix, _, err := index.Parse(ib)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ix.Get("org/model"); ok {
		t.Error("index entry still present")
	}
	if _, err := s.Remove("org/model"); !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("second remove err = %v, want ErrModelNotFound", err)
	}
}
