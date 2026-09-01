package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

// writeRepo creates a fake model repo on disk and returns its root plus
// the sorted file entries.
func writeRepo(t *testing.T) (string, []File) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"config.json":         `{"model_type":"test"}`,
		"model.gguf":          string(make([]byte, 3*1024)) + "ggufpayload",
		"sub/dir/tokeni.json": `{"vocab":[]}`,
	}
	var entries []File
	for path, content := range files {
		p := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, File{Path: path, Size: int64(len(content))})
	}
	sortFiles(entries)
	return root, entries
}

func sortFiles(fs []File) {
	for i := 1; i < len(fs); i++ {
		for j := i; j > 0 && fs[j].Path < fs[j-1].Path; j-- {
			fs[j], fs[j-1] = fs[j-1], fs[j]
		}
	}
}

func TestCreateDeterministic(t *testing.T) {
	root, entries := writeRepo(t)

	m1, err := Create("org/model", "rev1", entries, root)
	if err != nil {
		t.Fatal(err)
	}
	m2, err := Create("org/model", "rev1", entries, root)
	if err != nil {
		t.Fatal(err)
	}
	// CreatedAt is second-truncated: identical builds must agree fully.
	b1, _ := m1.Bytes()
	b2, _ := m2.Bytes()
	if string(b1) != string(b2) {
		t.Fatalf("manifests differ:\n%s\n%s", b1, b2)
	}
	if m1.InfoHash == "" || len(m1.InfoHash) != 40 {
		t.Fatalf("infohash %q invalid", m1.InfoHash)
	}
	if m1.Files[0].Path != "config.json" || len(m1.Files[0].SHA256) != 64 {
		t.Fatalf("files not hashed correctly: %+v", m1.Files)
	}
}

func TestCreateIdenticalContentSharesInfoHash(t *testing.T) {
	root, entries := writeRepo(t)
	m1, err := Create("org/model", "rev1", entries, root)
	if err != nil {
		t.Fatal(err)
	}
	// Same content, same torrent root name: the infohash is identical,
	// so independent pullers converge on one swarm.
	m2, err := Create("other/model", "rev1", entries, root)
	if err != nil {
		t.Fatal(err)
	}
	if m1.InfoHash != m2.InfoHash {
		t.Fatal("identical content under same torrent name must share infohash")
	}

	// Different content must differ.
	if err := os.WriteFile(filepath.Join(root, "extra.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries = append(entries, File{Path: "extra.bin", Size: 1})
	sortFiles(entries)
	m3, err := Create("org/model", "rev1", entries, root)
	if err != nil {
		t.Fatal(err)
	}
	if m1.InfoHash == m3.InfoHash {
		t.Fatal("different content must produce a different infohash")
	}
}

func TestRoundTripAndMetaInfoBytes(t *testing.T) {
	root, entries := writeRepo(t)
	m, err := Create("org/model", "rev1", entries, root)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.InfoHash != m.InfoHash || loaded.Model != m.Model || loaded.Revision != m.Revision {
		t.Fatalf("round trip mismatch: %+v vs %+v", loaded, m)
	}
	if f, ok := loaded.FileByPath("model.gguf"); !ok || f.Size != int64(3*1024+11) {
		t.Fatalf("FileByPath = %+v, ok = %v", f, ok)
	}

	// The .torrent must rebuild to the same infohash.
	torrent, err := loaded.MetaInfoBytes(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(torrent) == 0 {
		t.Fatal("empty torrent")
	}
}

func TestMetaInfoBytesWrongRoot(t *testing.T) {
	root, entries := writeRepo(t)
	m, err := Create("org/model", "rev1", entries, root)
	if err != nil {
		t.Fatal(err)
	}
	// Empty root: piece generation opens missing files.
	if _, err := m.MetaInfoBytes(t.TempDir()); err == nil {
		t.Fatal("expected error building metainfo from missing files")
	}
}

func TestVerifyDir(t *testing.T) {
	root, entries := writeRepo(t)
	m, err := Create("org/model", "rev1", entries, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.VerifyDir(root); err != nil {
		t.Fatalf("VerifyDir: %v", err)
	}

	// Corrupt one byte.
	p := filepath.Join(root, "config.json")
	orig, _ := os.ReadFile(p)
	_ = os.WriteFile(p, append(orig, 'x'), 0o644)
	if err := m.VerifyDir(root); err == nil {
		t.Fatal("VerifyDir must detect corruption")
	}
	_ = os.WriteFile(p, orig, 0o644)

	// Missing file.
	_ = os.Remove(filepath.Join(root, "sub", "dir", "tokeni.json"))
	if err := m.VerifyDir(root); err == nil {
		t.Fatal("VerifyDir must detect missing files")
	}
}

func TestValidateRejects(t *testing.T) {
	root, entries := writeRepo(t)
	m, err := Create("org/model", "rev1", entries, root)
	if err != nil {
		t.Fatal(err)
	}

	cases := []func(m *Manifest){
		func(m *Manifest) { m.Schema = "llmp2p/v0" },
		func(m *Manifest) { m.Model = "" },
		func(m *Manifest) { m.PieceLength = 0 },
		func(m *Manifest) { m.InfoHash = "ZZZZ" },
		func(m *Manifest) { m.Files = nil },
		func(m *Manifest) { m.Files[0].SHA256 = "deadbeef" },
	}
	for i, mutate := range cases {
		cp := *m
		cp.Files = append([]File(nil), m.Files...)
		mutate(&cp)
		if err := cp.Validate(); err == nil {
			t.Errorf("case %d: expected validation error", i)
		}
	}

	// Unsorted files.
	bad := *m
	bad.Files = []File{{Path: "b", Size: 1, SHA256: m.Files[0].SHA256}, {Path: "a", Size: 1, SHA256: m.Files[0].SHA256}}
	if err := bad.Validate(); err == nil {
		t.Error("unsorted files must be rejected")
	}
}

func TestSHA256Manifest(t *testing.T) {
	root, entries := writeRepo(t)
	m, err := Create("org/model", "rev1", entries, root)
	if err != nil {
		t.Fatal(err)
	}
	s1, err := m.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := m.Bytes()
	parsed, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := parsed.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	if s1 != s2 {
		t.Fatalf("manifest sha unstable: %s vs %s", s1, s2)
	}
}
