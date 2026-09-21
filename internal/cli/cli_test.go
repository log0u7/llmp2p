package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/log0u7/llmp2p/internal/engine"
	"github.com/log0u7/llmp2p/internal/manifest"
	"github.com/log0u7/llmp2p/internal/pull"
	"github.com/log0u7/llmp2p/internal/store"
)

func progressOf(completed, total int64, peers int) engine.Progress {
	return engine.Progress{Completed: completed, Total: total, Peers: peers}
}

// runRoot executes the CLI with args, feeding stdin, and returns
// (stdout, stderr, error).
func runRoot(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()

	root := NewRootCmd()
	root.SetArgs(args)
	root.SetIn(strings.NewReader(stdin))
	root.SetOut(io.Discard)
	runErr := root.Execute()

	_ = outW.Close()
	_ = errW.Close()
	out, _ := io.ReadAll(outR)
	errOut, _ := io.ReadAll(errR)
	return string(out), string(errOut), runErr
}

// storeModel installs a complete model (files, manifest pointer, torrent)
// into the store and returns its manifest.
func storeModel(t *testing.T, st *store.Store, modelID string, files map[string]string) *manifest.Manifest {
	t.Helper()
	dir, err := st.ModelDir(modelID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	entries := make([]manifest.File, 0, len(files))
	for path, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, manifest.File{Path: path, Size: int64(len(content))})
	}
	m, err := manifest.Create(modelID, "cafe123", entries, dir)
	if err != nil {
		t.Fatal(err)
	}
	ptr, err := st.ModelManifestFile(modelID)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Save(ptr); err != nil {
		t.Fatal(err)
	}
	tpath, err := st.TorrentPath(m.InfoHash)
	if err != nil {
		t.Fatal(err)
	}
	torrentBytes, err := m.MetaInfoBytes(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(tpath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tpath, torrentBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestListEmptyAndPopulated(t *testing.T) {
	dir := t.TempDir()
	_, errOut, err := runRoot(t, "", "list", "--dir", dir)
	if err != nil {
		t.Fatalf("list on empty store: %v", err)
	}
	if !strings.Contains(errOut, "0 model(s)") {
		t.Fatalf("stderr = %q", errOut)
	}

	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	storeModel(t, st, "org/model", map[string]string{"model.gguf": strings.Repeat("x", 2<<20)})

	outJSON, _, err := runRoot(t, "", "list", "--dir", dir, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var infos []modelInfo
	if err := json.Unmarshal([]byte(outJSON), &infos); err != nil {
		t.Fatalf("json output: %v (%q)", err, outJSON)
	}
	if len(infos) != 1 || infos[0].Model != "org/model" || infos[0].Revision != "cafe123" || infos[0].Files != 1 {
		t.Fatalf("infos = %+v", infos)
	}

	out, _, err := runRoot(t, "", "list", "--dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "org/model") || !strings.Contains(out, "cafe123") {
		t.Fatalf("human output = %q", out)
	}
}

func TestKeygenRoundTrip(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runRoot(t, "", "keygen", "--dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "publisher key created") {
		t.Fatalf("first keygen output = %q", out)
	}
	pub := out[strings.Index(out, "public key: ")+len("public key: "):]
	pub = strings.TrimSpace(pub)
	if len(pub) != 64 {
		t.Fatalf("public key = %q, want 64 hex chars", pub)
	}

	out, _, err = runRoot(t, "", "keygen", "--dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "already exists") || !strings.Contains(out, pub) {
		t.Fatalf("second keygen output = %q (want same key)", out)
	}
}

func TestRemoveConfirmationAndFlags(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	storeModel(t, st, "org/model", map[string]string{"model.gguf": "data"})

	// Path refs are rejected outright.
	if _, _, err := runRoot(t, "", "remove", "hf:org/model#f.gguf", "--dir", dir, "--yes"); err == nil {
		t.Fatal("remove accepted a path ref")
	}

	// Declining the prompt aborts.
	if _, _, err := runRoot(t, "n\n", "remove", "hf:org/model", "--dir", dir); err == nil {
		t.Fatal("declined removal must abort")
	}
	if _, err := os.Stat(filepath.Join(dir, "store", "org", "model")); err != nil {
		t.Fatal("model must survive an aborted removal")
	}

	// --yes removes for real, human output summarises the pieces.
	out, _, err := runRoot(t, "", "remove", "hf:org/model", "--dir", dir, "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "removed org/model") || !strings.Contains(out, "files") {
		t.Fatalf("remove output = %q", out)
	}

	// Removing again fails with not-found; JSON flag prints the summary.
	if _, _, err := runRoot(t, "", "remove", "hf:org/model", "--dir", dir, "--yes"); err == nil {
		t.Fatal("second removal must fail")
	}
	st2, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	storeModel(t, st2, "org/again", map[string]string{"a.gguf": "aa"})
	outJSON, _, err := runRoot(t, "", "remove", "hf:org/again", "--dir", dir, "--yes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var summary struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal([]byte(outJSON), &summary); err != nil || summary.Model != "org/again" {
		t.Fatalf("json summary = %q err=%v", outJSON, err)
	}
}

func TestVerifyOKCorruptAndJSON(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	storeModel(t, st, "org/model", map[string]string{"model.gguf": "gguf-data"})

	out, _, err := runRoot(t, "", "verify", "hf:org/model", "--dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ok org/model @cafe123") {
		t.Fatalf("verify output = %q", out)
	}

	// Corrupt the file: human mode fails, JSON mode reports ok=false and
	// still fails the command.
	ggufPath := filepath.Join(dir, "store", "org", "model", "model.gguf")
	if err := os.WriteFile(ggufPath, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runRoot(t, "", "verify", "hf:org/model", "--dir", dir); err == nil {
		t.Fatal("verify must fail on corrupted files")
	}
	outJSON, _, err := runRoot(t, "", "verify", "hf:org/model", "--dir", dir, "--json")
	if err == nil {
		t.Fatal("verify --json must fail on corrupted files")
	}
	if !strings.Contains(outJSON, `"ok": false`) {
		t.Fatalf("json verify output = %q", outJSON)
	}

	// Path refs rejected; unknown model fails.
	if _, _, err := runRoot(t, "", "verify", "hf:org/model#f.gguf", "--dir", dir); err == nil {
		t.Fatal("verify accepted a path ref")
	}
	if _, _, err := runRoot(t, "", "verify", "hf:org/missing", "--dir", dir); err == nil {
		t.Fatal("verify accepted an unknown model")
	}
}

func TestPullBadRef(t *testing.T) {
	if _, _, err := runRoot(t, "", "pull", "garbage", "--dir", t.TempDir()); err == nil {
		t.Fatal("pull accepted a bad ref")
	}
}

// fakeDaemon serves the subset of the daemon API the CLI delegation uses.
func fakeDaemon(t *testing.T, jobStatus, jobError string, posted *[]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/status", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /api/v1/pulls", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Ref      string `json:"ref"`
			HTTPOnly bool   `json:"httpOnly"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		*posted = append(*posted, body.Ref)
		w.WriteHeader(http.StatusAccepted)
		_, _ = fmt.Fprintf(w, `{"id":"job1","ref":%q,"status":"queued"}`, body.Ref)
	})
	mux.HandleFunc("GET /api/v1/pulls/job1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"id":"job1","ref":"hf:org/model","status":%q,"error":%q,
			"result":{"model":"org/model","revision":"cafe123","mode":"http","files":2,"size":5,"infoHash":"%s","manifestSha256":"%s"}}`,
			jobStatus, jobError, strings.Repeat("a", 40), strings.Repeat("b", 64))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestPullDelegatesToDaemon(t *testing.T) {
	var posted []string
	srv := fakeDaemon(t, "succeeded", "", &posted)
	dir := t.TempDir()

	out, _, err := runRoot(t, "", "pull", "hf:org/model", "--dir", dir,
		"--daemon", srv.URL, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if len(posted) != 1 || posted[0] != "hf:org/model" {
		t.Fatalf("posted refs = %v", posted)
	}
	var res pull.Result
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("summary = %q: %v", out, err)
	}
	if res.Model != "org/model" || res.Mode != "http" {
		t.Fatalf("result = %+v", res)
	}
}

func TestPullDaemonReportsFailure(t *testing.T) {
	var posted []string
	srv := fakeDaemon(t, "failed", "boom", &posted)
	if _, _, err := runRoot(t, "", "pull", "hf:org/model", "--dir", t.TempDir(),
		"--daemon", srv.URL); err == nil || !strings.Contains(err.Error(), "daemon pull failed: boom") {
		t.Fatalf("err = %v, want daemon failure propagation", err)
	}
}

func TestPullDaemonRejects(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/status", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /api/v1/pulls", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"nope"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	if _, _, err := runRoot(t, "", "pull", "hf:org/model", "--dir", t.TempDir(),
		"--daemon", srv.URL); err == nil || !strings.Contains(err.Error(), "daemon rejected pull") {
		t.Fatalf("err = %v, want rejection", err)
	}
}

func TestPullLocalEndToEnd(t *testing.T) {
	hubMux := http.NewServeMux()
	gguf := "GGUF-payload"
	hubMux.HandleFunc("/api/models/org/model/revision/main", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"sha":"cafe123"}`)
	})
	hubMux.HandleFunc("/api/models/org/model/tree/cafe123", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `[{"type":"file","path":"model.gguf","size":%d,"lfs":{"oid":"%s"}}]`, len(gguf), sha256HexStr(gguf))
	})
	hubMux.HandleFunc("/org/model/resolve/cafe123/model.gguf", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(gguf))
	})
	hub := httptest.NewServer(hubMux)
	t.Cleanup(hub.Close)

	boot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"entries":{}}`))
	}))
	t.Cleanup(boot.Close)

	dir := t.TempDir()
	t.Setenv("HF_ENDPOINT", hub.URL)
	out, _, err := runRoot(t, "", "pull", "hf:org/model", "--dir", dir,
		"--no-daemon", "--http-only", "--bootstrap", boot.URL, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res pull.Result
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("summary = %q: %v", out, err)
	}
	if res.Mode != pull.ModeHTTP || res.Files != 1 {
		t.Fatalf("result = %+v", res)
	}
	got, err := os.ReadFile(filepath.Join(dir, "store", "org", "model", "model.gguf"))
	if err != nil || string(got) != gguf {
		t.Fatalf("downloaded file mismatch: err=%v", err)
	}
}

func sha256HexStr(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestSeedResolveTarget(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := storeModel(t, st, "org/model", map[string]string{"model.gguf": "data"})
	flagStoreDir = dir

	// Torrent file path: returned as-is with the data dir (or cwd).
	wd, _ := os.Getwd()
	tpath, root, err := resolveSeedTarget("x.torrent", "")
	if err != nil || tpath != "x.torrent" || root != wd {
		t.Fatalf("resolveSeedTarget(torrent) = %q, %q, %v", tpath, root, err)
	}
	tpath, root, err = resolveSeedTarget("x.torrent", "/data")
	if err != nil || tpath != "x.torrent" || root != "/data" {
		t.Fatalf("resolveSeedTarget(torrent+dir) = %q, %q, %v", tpath, root, err)
	}

	// hf ref without a stored model fails.
	if _, _, err := resolveSeedTarget("hf:org/missing", ""); err == nil {
		t.Fatal("resolveSeedTarget accepted an unknown model")
	}

	// hf ref with model + torrent resolves to the torrent and owner dir.
	modelDir, _ := st.ModelDir("org/model")
	tpath, root, err = resolveSeedTarget("hf:org/model", "")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(tpath) != m.InfoHash+".torrent" {
		t.Fatalf("torrent path = %q", tpath)
	}
	if want := filepath.Dir(modelDir); root != want {
		t.Fatalf("data root = %q, want %q", root, want)
	}

	// Garbage target.
	if _, _, err := resolveSeedTarget("!!!", ""); err == nil {
		t.Fatal("resolveSeedTarget accepted garbage")
	}
}

func TestImportTargetsAndFakeOllama(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	storeModel(t, st, "org/model", map[string]string{"model.gguf": "GGUF-data"})

	// Bad target.
	if _, _, err := runRoot(t, "", "import", "!!!", "--dir", dir); err == nil {
		t.Fatal("import accepted garbage")
	}

	// Store model without any .gguf file: FindGGUF fails.
	if err := os.Remove(filepath.Join(dir, "store", "org", "model", "model.gguf")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runRoot(t, "", "import", "hf:org/model", "--dir", dir); err == nil {
		t.Fatal("import accepted a model without GGUF")
	}

	// Direct GGUF path with a fake ollama binary.
	if runtime.GOOS == "windows" {
		t.Skip("fake ollama script is unix-only")
	}
	gguf := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(gguf, []byte("GGUF-data"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeBin := filepath.Join(t.TempDir(), "fake-ollama")
	script := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	out, _, err := runRoot(t, "", "import", gguf, "--ollama", fakeBin, "--name", "fake:latest")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "imported as fake:latest") {
		t.Fatalf("import output = %q", out)
	}
}

func TestProgressPrinter(t *testing.T) {
	var shown bool
	print := progressPrinter(io.Discard, &shown)
	print(pull.ModeP2P, progressOf(0, 0, 0))
	if shown {
		t.Fatal("zero-total progress must be ignored")
	}

	var buf strings.Builder
	print = progressPrinter(&buf, &shown)
	print(pull.ModeP2P, progressOf(50, 100, 3))
	if !strings.Contains(buf.String(), "swarm  50%") || !strings.Contains(buf.String(), "peers 3") {
		t.Fatalf("p2p progress = %q", buf.String())
	}
	buf.Reset()
	print(pull.ModeHTTP, progressOf(2, 5, 0))
	if !strings.Contains(buf.String(), "http 2/5 files") {
		t.Fatalf("http progress = %q", buf.String())
	}
	buf.Reset()
	print("other", progressOf(1, 1, 0))
	if buf.String() != "" {
		t.Fatalf("unknown mode wrote %q", buf.String())
	}
}

func TestHumanBytesAndShortRev(t *testing.T) {
	cases := map[int64]string{
		5:          "5 B",
		2048:       "2 KiB",
		5 << 20:    "5.0 MiB",
		3 << 30:    "3.0 GiB",
		1<<30 + 11: "1.0 GiB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
	if got := shortRev(strings.Repeat("a", 40)); got != strings.Repeat("a", 12) {
		t.Errorf("shortRev = %q", got)
	}
	if got := shortRev("short"); got != "short" {
		t.Errorf("shortRev(short) = %q", got)
	}
}

func TestSwarmKeyValidation(t *testing.T) {
	// Inline hex accepted (validation only: the pull errors later at
	// discovery, not at flag parsing).
	dir := t.TempDir()
	if _, _, err := runRoot(t, "", "pull", "hf:org/model", "--dir", dir,
		"--swarm-key", strings.Repeat("a", 64)); err == nil {
		// A valid key with no DHT record available must still fail
		// (private swarm forbids the Hub fallback).
		t.Fatal("private swarm pull without any record must fail")
	}
	// Bad hex rejected up front.
	if _, _, err := runRoot(t, "", "pull", "hf:org/model", "--dir", t.TempDir(),
		"--swarm-key", "nothex"); err == nil || !strings.Contains(err.Error(), "64-char hex") {
		t.Fatalf("err = %v, want key validation error", err)
	}
	// Missing key file rejected up front.
	if _, _, err := runRoot(t, "", "pull", "hf:org/model", "--dir", t.TempDir(),
		"--swarm-key", "@/nonexistent/key.hex"); err == nil || !strings.Contains(err.Error(), "swarm key file") {
		t.Fatalf("err = %v, want file error", err)
	}
	// Key from file resolves.
	keyFile := filepath.Join(t.TempDir(), "swarm.pub")
	if err := os.WriteFile(keyFile, []byte(strings.Repeat("b", 64)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runRoot(t, "", "pull", "hf:org/model", "--dir", t.TempDir(),
		"--swarm-key", "@"+keyFile); err == nil {
		t.Fatal("valid key without a record must still fail (no Hub fallback)")
	}
}

// TestPullWithPeerJoinsLocalSwarm runs the deterministic two-machine demo
// path end to end: store A pulls over HTTP, an engine seeds it on a fixed
// port, a static origin serves the index, and store B pulls with --peer
// and must land in p2p mode.
func TestPullWithPeerJoinsLocalSwarm(t *testing.T) {
	gguf := "GGUF-SWARM-PAYLOAD"
	cfg := `{"model_type":"demo"}`
	hubMux := http.NewServeMux()
	hubMux.HandleFunc("/api/models/org/demo/revision/main", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"sha":"cafe123"}`)
	})
	hubMux.HandleFunc("/api/models/org/demo/tree/cafe123", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `[{"type":"file","path":"config.json","size":%d},
			{"type":"file","path":"model.gguf","size":%d,"lfs":{"oid":"%s"}}]`,
			len(cfg), len(gguf), sha256HexStr(gguf))
	})
	hubMux.HandleFunc("/org/demo/resolve/cafe123/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "config.json") {
			_, _ = w.Write([]byte(cfg))
			return
		}
		_, _ = w.Write([]byte(gguf))
	})
	hub := httptest.NewServer(hubMux)
	t.Cleanup(hub.Close)

	dirA := t.TempDir()
	t.Setenv("HF_ENDPOINT", hub.URL)
	if _, _, err := runRoot(t, "", "pull", "hf:org/demo", "--dir", dirA,
		"--no-daemon", "--http-only"); err != nil {
		t.Fatal(err)
	}

	// Seed store A on a fixed port.
	stA, err := store.Open(dirA)
	if err != nil {
		t.Fatal(err)
	}
	mfile, err := stA.ModelManifestFile("org/demo")
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Load(mfile)
	if err != nil {
		t.Fatal(err)
	}
	tpath, err := stA.TorrentPath(m.InfoHash)
	if err != nil {
		t.Fatal(err)
	}
	seeder, err := engine.New(engine.Config{DataDir: filepath.Join(dirA, "store", "org"),
		NoDHT: true, Seed: true, DisableUTP: true, ListenPort: 0}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = seeder.Close() })
	seedCtx, seedCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer seedCancel()
	if err := seeder.SeedTorrentFile(seedCtx, tpath); err != nil {
		t.Fatal(err)
	}
	seederAddr := fmt.Sprintf("127.0.0.1:%d", seeder.ListenPort())

	// Static origin over store A.
	origin := httptest.NewServer(http.FileServer(http.Dir(dirA)))
	t.Cleanup(origin.Close)

	// Machine B: pull with --peer.
	dirB := t.TempDir()
	out, _, err := runRoot(t, "", "pull", "hf:org/demo", "--dir", dirB,
		"--no-daemon", "--bootstrap", origin.URL, "--grace", "60s",
		"--peer", seederAddr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "via p2p") {
		t.Fatalf("pull output = %q, want p2p mode", out)
	}
	got, err := os.ReadFile(filepath.Join(dirB, "store", "org", "demo", "model.gguf"))
	if err != nil || string(got) != gguf {
		t.Fatalf("b file mismatch: err=%v", err)
	}
}

// captureStdout runs fn with os.Stdout redirected and returns what it
// printed. Functions under test write to os.Stdout directly.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	out := <-done
	_ = r.Close()
	return out
}

// TestPrintPullSummaryFormat pins the exact pull summary output: the human
// line feeds the project demo/screenshots, the JSON feeds scripts. Any
// format change must be a deliberate, documented decision.
func TestPrintPullSummaryFormat(t *testing.T) {
	res := pull.Result{
		Model:          "org/model",
		Revision:       "abcdef1234567890",
		Mode:           pull.ModeP2P,
		Files:          5,
		Size:           34933719,
		InfoHash:       "278afd9de89734be32a73e55931fc9ea6b9f7b49",
		ManifestSHA256: strings.Repeat("b", 64),
	}

	human := captureStdout(t, func() { printPullSummary(res, false) })
	wantHuman := "pulled org/model @abcdef123456 via p2p: 5 files, 33.3 MiB\n" +
		"manifest " + strings.Repeat("b", 64) + "\n" +
		"infohash 278afd9de89734be32a73e55931fc9ea6b9f7b49\n" +
		"to keep sharing: llmp2p seed hf:org/model\n"
	if human != wantHuman {
		t.Fatalf("human summary =\n%q\nwant\n%q", human, wantHuman)
	}

	asJSON := captureStdout(t, func() { printPullSummary(res, true) })
	wantJSON := "{\n" +
		"  \"model\": \"org/model\",\n" +
		"  \"revision\": \"abcdef1234567890\",\n" +
		"  \"mode\": \"p2p\",\n" +
		"  \"files\": 5,\n" +
		"  \"size\": 34933719,\n" +
		"  \"infoHash\": \"278afd9de89734be32a73e55931fc9ea6b9f7b49\",\n" +
		"  \"manifestSha256\": \"" + strings.Repeat("b", 64) + "\"\n" +
		"}\n"
	if asJSON != wantJSON {
		t.Fatalf("json summary =\n%q\nwant\n%q", asJSON, wantJSON)
	}

	cache := pull.Result{Model: "org/model", Revision: "r", Mode: pull.ModeCache}
	out := captureStdout(t, func() { printPullSummary(cache, false) })
	if strings.Contains(out, "to keep sharing") {
		t.Fatalf("cache hits must not print the seed hint: %q", out)
	}
}

// TestContextWithSignalCancel pins the seed signal contract: SIGINT and
// SIGTERM cancel the returned context (seeding stops cleanly).
func TestContextWithSignalCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal delivery to self is unreliable on windows")
	}
	for _, sig := range []os.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			ctx, cancel := contextWithSignal(context.Background())
			defer cancel()
			if err := syscall.Kill(os.Getpid(), sig.(syscall.Signal)); err != nil {
				t.Fatal(err)
			}
			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
				t.Fatalf("context not canceled by %s", sig)
			}
		})
	}
}

// TestIsHexKeyAcceptsUppercase pins the CLI swarm-key contract: hex keys
// are case-insensitive here (unlike the store path validator).
func TestIsHexKeyAcceptsUppercase(t *testing.T) {
	if !isHexKey(strings.ToUpper(strings.Repeat("a", 64))) {
		t.Fatal("uppercase hex must be accepted")
	}
	if !isHexKey(strings.Repeat("a", 64)) {
		t.Fatal("lowercase hex must be accepted")
	}
	if isHexKey(strings.Repeat("g", 64)) {
		t.Fatal("non-hex must be rejected")
	}
}
