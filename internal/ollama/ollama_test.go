package ollama

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultName(t *testing.T) {
	cases := map[string]string{
		"Qwen/Qwen3-Coder-30B-A3B-GGUF": "qwen3-coder-30b-a3b-gguf",
		"org/repo.name_v2":              "repo.name_v2",
		"Org/Weird Name!!":              "weird-name",
	}
	for in, want := range cases {
		if got := DefaultName(in); got != want {
			t.Errorf("DefaultName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFindGGUF(t *testing.T) {
	dir := t.TempDir()
	if _, err := FindGGUF(dir); err == nil {
		t.Fatal("empty dir must fail")
	}
	single := filepath.Join(dir, "model.gguf")
	_ = os.WriteFile(single, []byte("gguf"), 0o644)
	got, err := FindGGUF(dir)
	if err != nil || got != single {
		t.Fatalf("FindGGUF = %q, err = %v", got, err)
	}
}

func TestFindGGUFRejectsShards(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "model-00001-of-00002.gguf"), []byte("a"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "model-00002-of-00002.gguf"), []byte("b"), 0o644)
	if _, err := FindGGUF(dir); err == nil || !strings.Contains(err.Error(), "split GGUF") {
		t.Fatalf("err = %v, want split GGUF rejection", err)
	}
}

func TestFindGGUFRejectsMultiple(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.gguf"), []byte("a"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "b.gguf"), []byte("b"), 0o644)
	if _, err := FindGGUF(dir); err == nil {
		t.Fatal("multiple GGUFs must be rejected")
	}
}

// fakeOllama writes a shell script mimicking the ollama CLI that records
// its invocation into args.txt.
func fakeOllama(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "bin", "ollama")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\npwd > \"$RECORD/args.txt\"\nprintf '%s\\n' \"$@\" >> \"$RECORD/args.txt\"\nenv | grep ^OLLAMA_HOST >> \"$RECORD/args.txt\" || true\ncat \"$4\" > \"$RECORD/modelfile.txt\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestCreateInvokesCLI(t *testing.T) {
	root := t.TempDir()
	t.Setenv("RECORD", root)
	gguf := filepath.Join(root, "model.gguf")
	_ = os.WriteFile(gguf, []byte("gguf-data"), 0o644)

	bin := fakeOllama(t, root)
	var out bytes.Buffer
	if err := Create(context.Background(), bin, gguf, "mymodel", "tcp://1.2.3.4:11434", &out); err != nil {
		t.Fatal(err)
	}
	rec, err := os.ReadFile(filepath.Join(root, "args.txt"))
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(rec)), "\n")
	// args.txt: cwd, create, name, -f, modelfile, OLLAMA_HOST=...
	if len(args) != 6 {
		t.Fatalf("recorded args = %v", args)
	}
	if args[1] != "create" || args[2] != "mymodel" || args[3] != "-f" {
		t.Fatalf("recorded args = %v", args)
	}
	mb, err := os.ReadFile(filepath.Join(root, "modelfile.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "FROM " + gguf + "\n"; string(mb) != want {
		t.Fatalf("Modelfile = %q, want %q", mb, want)
	}
	if args[5] != "OLLAMA_HOST=tcp://1.2.3.4:11434" {
		t.Fatalf("host not passed: %q", args[5])
	}
}

func TestCreateMissingBinary(t *testing.T) {
	root := t.TempDir()
	gguf := filepath.Join(root, "model.gguf")
	_ = os.WriteFile(gguf, []byte("d"), 0o644)
	if err := Create(context.Background(), "/nonexistent/ollama", gguf, "m", "", &bytes.Buffer{}); err == nil {
		t.Fatal("expected error for missing binary")
	}
}
