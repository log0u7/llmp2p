package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/log0u7/llmp2p/internal/index"
	"github.com/log0u7/llmp2p/internal/manifest"
)

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

func TestRemoveInvalidModelID(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Remove("not-an-id"); err == nil {
		t.Fatal("Remove accepted an invalid model id")
	}
}

func TestRemoveWithoutManifestKeepsModelFilesOnly(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir, err := s.ModelDir("org/model")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	sum, err := s.Remove("org/model")
	if err != nil {
		t.Fatal(err)
	}
	if !sum.DirRemoved {
		t.Fatalf("dir must be removed: %+v", sum)
	}
	if sum.TorrentRemoved || sum.ManifestRemoved || sum.IndexEntryRemoved || sum.Files != 0 {
		t.Fatalf("nothing else to remove: %+v", sum)
	}
}

func TestRemoveWithBrokenManifestStillRemovesDir(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir, err := s.ModelDir("org/model")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mfile, err := s.ModelManifestFile("org/model")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mfile, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}

	sum, err := s.Remove("org/model")
	if err != nil {
		t.Fatal(err)
	}
	if !sum.DirRemoved {
		t.Fatalf("dir must be removed: %+v", sum)
	}
	if sum.TorrentRemoved || sum.ManifestRemoved {
		t.Fatalf("unparseable manifest must not claim removals: %+v", sum)
	}
}

func TestRemoveWithoutIndexFile(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const ih = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	fakeStoredModel(t, s, "org/model", ih)
	if err := os.Remove(s.LocalIndexPath()); err != nil {
		t.Fatal(err)
	}

	sum, err := s.Remove("org/model")
	if err != nil {
		t.Fatal(err)
	}
	if sum.IndexEntryRemoved {
		t.Fatal("no index file: entry removal must not be claimed")
	}
}

func TestSignaturePath(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sp, err := s.SignaturePath(sha)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(sp) != sha+".sig" {
		t.Fatalf("SignaturePath = %q", sp)
	}
	if _, err := s.SignaturePath("nothex"); err == nil {
		t.Fatal("SignaturePath accepted a non-hex hash")
	}
}

func TestModelManifestFileValidation(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ModelManifestFile("nope"); err == nil {
		t.Fatal("ModelManifestFile accepted an invalid model id")
	}
}

func TestManifestsListing(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.Manifests(); err != nil || got != nil {
		t.Fatalf("empty store Manifests = %v, %v; want nil, nil", got, err)
	}
	const sha = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := os.WriteFile(filepath.Join(s.manifestsDir(), sha+".json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.manifestsDir(), sha+".sig"), []byte("sig"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.manifestsDir(), "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := s.Manifests()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != sha {
		t.Fatalf("Manifests = %v, want [%s]", got, sha)
	}
}

func TestManifestsReadError(t *testing.T) {
	// No Open(): the manifests dir does not exist.
	s := &Store{root: filepath.Join(t.TempDir(), "gone")}
	if _, err := s.Manifests(); err == nil {
		t.Fatal("Manifests must fail on a missing directory")
	}
}

func TestModelsListing(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.Models(); err != nil || got != nil {
		t.Fatalf("empty store Models = %v, %v; want nil, nil", got, err)
	}
	ownerDir := filepath.Join(s.storeDir(), "org")
	for _, repo := range []string{"model-a", "model-b"} {
		if err := os.MkdirAll(filepath.Join(ownerDir, repo), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Stray files at both levels must be skipped.
	if err := os.WriteFile(filepath.Join(s.storeDir(), "stray-file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ownerDir, "not-a-repo"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := s.Models()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"org/model-a", "org/model-b"}
	if len(got) != len(want) {
		t.Fatalf("Models = %v, want %v", got, want)
	}
	for i, id := range want {
		if got[i] != id {
			t.Errorf("Models[%d] = %q, want %q", i, got[i], id)
		}
	}
}

func TestOpenFailsUnderFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(root, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root); err == nil {
		t.Fatal("Open must fail when the root is a regular file")
	}
}

func TestLockFailsOnUnwritableRoot(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })
	if _, err := s.Lock(50 * time.Millisecond); err == nil {
		t.Fatal("Lock must fail on an unwritable root")
	}
}

// TestIsHexLowercaseOnly pins the store path contract: model identifiers
// (infohash, manifest sha) are lowercase hex only, on disk and in URLs.
func TestIsHexLowercaseOnly(t *testing.T) {
	if !isHex(strings.Repeat("a", 40), 40) {
		t.Fatal("lowercase hex must be accepted")
	}
	if isHex(strings.ToUpper(strings.Repeat("a", 40)), 40) {
		t.Fatal("uppercase hex must be rejected: store paths are lowercase-only")
	}
	if isHex(strings.Repeat("g", 40), 40) {
		t.Fatal("non-hex must be rejected")
	}
	if isHex(strings.Repeat("a", 39), 40) {
		t.Fatal("wrong length must be rejected")
	}
}
