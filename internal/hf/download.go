package hf

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// ErrChecksum is returned when a downloaded artifact does not match the
// expected sha256.
var ErrChecksum = errors.New("hf: checksum mismatch")

// artifactURL builds the CDN resolve URL for one repo file. Every segment
// is percent-escaped individually so paths keep their structure.
func (c *Client) artifactURL(repoID, revision, path string) string {
	segments := []string{}
	for _, seg := range strings.Split(repoID, "/") {
		segments = append(segments, url.PathEscape(seg))
	}
	segments = append(segments, "resolve")
	for _, part := range []string{revision, path} {
		for _, seg := range strings.Split(part, "/") {
			segments = append(segments, url.PathEscape(seg))
		}
	}
	return c.baseURL() + "/" + strings.Join(segments, "/")
}

func (c *Client) downloadClient() *http.Client {
	// Downloads can be large and long-lived: no client timeout, the
	// caller's context governs cancellation.
	if c.HTTP != nil && c.HTTP.Transport != nil {
		return &http.Client{Transport: c.HTTP.Transport}
	}
	return &http.Client{}
}

// Download streams one repo artifact into w and returns its sha256 hex
// digest and total byte count.
func (c *Client) Download(ctx context.Context, repoID, revision, path string, w io.Writer) (string, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.artifactURL(repoID, revision, path), nil)
	if err != nil {
		return "", 0, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	res, err := c.downloadClient().Do(req)
	if err != nil {
		return "", 0, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return "", 0, ErrNotFound
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", 0, &HTTPError{Status: res.StatusCode, URL: req.URL.String()}
	}

	hasher := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, hasher), res.Body)
	if err != nil {
		return "", 0, fmt.Errorf("hf: download %s: %w", path, err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), n, nil
}

// DownloadFile downloads one repo artifact to dst with resume support and
// integrity checking. Partial data is written to dst + ".llmp2p.part" and
// renamed atomically on success. When wantSHA256 is non-empty the final
// digest must match or ErrChecksum is returned and the partial file removed.
func (c *Client) DownloadFile(ctx context.Context, repoID, revision, path, dst, wantSHA256 string) (int64, error) {
	tmp := dst + ".llmp2p.part"

	offset, hasher, f, err := resumeState(tmp)
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.artifactURL(repoID, revision, path), nil)
	if err != nil {
		f.Close()
		return 0, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	res, err := c.downloadClient().Do(req)
	if err != nil {
		f.Close()
		return 0, err
	}
	defer res.Body.Close()

	switch {
	case res.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		// The server cannot satisfy the range: restart from scratch.
		f.Close()
		if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
			return 0, err
		}
		return c.DownloadFile(ctx, repoID, revision, path, dst, wantSHA256)
	case res.StatusCode == http.StatusPartialContent:
		// Resuming from offset.
	case res.StatusCode == http.StatusNotFound:
		f.Close()
		return 0, ErrNotFound
	case res.StatusCode >= 200 && res.StatusCode < 300:
		// Server ignored the Range header (or none was sent): rewrite
		// from the beginning and rehash the full body.
		if offset > 0 {
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				f.Close()
				return 0, err
			}
			hasher.Reset()
		}
	default:
		f.Close()
		return 0, &HTTPError{Status: res.StatusCode, URL: req.URL.String()}
	}

	n, err := io.Copy(io.MultiWriter(f, hasher), res.Body)
	if err != nil {
		f.Close()
		return 0, fmt.Errorf("hf: download %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return 0, err
	}
	if err := f.Close(); err != nil {
		return 0, err
	}

	got := hex.EncodeToString(hasher.Sum(nil))
	if wantSHA256 != "" && got != wantSHA256 {
		os.Remove(tmp)
		return 0, fmt.Errorf("%w: got %s want %s", ErrChecksum, got, wantSHA256)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return 0, err
	}
	return offset + n, nil
}

// resumeState opens the partial file for appending and seeds the hasher
// with its existing content so the final digest covers the whole artifact.
func resumeState(tmp string) (int64, hash.Hash, *os.File, error) {
	info, err := os.Stat(tmp)
	switch {
	case err == nil:
		f, err := os.OpenFile(tmp, os.O_RDWR, 0o644)
		if err != nil {
			return 0, nil, nil, err
		}
		hasher := newDigest()
		if _, err := io.Copy(hasher, io.LimitReader(f, info.Size())); err != nil {
			f.Close()
			return 0, nil, nil, err
		}
		if _, err := f.Seek(0, io.SeekEnd); err != nil {
			f.Close()
			return 0, nil, nil, err
		}
		return info.Size(), hasher, f, nil
	case os.IsNotExist(err):
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
		if err != nil {
			return 0, nil, nil, err
		}
		return 0, newDigest(), f, nil
	default:
		return 0, nil, nil, err
	}
}

func newDigest() hash.Hash { return sha256.New() }
