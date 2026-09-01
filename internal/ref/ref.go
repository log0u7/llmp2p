// Package ref parses llmp2p model references of the form
//
//	hf:owner/repo[@revision][#/path/to/artifact]
//
// The revision defaults to "main" and an empty path means "whole repo".
package ref

import (
	"fmt"
	"regexp"
	"strings"
)

// DefaultRevision is used when a reference carries no revision.
const DefaultRevision = "main"

// Ref is a parsed model reference.
type Ref struct {
	Owner    string
	Repo     string
	Revision string
	// Path is a single artifact inside the repo; empty means the whole repo.
	Path string
}

var (
	ownerRe   = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9_-]{0,93}[a-zA-Z0-9])?$`)
	repoRe    = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,96}$`)
	revRe     = regexp.MustCompile(`^[a-zA-Z0-9._/-]{1,200}$`)
	pathSegRe = regexp.MustCompile(`^[a-zA-Z0-9._ +()'\-]{1,255}$`)
)

// Parse parses a model reference string.
func Parse(s string) (*Ref, error) {
	rest, ok := strings.CutPrefix(s, "hf://")
	if !ok {
		rest, ok = strings.CutPrefix(s, "hf:")
	}
	if !ok {
		return nil, fmt.Errorf("reference must start with hf: or hf://, got %q", s)
	}

	body, path := rest, ""
	if before, after, found := strings.Cut(rest, "#"); found {
		body, path = before, after
	}
	body, revision := body, DefaultRevision
	if before, after, found := strings.Cut(body, "@"); found {
		body, revision = before, after
	}

	owner, repo, found := strings.Cut(body, "/")
	if !found {
		return nil, fmt.Errorf("reference must be owner/repo, got %q", body)
	}
	r := &Ref{Owner: owner, Repo: repo, Revision: revision, Path: path}
	if err := r.validate(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Ref) validate() error {
	if !ownerRe.MatchString(r.Owner) {
		return fmt.Errorf("invalid owner %q", r.Owner)
	}
	if !repoRe.MatchString(r.Repo) {
		return fmt.Errorf("invalid repo %q", r.Repo)
	}
	if !revRe.MatchString(r.Revision) {
		return fmt.Errorf("invalid revision %q", r.Revision)
	}
	if strings.Contains(r.Revision, "..") {
		return fmt.Errorf("invalid revision %q", r.Revision)
	}
	if r.Path == "" {
		return nil
	}
	if strings.HasPrefix(r.Path, "/") {
		return fmt.Errorf("artifact path must be relative, got %q", r.Path)
	}
	for _, seg := range strings.Split(r.Path, "/") {
		if seg == "" || seg == "." || seg == ".." || !pathSegRe.MatchString(seg) {
			return fmt.Errorf("invalid artifact path %q", r.Path)
		}
	}
	return nil
}

// ValidModelID reports whether id is a well-formed "owner/repo" pair as
// produced by Ref.ID. Callers persisting model ids on disk (store paths)
// must check this defensively.
func ValidModelID(id string) bool {
	owner, repo, ok := strings.Cut(id, "/")
	if !ok || strings.Contains(repo, "/") {
		return false
	}
	return ownerRe.MatchString(owner) && repoRe.MatchString(repo)
}

// ID returns the model identifier "owner/repo".
func (r *Ref) ID() string {
	return r.Owner + "/" + r.Repo
}

// String returns the canonical form of the reference.
func (r *Ref) String() string {
	s := "hf:" + r.ID()
	if r.Revision != DefaultRevision {
		s += "@" + r.Revision
	}
	if r.Path != "" {
		s += "#" + r.Path
	}
	return s
}
