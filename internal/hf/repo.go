package hf

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// RepoInfo is the resolved state of a model repository at a revision.
type RepoInfo struct {
	// Revision is the pinned commit sha.
	Revision string
	// Files lists every file in the repo (recursive), sorted by path.
	Files []FileInfo
}

// FileInfo describes one artifact of a repo.
type FileInfo struct {
	Path string
	Size int64
	// LFSOID is the sha256 of the file content for LFS-backed files,
	// empty for regular git blobs.
	LFSOID string
}

func (f FileInfo) IsLFS() bool { return f.LFSOID != "" }

// Resolve pins a repo revision and lists its files recursively. The
// revision argument may be a branch, a tag, a PR ref (refs/pr/1) or a
// commit sha; the returned Revision is always the pinned commit sha.
func (c *Client) Resolve(ctx context.Context, repoID, revision string) (*RepoInfo, error) {
	metaURL := c.endpoint("/api/models/"+repoID+"/revision/"+revision, nil)

	req, err := c.newRequest(ctx, http.MethodGet, metaURL, nil)
	if err != nil {
		return nil, err
	}
	var meta struct {
		SHA string `json:"sha"`
	}
	if err := c.do(req, &meta); err != nil {
		return nil, fmt.Errorf("resolve %s@%s: %w", repoID, revision, err)
	}

	files, err := c.listTree(ctx, repoID, meta.SHA)
	if err != nil {
		return nil, fmt.Errorf("list %s@%s: %w", repoID, revision, err)
	}
	return &RepoInfo{Revision: meta.SHA, Files: files}, nil
}

type treeEntry struct {
	Type string `json:"type"`
	Path string `json:"path"`
	Size int64  `json:"size"`
	LFS  *struct {
		OID string `json:"oid"`
	} `json:"lfs"`
}

func (c *Client) listTree(ctx context.Context, repoID, sha string) ([]FileInfo, error) {
	var files []FileInfo
	page := c.endpoint("/api/models/"+repoID+"/tree/"+sha, url.Values{
		"recursive": {"true"},
		"expand":    {"false"},
	})
	for page != "" {
		req, err := c.newRequest(ctx, http.MethodGet, page, nil)
		if err != nil {
			return nil, err
		}
		res, err := c.httpClient().Do(req)
		if err != nil {
			return nil, err
		}
		var entries []treeEntry
		err = decodeResponse(res, &entries)
		nextRel := linkNext(res.Header.Get("Link"))
		_ = res.Body.Close()
		if err != nil {
			return nil, err
		}

		for _, e := range entries {
			if e.Type != "file" {
				continue
			}
			fi := FileInfo{Path: e.Path, Size: e.Size}
			if e.LFS != nil {
				fi.LFSOID = e.LFS.OID
			}
			files = append(files, fi)
		}

		if nextRel == "" {
			break
		}
		// The Link header carries a relative URL: absolutize it against
		// the page we just fetched.
		next, err := res.Request.URL.Parse(nextRel)
		if err != nil {
			return nil, fmt.Errorf("list %s@%s: bad Link header %q: %w", repoID, sha, nextRel, err)
		}
		page = next.String()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// linkNext extracts the URL of the next page from an RFC 5988 Link header.
func linkNext(header string) string {
	for _, part := range splitLinkHeader(header) {
		if url, rel, ok := strings.Cut(part, ";"); ok && strings.TrimSpace(rel) == `rel="next"` {
			return strings.Trim(strings.TrimSpace(url), "<>")
		}
	}
	return ""
}

func splitLinkHeader(header string) []string {
	if header == "" {
		return nil
	}
	return strings.Split(header, ",")
}
