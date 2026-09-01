// Package index implements the llmp2p bootstrap index: a small JSON file
// mapping model ids to the torrent infohash and manifest hash needed to
// join their swarm. Index files live at known HTTPS URLs (bootstrap
// origins); manifests are served alongside them.
//
// First pulls that find no entry fall back to HTTP download from the Hub
// and generate a manifest themselves.
package index

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/log0u7/llmp2p/internal/ref"
)

// Entry binds a model to its swarm.
type Entry struct {
	Model          string    `json:"model"`
	InfoHash       string    `json:"infoHash"`       // v1 btih, 40 hex
	ManifestSHA256 string    `json:"manifestSha256"` // manifest content hash, 64 hex
	Revision       string    `json:"revision"`       // pinned commit sha
	Size           int64     `json:"size"`
	AddedAt        time.Time `json:"addedAt"`
	AddedBy        string    `json:"addedBy,omitempty"`
}

// Index is a set of entries keyed by model id.
type Index struct {
	Entries map[string]Entry `json:"entries"`
}

var (
	infoHashHexRe = regexp.MustCompile(`^[0-9a-f]{40}$`)
	manifestHexRe = regexp.MustCompile(`^[0-9a-f]{64}$`)
	revisionRe    = regexp.MustCompile(`^[a-zA-Z0-9._/-]{1,200}$`)
)

// Validate checks one entry's fields.
func (e Entry) Validate() error {
	if !ref.ValidModelID(e.Model) {
		return fmt.Errorf("index: invalid model id %q", e.Model)
	}
	if !infoHashHexRe.MatchString(e.InfoHash) {
		return fmt.Errorf("index %s: invalid infohash %q", e.Model, e.InfoHash)
	}
	if !manifestHexRe.MatchString(e.ManifestSHA256) {
		return fmt.Errorf("index %s: invalid manifest sha256", e.Model)
	}
	if !revisionRe.MatchString(e.Revision) {
		return fmt.Errorf("index %s: invalid revision %q", e.Model, e.Revision)
	}
	if strings.Contains(e.Revision, "..") {
		return fmt.Errorf("index %s: invalid revision %q", e.Model, e.Revision)
	}
	if e.Size < 0 {
		return fmt.Errorf("index %s: negative size", e.Model)
	}
	return nil
}

// Get returns the entry for a model id.
func (ix *Index) Get(model string) (Entry, bool) {
	e, ok := ix.Entries[model]
	return e, ok
}

// Remove deletes the entry for a model id and reports whether it existed.
func (ix *Index) Remove(model string) bool {
	if _, ok := ix.Entries[model]; !ok {
		return false
	}
	delete(ix.Entries, model)
	return true
}

// Add inserts an entry, returning an error if an incompatible entry
// already exists (same model, different manifest).
func (ix *Index) Add(e Entry) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if existing, ok := ix.Entries[e.Model]; ok && existing.ManifestSHA256 != e.ManifestSHA256 {
		return fmt.Errorf("index: conflicting entry for %s: have %s, adding %s",
			e.Model, existing.ManifestSHA256[:12], e.ManifestSHA256[:12])
	}
	if ix.Entries == nil {
		ix.Entries = make(map[string]Entry)
	}
	ix.Entries[e.Model] = e
	return nil
}

// Parse decodes and validates an index document. Invalid individual
// entries are dropped and reported; the document itself must be valid JSON.
func Parse(b []byte) (*Index, []string, error) {
	ix := &Index{}
	if err := json.Unmarshal(b, ix); err != nil {
		return nil, nil, fmt.Errorf("index: decode: %w", err)
	}
	var warnings []string
	valid := make(map[string]Entry, len(ix.Entries))
	for model, e := range ix.Entries {
		if err := e.Validate(); err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		if model != e.Model {
			warnings = append(warnings, fmt.Sprintf("index: key %q does not match entry model %q", model, e.Model))
			continue
		}
		valid[model] = e
	}
	ix.Entries = valid
	sort.Strings(warnings)
	return ix, warnings, nil
}

// Save writes the index document to path.
func (ix *Index) Save(path string) error {
	b, err := json.MarshalIndent(ix, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// FetchResult carries the merged index and per-URL fetch diagnostics.
type FetchResult struct {
	Index    *Index
	Warnings []string
	Errors   []error
}

// Fetch retrieves index documents from the given bootstrap base URLs in
// order and merges them. Entries seen first win; conflicting entries are
// reported as warnings. Unreachable origins are reported in Errors but do
// not abort the merge.
func Fetch(client *http.Client, base_urls []string) (*FetchResult, error) {
	if client == nil {
		client = http.DefaultClient
	}
	res := &FetchResult{Index: &Index{Entries: map[string]Entry{}}}
	for _, base := range base_urls {
		url := trimSlash(base) + "/index.json"
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			res.Errors = append(res.Errors, err)
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("index: fetch %s: %w", url, err))
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		_ = resp.Body.Close()
		if err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("index: read %s: %w", url, err))
			continue
		}
		if resp.StatusCode != http.StatusOK {
			res.Errors = append(res.Errors, fmt.Errorf("index: fetch %s: status %d", url, resp.StatusCode))
			continue
		}
		ix, warnings, err := Parse(body)
		if err != nil {
			res.Errors = append(res.Errors, err)
			continue
		}
		res.Warnings = append(res.Warnings, warnings...)
		for model, e := range ix.Entries {
			if existing, ok := res.Index.Entries[model]; ok {
				if existing.ManifestSHA256 != e.ManifestSHA256 {
					res.Warnings = append(res.Warnings,
						fmt.Sprintf("index: conflicting entries for %s across origins", model))
				}
				continue // first origin wins
			}
			res.Index.Entries[model] = e
		}
	}
	if len(res.Errors) == len(base_urls) && len(base_urls) > 0 {
		return res, errors.New("index: all bootstrap origins failed")
	}
	return res, nil
}

// ManifestURL builds the URL of a manifest hosted next to an index.
func ManifestURL(base, manifestSHA256 string) string {
	return trimSlash(base) + "/manifests/" + manifestSHA256 + ".json"
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
