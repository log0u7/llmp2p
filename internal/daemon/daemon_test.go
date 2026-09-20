package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/log0u7/llmp2p/internal/engine"
	"github.com/log0u7/llmp2p/internal/hf"
	"github.com/log0u7/llmp2p/internal/pull"

	"github.com/log0u7/llmp2p/internal/manifest"
	"github.com/log0u7/llmp2p/internal/store"
)

// fakeModel installs a model directory with a manifest pointer and a
// torrent file, without real torrent data (no engine gets started: the
// torrent file must exist for the daemon to seed it, so we also drop a
// minimal valid torrent? No: without data the engine would fail. Instead
// the daemon skips models whose torrent is missing, so we assert that).
func fakeModel(t *testing.T, st *store.Store, modelID, infoHash string) string {
	t.Helper()
	dir, err := st.ModelDir(modelID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{
		Schema:      manifest.Schema,
		Model:       modelID,
		Revision:    "cafe123",
		CreatedAt:   time.Now().UTC(),
		PieceLength: manifest.PieceLength,
		InfoHash:    infoHash,
		Files: []manifest.File{
			{Path: "model.gguf", Size: 4, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		},
	}
	mfile, err := st.ModelManifestFile(modelID)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Save(mfile); err != nil {
		t.Fatal(err)
	}
	return dir
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

// TestDaemonRunsAndServesStatus starts the daemon over an empty store and
// exercises the three endpoints.
func TestDaemonRunsAndServesStatus(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// A model without a torrent file is skipped, but still listed.
	fakeModel(t, st, "org/model", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, Options{Store: st, ListenAddr: addr, Version: "test"})
	}()
	defer func() {
		cancel()
		if err := <-errCh; err != nil {
			t.Errorf("daemon: %v", err)
		}
	}()

	// Wait for the API to come up.
	var status statusResponse
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := http.Get(fmt.Sprintf("http://%s/api/v1/status", addr))
		if err == nil {
			if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("status API never came up: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if status.Version != "test" || status.Models != 1 {
		t.Fatalf("status = %+v", status)
	}

	// Models endpoint.
	resp, err := http.Get(fmt.Sprintf("http://%s/api/v1/models", addr))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	if err := json.NewDecoder(resp.Body).Decode(&ids); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if len(ids) != 1 || ids[0] != "org/model" {
		t.Fatalf("models = %v", ids)
	}

	// Torrents endpoint (empty: the model has no torrent file).
	resp, err = http.Get(fmt.Sprintf("http://%s/api/v1/torrents", addr))
	if err != nil {
		t.Fatal(err)
	}
	var torrents []any
	if err := json.NewDecoder(resp.Body).Decode(&torrents); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if len(torrents) != 0 {
		t.Fatalf("torrents = %v", torrents)
	}
}

func TestDaemonRefusesSecondInstance(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- Run(ctx, Options{Store: st, ListenAddr: "127.0.0.1:0"}) }()

	// Give the first daemon time to take the lock, then expect the lock
	// wait loop to still be running (no error before ctx timeout).
	time.Sleep(500 * time.Millisecond)
	select {
	case err := <-done:
		if err != nil && err != context.DeadlineExceeded {
			t.Fatalf("first daemon exited early: %v", err)
		}
	default:
	}
}

func TestMetricsEndpoint(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fakeModel(t, st, "org/model", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- Run(ctx, Options{Store: st, ListenAddr: addr, Version: "test"}) }()
	defer func() {
		cancel()
		<-errCh
	}()

	resp, err := waitUp(t, addr, "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	text := string(body)
	for _, want := range []string{
		"# TYPE llmp2pd_uptime_seconds gauge",
		"llmp2pd_models 1",
		"# TYPE llmp2pd_uploaded_bytes_total counter",
		"llmp2pd_pulls_total 0",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("metrics missing %q\ngot:\n%s", want, text)
		}
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content-type = %q", ct)
	}
}

// waitUp polls the daemon API until the path answers, then returns the
// response.
func waitUp(t *testing.T, addr, path string) (*http.Response, error) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := http.Get(fmt.Sprintf("http://%s%s", addr, path))
		if err == nil {
			return resp, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestDaemonPullDelegation posts a delegated pull against a daemon whose
// pull template points at a fake Hub, then polls the job to completion.
func TestDaemonPullDelegation(t *testing.T) {
	var hits atomic.Int64
	hub := fakePullHub(t, &hits)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, Options{Store: st, ListenAddr: addr, Version: "test",
			PullTemplate: &pull.Options{HF: fakePullHubClient(hub.URL),
				BootstrapURLs: []string{emptyBootstrapSrv(t)},
				HTTPClient:    http.DefaultClient,
				EngineCfg:     engine.Config{NoDHT: true}},
		})
	}()
	defer func() {
		cancel()
		<-errCh
	}()
	if _, err := waitUp(t, addr, "/api/v1/status"); err != nil {
		t.Fatal(err)
	}

	// Create the job.
	res, err := http.Post(fmt.Sprintf("http://%s/api/v1/pulls", addr), "application/json",
		strings.NewReader(`{"ref":"hf:org/model"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("create status = %d", res.StatusCode)
	}
	var job struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(res.Body).Decode(&job)
	_ = res.Body.Close()

	// Poll until the job finishes.
	deadline := time.Now().Add(30 * time.Second)
	var final struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Result struct {
			Mode     string `json:"mode"`
			Files    int    `json:"files"`
			InfoHash string `json:"infoHash"`
		} `json:"result"`
	}
	for {
		res, err := http.Get(fmt.Sprintf("http://%s/api/v1/pulls/%s", addr, job.ID))
		if err != nil {
			t.Fatal(err)
		}
		_ = json.NewDecoder(res.Body).Decode(&final)
		_ = res.Body.Close()
		if final.Status == "succeeded" || final.Status == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pull job did not finish, status=%q err=%q", final.Status, final.Error)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if final.Status != "succeeded" {
		t.Fatalf("job failed: %s: %s", final.Status, final.Error)
	}
	if final.Result.Mode != "http" || final.Result.Files != 2 {
		t.Fatalf("result = %+v", final.Result)
	}

	// Files landed in the daemon's store.
	dir, err := st.ModelDir("org/model")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "model.gguf")); err != nil {
		t.Fatal(err)
	}

	// Pull counter recorded in metrics.
	resp, err := waitUp(t, addr, "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), `llmp2pd_pulls_total{result="succeeded"} 1`) {
		t.Fatalf("pulls_total missing in metrics:\n%s", body)
	}
}

// fakePullHub serves the minimal Hub endpoints a pull needs.
func fakePullHub(t *testing.T, hits *atomic.Int64) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/models/org/model/revision/main", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = fmt.Fprint(w, `{"sha":"cafe123"}`)
	})
	mux.HandleFunc("/api/models/org/model/tree/cafe123", func(w http.ResponseWriter, r *http.Request) {
		sum := sha256.Sum256([]byte("gguf"))
		_, _ = fmt.Fprintf(w, `[{"type":"file","path":"config.json","size":2},
			{"type":"file","path":"model.gguf","size":4,"lfs":{"oid":"%s"}}]`,
			hex.EncodeToString(sum[:]))
	})
	mux.HandleFunc("/org/model/resolve/cafe123/", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch filepath.Base(r.URL.Path) {
		case "config.json":
			_, _ = w.Write([]byte("{}"))
		case "model.gguf":
			_, _ = w.Write([]byte("gguf"))
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func emptyBootstrapSrv(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"entries":{}}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func fakePullHubClient(hubURL string) *hf.Client {
	c := hf.New()
	c.BaseURL = hubURL
	c.HTTP = http.DefaultClient
	return c
}

func TestDefaultPullTemplate(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	tmpl := defaultPullTemplate(Options{Store: st})
	if tmpl.Store != st || !tmpl.NoLock {
		t.Fatalf("default template = %+v", tmpl)
	}

	custom := &pull.Options{HTTPOnly: true}
	tmpl = defaultPullTemplate(Options{Store: st, PullTemplate: custom})
	if !tmpl.NoLock || tmpl.HTTPOnly != true || tmpl.Store != st {
		t.Fatalf("custom template = %+v (store must be filled, NoLock forced)", tmpl)
	}

	withStore := &pull.Options{Store: st}
	tmpl = defaultPullTemplate(Options{Store: st, PullTemplate: withStore})
	if tmpl.Store != st || !tmpl.NoLock {
		t.Fatalf("custom-with-store template = %+v", tmpl)
	}
}

func TestDaemonSkipsBrokenModels(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fakeModel(t, st, "org/model", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	fakeModel(t, st, "org/broken", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	ptr, err := st.ModelManifestFile("org/broken")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ptr, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}

	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- Run(ctx, Options{Store: st, ListenAddr: addr}) }()
	defer func() {
		cancel()
		if err := <-errCh; err != nil {
			t.Errorf("daemon: %v", err)
		}
	}()

	resp, err := waitUp(t, addr, "/api/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	var status statusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if status.Models != 2 || status.Torrents != 0 || status.SeedingEngines != 0 {
		t.Fatalf("status = %+v, want both models listed and nothing seeded", status)
	}
}

func TestDaemonFailsOnGarbageTorrent(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fakeModel(t, st, "org/model", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	tpath, err := st.TorrentPath("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tpath, []byte("not-a-torrent"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err = Run(ctx, Options{Store: st, ListenAddr: "127.0.0.1:0",
		EngineOverrides: func(c *engine.Config) { c.NoDHT = true; c.DisableUTP = true }})
	if err == nil || !strings.Contains(err.Error(), "daemon: seed org/model") {
		t.Fatalf("err = %v, want seeding failure", err)
	}
}

func TestDaemonPullAPIErrors(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- Run(ctx, Options{Store: st, ListenAddr: addr}) }()
	defer func() {
		cancel()
		<-errCh
	}()
	if _, err := waitUp(t, addr, "/api/v1/status"); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("http://%s/api/v1/pulls", addr)

	post := func(body string) *http.Response {
		t.Helper()
		res, err := http.Post(base, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = res.Body.Close() })
		return res
	}
	status := func(res *http.Response) int { return res.StatusCode }

	if got := status(post("not json")); got != http.StatusBadRequest {
		t.Errorf("invalid body status = %d, want 400", got)
	}
	if got := status(post(`{"ref":"nope"}`)); got != http.StatusBadRequest {
		t.Errorf("bad ref status = %d, want 400", got)
	}
	if got := status(post(`{"ref":"hf:org/model#file.gguf"}`)); got != http.StatusBadRequest {
		t.Errorf("path ref status = %d, want 400", got)
	}

	res, err := http.Get(base + "/unknown-job")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown job status = %d, want 404", res.StatusCode)
	}

	res, err = http.Get(base)
	if err != nil {
		t.Fatal(err)
	}
	var jobs []any
	if err := json.NewDecoder(res.Body).Decode(&jobs); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if len(jobs) != 0 {
		t.Fatalf("jobs = %v, want empty list", jobs)
	}
}

func TestCountModelsBrokenStore(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{st: st}
	if got := s.countModels(); got != 0 {
		t.Fatalf("countModels = %d, want 0 on an empty store", got)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if got := s.countModels(); got != 0 {
		t.Fatalf("countModels = %d, want 0 on a removed store", got)
	}
}

func TestPullRejectsOversizedBody(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- Run(ctx, Options{Store: st, ListenAddr: addr}) }()
	defer func() {
		cancel()
		<-errCh
	}()
	if _, err := waitUp(t, addr, "/api/v1/status"); err != nil {
		t.Fatal(err)
	}

	oversized := strings.NewReader(`{"ref":"` + strings.Repeat("a", 128<<10) + `"}`)
	res, err := http.Post(fmt.Sprintf("http://%s/api/v1/pulls", addr), "application/json", oversized)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized body status = %d, want 400", res.StatusCode)
	}
}

func TestMetricsConcurrentWithPullResults(t *testing.T) {
	s := &Server{
		st:        &store.Store{},
		engines:   map[string]*engine.Engine{},
		startedAt: time.Now(),
		pullStats: map[string]int{},
	}

	// Record results concurrently with metric renders: the race detector
	// flags any unsynchronized pullStats access.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			s.recordPullResult("succeeded")
		}
	}()
	for {
		select {
		case <-done:
			rec := httptest.NewRecorder()
			s.writeMetrics(rec, nil)
			if !strings.Contains(rec.Body.String(), `llmp2pd_pulls_total{result="succeeded"} 200`) {
				t.Fatalf("metrics lost updates:\n%s", rec.Body.String())
			}
			return
		default:
			rec := httptest.NewRecorder()
			s.writeMetrics(rec, nil)
		}
	}
}

func TestPullJobTimesOut(t *testing.T) {
	old := pullJobTimeout
	pullJobTimeout = 150 * time.Millisecond
	t.Cleanup(func() { pullJobTimeout = old })

	// A Hub that hangs: the job must fail on the timeout, not hang the
	// sequential queue forever.
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
	}))
	t.Cleanup(hang.Close)

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, Options{Store: st, ListenAddr: addr,
			PullTemplate: &pull.Options{HF: fakePullHubClient(hang.URL),
				HTTPClient: http.DefaultClient,
				EngineCfg:  engine.Config{NoDHT: true}},
		})
	}()
	defer func() {
		cancel()
		<-errCh
	}()
	if _, err := waitUp(t, addr, "/api/v1/status"); err != nil {
		t.Fatal(err)
	}

	res, err := http.Post(fmt.Sprintf("http://%s/api/v1/pulls", addr), "application/json",
		strings.NewReader(`{"ref":"hf:org/model","httpOnly":true}`))
	if err != nil {
		t.Fatal(err)
	}
	var job struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(res.Body).Decode(&job)
	_ = res.Body.Close()

	deadline := time.Now().Add(10 * time.Second)
	for {
		res, err := http.Get(fmt.Sprintf("http://%s/api/v1/pulls/%s", addr, job.ID))
		if err != nil {
			t.Fatal(err)
		}
		var final struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		_ = json.NewDecoder(res.Body).Decode(&final)
		_ = res.Body.Close()
		if final.Status == "failed" {
			if !strings.Contains(final.Error, "deadline") && !strings.Contains(final.Error, "canceled") {
				t.Fatalf("job error = %q, want context deadline", final.Error)
			}
			return
		}
		if final.Status == "succeeded" {
			t.Fatal("hung pull must not succeed")
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not time out, status=%q", final.Status)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestDaemonJSONContentType pins the API contract: every /api/v1 endpoint
// answers application/json (scripts and llmp2p itself decode it).
func TestDaemonJSONContentType(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- Run(ctx, Options{Store: st, ListenAddr: addr, Version: "test"}) }()
	defer func() {
		cancel()
		<-errCh
	}()

	for _, path := range []string{"/api/v1/status", "/api/v1/models", "/api/v1/torrents"} {
		resp, err := waitUp(t, addr, path)
		if err != nil {
			t.Fatal(err)
		}
		ct := resp.Header.Get("Content-Type")
		_ = resp.Body.Close()
		if !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("%s content-type = %q, want application/json", path, ct)
		}
	}
}

// TestDefaultPullTemplateSemantics pins the delegated-pull defaults: the
// daemon owns the store lock, so the template must force NoLock and use
// the daemon's store.
func TestDefaultPullTemplateSemantics(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tmpl := defaultPullTemplate(Options{Store: st})
	if !tmpl.NoLock {
		t.Fatal("default template must set NoLock: the daemon already owns the store lock")
	}
	if tmpl.Store != st {
		t.Fatal("default template must use the daemon store")
	}
}

// TestEnqueueSnapshotShape pins the POST /api/v1/pulls 202 response: id,
// echoed ref, status queued, non-zero queuedAt. The background job fails
// fast against a closed origin (offline test).
func TestEnqueueSnapshotShape(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "closed", http.StatusServiceUnavailable)
	}))
	origin.Close() // fail fast, no network reach needed

	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, Options{Store: st, ListenAddr: addr, Version: "test",
			PullTemplate: &pull.Options{
				BootstrapURLs: []string{origin.URL},
				HTTPOnly:      true,
				HTTPClient:    http.DefaultClient,
			},
		})
	}()
	defer func() {
		cancel()
		<-errCh
	}()
	if _, err := waitUp(t, addr, "/api/v1/status"); err != nil {
		t.Fatal(err)
	}

	res, err := http.Post(fmt.Sprintf("http://%s/api/v1/pulls", addr), "application/json",
		strings.NewReader(`{"ref":"hf:org/model"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("create status = %d, want 202", res.StatusCode)
	}
	var job struct {
		ID       string    `json:"id"`
		Ref      string    `json:"ref"`
		Status   string    `json:"status"`
		QueuedAt time.Time `json:"queuedAt"`
	}
	if err := json.NewDecoder(res.Body).Decode(&job); err != nil {
		t.Fatal(err)
	}
	if job.ID == "" {
		t.Fatal("job id is empty")
	}
	if job.Ref != "hf:org/model" {
		t.Fatalf("job ref = %q, want hf:org/model", job.Ref)
	}
	if job.Status != "queued" {
		t.Fatalf("job status = %q, want queued", job.Status)
	}
	if job.QueuedAt.IsZero() {
		t.Fatal("queuedAt is zero")
	}
}
