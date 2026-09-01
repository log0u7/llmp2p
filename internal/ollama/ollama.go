// Package ollama imports downloaded GGUF models into Ollama through the
// ollama CLI. Mutating Ollama's blob storage directly is version-dependent
// and unsupported: `ollama create` with a generated Modelfile is the
// stable interface.
package ollama

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Bin is the default ollama binary name.
const Bin = "ollama"

// DefaultName derives an Ollama-compatible model name from a repo id.
func DefaultName(modelID string) string {
	repo := modelID
	if _, r, ok := strings.Cut(modelID, "/"); ok {
		repo = r
	}
	repo = strings.ToLower(repo)
	repo = regexp.MustCompile(`[^a-z0-9._-]+`).ReplaceAllString(repo, "-")
	repo = strings.Trim(repo, "-.")
	if repo == "" {
		repo = "llmp2p-model"
	}
	return repo
}

// FindGGUF returns the single GGUF file of a model directory. Split GGUF
// shards (-00001-of-00002.gguf) are rejected: Ollama cannot import them.
func FindGGUF(modelDir string) (string, error) {
	var ggufs, shards []string
	err := filepath.Walk(modelDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if strings.HasSuffix(strings.ToLower(info.Name()), ".gguf") {
			if shardRe.MatchString(info.Name()) {
				shards = append(shards, path)
			} else {
				ggufs = append(ggufs, path)
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(shards) > 0 {
		return "", fmt.Errorf("ollama: split GGUF is not importable (%d shards like %s); use a single-file GGUF repo",
			len(shards), filepath.Base(shards[0]))
	}
	switch len(ggufs) {
	case 1:
		return ggufs[0], nil
	case 0:
		return "", errors.New("ollama: no .gguf file found in model directory")
	default:
		return "", fmt.Errorf("ollama: %d GGUF files found, expected one", len(ggufs))
	}
}

var shardRe = regexp.MustCompile(`-\d{5}-of-\d{5}\.gguf$`)

// Create imports ggufPath into Ollama under name. host, when non-empty,
// is passed via OLLAMA_HOST. Output is streamed to w.
func Create(ctx context.Context, ollamaBin, ggufPath, name, host string, w io.Writer) error {
	info, err := os.Stat(ggufPath)
	if err != nil {
		return fmt.Errorf("ollama: %w", err)
	}
	if info.IsDir() || info.Size() == 0 {
		return fmt.Errorf("ollama: %s is not a GGUF file", ggufPath)
	}

	tmp, err := os.CreateTemp("", "llmp2p-Modelfile-*")
	if err != nil {
		return err
	}
	modelfile := tmp.Name()
	defer func() { _ = os.Remove(modelfile) }()
	if _, err := fmt.Fprintf(tmp, "FROM %s\n", ggufPath); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	bin := ollamaBin
	if bin == "" {
		bin = Bin
	}
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("ollama: %s not found in PATH: %w", bin, err)
	}
	cmd := exec.CommandContext(ctx, bin, "create", name, "-f", modelfile)
	if host != "" {
		cmd.Env = append(os.Environ(), "OLLAMA_HOST="+host)
	}
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ollama: create %s: %w", name, err)
	}
	return nil
}
