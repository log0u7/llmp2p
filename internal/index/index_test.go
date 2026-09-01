package index

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	ih40  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sha64 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func validEntry(model string) Entry {
	return Entry{
		Model:          model,
		InfoHash:       ih40,
		ManifestSHA256: sha64,
		Revision:       "cafe123",
		Size:           1024,
		AddedAt:        time.Now().UTC().Truncate(time.Second),
		AddedBy:        "tester",
	}
}

func TestEntryValidate(t *testing.T) {
	if err := validEntry("org/model").Validate(); err != nil {
		t.Fatal(err)
	}
	bad := []func(*Entry){
		func(e *Entry) { e.Model = "no-slash" },
		func(e *Entry) { e.InfoHash = "short" },
		func(e *Entry) { e.ManifestSHA256 = "not-hex" },
		func(e *Entry) { e.Revision = "../etc" },
		func(e *Entry) { e.Size = -1 },
	}
	for i, f := range bad {
		e := validEntry("org/model")
		f(&e)
		if err := e.Validate(); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
}

func TestAddConflict(t *testing.T) {
	ix := &Index{}
	if err := ix.Add(validEntry("org/model")); err != nil {
		t.Fatal(err)
	}
	other := validEntry("org/model")
	other.ManifestSHA256 = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if err := ix.Add(other); err == nil {
		t.Fatal("conflicting entry must be rejected")
	}
}

func TestParseDropsInvalidEntries(t *testing.T) {
	doc := `{"entries":{
		"good/model":{"model":"good/model","infoHash":"` + ih40 + `","manifestSha256":"` + sha64 + `","revision":"r1","size":1},
		"bad/model":{"model":"bad/model","infoHash":"zz","manifestSha256":"` + sha64 + `","revision":"r1","size":1},
		"mismatch":{"model":"other/model","infoHash":"` + ih40 + `","manifestSha256":"` + sha64 + `","revision":"r1","size":1}
	}}`
	ix, warnings, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(ix.Entries))
	}
	if _, ok := ix.Get("good/model"); !ok {
		t.Fatal("valid entry missing")
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestFetchMergesOriginsFirstWins(t *testing.T) {
	originA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"entries":{"org/a":{"model":"org/a","infoHash":"` + ih40 + `","manifestSha256":"` + sha64 + `","revision":"rA","size":1}}}`))
	}))
	defer originA.Close()
	originB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		shaC := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
		_, _ = w.Write([]byte(`{"entries":{
			"org/a":{"model":"org/a","infoHash":"` + ih40 + `","manifestSha256":"` + shaC + `","revision":"rB","size":1},
			"org/b":{"model":"org/b","infoHash":"` + ih40 + `","manifestSha256":"` + shaC + `","revision":"rB","size":2}
		}}`))
	}))
	defer originB.Close()

	res, err := Fetch(nil, []string{originA.URL, originB.URL})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("errors = %v", res.Errors)
	}
	if len(res.Index.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(res.Index.Entries))
	}
	a, _ := res.Index.Get("org/a")
	if a.Revision != "rA" {
		t.Fatalf("first origin must win: got %q", a.Revision)
	}
	// One conflict warning for org/a.
	conflicts := 0
	for _, w := range res.Warnings {
		if len(w) > 10 && w[:10] == "index: con" {
			conflicts++
		}
	}
	if conflicts != 1 {
		t.Fatalf("conflict warnings = %d, want 1 (%v)", conflicts, res.Warnings)
	}
}

func TestFetchAllOriginsFail(t *testing.T) {
	res, err := Fetch(nil, []string{"http://127.0.0.1:1"})
	if err == nil {
		t.Fatal("expected error when all origins fail")
	}
	if res == nil || res.Index == nil {
		t.Fatal("result with empty index must still be returned")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	ix := &Index{}
	if err := ix.Add(validEntry("org/model")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "index.json")
	if err := ix.Save(path); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := parsed.Get("org/model"); !ok {
		t.Fatal("entry lost in round trip")
	}
}

func TestManifestURL(t *testing.T) {
	got := ManifestURL("https://example.com/llmp2p/main/", sha64)
	want := "https://example.com/llmp2p/main/manifests/" + sha64 + ".json"
	if got != want {
		t.Fatalf("ManifestURL = %q, want %q", got, want)
	}
}
