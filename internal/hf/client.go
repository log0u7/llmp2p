// Package hf is a minimal Hugging Face Hub client used to resolve model
// repositories (revision, file listing, LFS sha256) and download artifacts
// with integrity checking.
package hf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is the public Hub endpoint.
const DefaultBaseURL = "https://huggingface.co"

// ErrNotFound is returned when a repo, revision, or artifact does not exist.
var ErrNotFound = errors.New("hf: not found")

// HTTPError is a non-2xx Hub response other than 404.
type HTTPError struct {
	Status int
	URL    string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("hf: %s returned %d", e.URL, e.Status)
}

// Client talks to the Hugging Face Hub API.
type Client struct {
	// BaseURL defaults to DefaultBaseURL.
	BaseURL string
	// Token is an optional access token for gated or rate-limited access.
	Token string
	// HTTP is the underlying transport; zero value uses a sane default.
	HTTP *http.Client
}

// New returns a Client using DefaultBaseURL and the default transport.
func New() *Client {
	return &Client{
		BaseURL: DefaultBaseURL,
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) newRequest(ctx context.Context, method, urlStr string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, urlStr, body)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	return req, nil
}

// do executes the request and unmarshals a JSON response into out when out
// is not nil. Long downloads must use raw instead.
func (c *Client) do(req *http.Request, out any) error {
	res, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	return decodeResponse(res, out)
}

func decodeResponse(res *http.Response, out any) error {
	if res.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return &HTTPError{Status: res.StatusCode, URL: res.Request.URL.String()}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, res.Body)
		return nil
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("hf: decode %s: %w", res.Request.URL, err)
	}
	return nil
}

func (c *Client) endpoint(path string, query url.Values) string {
	u := strings.TrimSuffix(c.baseURL(), "/") + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return u
}

func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return DefaultBaseURL
}
