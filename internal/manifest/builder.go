package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// Create hashes every entry under root and builds the metainfo, returning
// the completed manifest. Entries must be sorted by path (see Validate).
// The torrent root name is derived from the model id so that two
// independent first pulls always produce the same infohash and therefore
// join the same swarm.
func Create(model, revision string, entries []File, root string) (*Manifest, error) {
	m := &Manifest{
		Schema:      Schema,
		Model:       model,
		Revision:    revision,
		CreatedAt:   time.Now().UTC().Truncate(time.Second),
		PieceLength: PieceLength,
		Files:       make([]File, 0, len(entries)),
	}
	for _, e := range entries {
		p := filepath.Join(root, filepath.FromSlash(e.Path))
		info, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("hash %s: %w", e.Path, err)
		}
		if info.Size() != e.Size {
			return nil, fmt.Errorf("hash %s: size %d, want %d", e.Path, info.Size(), e.Size)
		}
		sum, err := hashFile(p)
		if err != nil {
			return nil, fmt.Errorf("hash %s: %w", e.Path, err)
		}
		m.Files = append(m.Files, File{Path: e.Path, Size: e.Size, SHA256: sum})
	}

	info, err := m.buildInfo(root)
	if err != nil {
		return nil, err
	}
	mi := &metainfo.MetaInfo{InfoBytes: bencode.MustMarshal(info)}
	m.InfoHash = mi.HashInfoBytes().HexString()
	return m, nil
}

// buildInfo constructs the v1 info dict from the manifest files, reading
// content from root to generate piece hashes.
func (m *Manifest) buildInfo(root string) (*metainfo.Info, error) {
	if err := m.validateContent(); err != nil {
		return nil, err
	}
	info := &metainfo.Info{
		PieceLength: m.PieceLength,
		Name:        torrentName(m.Model),
	}
	for _, f := range m.Files {
		info.Files = append(info.Files, metainfo.FileInfo{
			Path:   slashSegments(f.Path),
			Length: f.Size,
		})
	}
	err := info.GeneratePieces(func(fi metainfo.FileInfo) (io.ReadCloser, error) {
		rel := filepath.Join(fi.BestPath()...)
		return os.Open(filepath.Join(root, rel))
	})
	if err != nil {
		return nil, fmt.Errorf("manifest: generate pieces: %w", err)
	}
	return info, nil
}

// MetaInfoBytes renders the .torrent file for m. The resulting infohash is
// asserted equal to m.InfoHash.
func (m *Manifest) MetaInfoBytes(root string) ([]byte, error) {
	info, err := m.buildInfo(root)
	if err != nil {
		return nil, err
	}
	mi := &metainfo.MetaInfo{InfoBytes: bencode.MustMarshal(info)}
	if got := mi.HashInfoBytes().HexString(); got != m.InfoHash {
		return nil, fmt.Errorf("manifest: rebuilt infohash %s, want %s", got, m.InfoHash)
	}
	var buf bytes.Buffer
	if err := mi.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// torrentName maps a model id to the torrent root name: HF repo ids
// contain "/", which is not a valid single path component.
func torrentName(model string) string {
	return strings.ReplaceAll(model, "/", "__")
}

func slashSegments(p string) []string {
	return strings.Split(p, "/")
}

// ErrWrongInfoHash is returned when a downloaded metainfo does not match
// the pinned infohash.
var ErrWrongInfoHash = errors.New("manifest: metainfo infohash mismatch")
