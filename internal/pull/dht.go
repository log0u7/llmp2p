package pull

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"

	anadht "github.com/anacrolix/dht/v2"

	"github.com/log0u7/llmp2p/internal/dht"
	"github.com/log0u7/llmp2p/internal/index"
	"github.com/log0u7/llmp2p/internal/signing"
)

// dhtNodes resolves the DHT nodes to query or publish to: explicit test /
// private-swarm addresses when set, the public routers otherwise.
func dhtNodes(opts Options) ([]anadht.Addr, error) {
	if len(opts.DHTAddrs) > 0 {
		var out []anadht.Addr
		for _, a := range opts.DHTAddrs {
			ua, err := net.ResolveUDPAddr("udp", a)
			if err != nil {
				continue
			}
			out = append(out, anadht.NewAddr(ua))
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("dht: no usable node address in %v", opts.DHTAddrs)
		}
		return out, nil
	}
	return anadht.GlobalBootstrapAddrs("udp")
}

// discoverViaDHT resolves the swarm entry for a model from BEP 44 mutable
// records. Only records signed by an allowlisted publisher key can be
// found: the target is derived from (key, model id). The returned
// manifest bytes are nil for pointer-only records; when present they were
// digest-checked against the record and signed by the allowlisted
// publisher, and the caller can skip HTTPS origins entirely.
func discoverViaDHT(ctx context.Context, opts Options, modelID, revision string) (index.Entry, []byte, error) {
	client, err := dht.NewClient(opts.AllowedSigners)
	if err != nil {
		return index.Entry{}, nil, err
	}
	conn, err := dht.ListenUDP()
	if err != nil {
		return index.Entry{}, nil, fmt.Errorf("dht: listen: %w", err)
	}
	defer func() { _ = conn.Close() }()
	srv, err := dht.NewServer(conn)
	if err != nil {
		return index.Entry{}, nil, fmt.Errorf("dht: server: %w", err)
	}
	defer srv.Close()

	nodes, err := dhtNodes(opts)
	if err != nil {
		return index.Entry{}, nil, err
	}
	var lastErr error
	for _, node := range nodes {
		rec, err := client.Get(ctx, srv, node, modelID)
		if err != nil {
			lastErr = err
			continue
		}
		if rec.Revision != revision {
			lastErr = fmt.Errorf("dht: record pinned to revision %s, want %s", rec.Revision, revision)
			continue
		}
		return index.Entry{
			Model:          modelID,
			InfoHash:       hex.EncodeToString(rec.InfoHash),
			ManifestSHA256: hex.EncodeToString(rec.ManifestSHA256),
			Revision:       rec.Revision,
			Size:           rec.Size,
		}, rec.Manifest, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("dht: no reachable node")
	}
	return index.Entry{}, nil, lastErr
}

// publishToDHT stores a signed record for a freshly published model. It
// is best-effort: failures are logged and never fail the pull. Requires
// the publisher key created by `llmp2p keygen`. The canonical manifest
// bytes are embedded in the record when they fit under the BEP 44 cap;
// otherwise the record stays pointer-only.
func publishToDHT(opts Options, modelID string, infoHash, msha, revision string, size int64, manifestBytes []byte) {
	keyPath := filepath.Join(opts.Store.Root(), signing.DefaultKeyFile)
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		logf(opts.Log, "dht publish skipped: no publisher key (run llmp2p keygen)")
		return
	}
	priv, _, err := signing.LoadOrCreate(keyPath)
	if err != nil {
		logf(opts.Log, "dht publish skipped: unusable key", "err", err)
		return
	}
	ih, err := hex.DecodeString(infoHash)
	if err != nil || len(ih) != 20 {
		logf(opts.Log, "dht publish skipped: bad infohash", "infoHash", infoHash)
		return
	}
	sha, err := hex.DecodeString(msha)
	if err != nil || len(sha) != 32 {
		logf(opts.Log, "dht publish skipped: bad manifest sha256", "sha", msha)
		return
	}
	if len(manifestBytes) > dht.MaxEmbeddedManifest {
		manifestBytes = nil
	}

	conn, err := dht.ListenUDP()
	if err != nil {
		logf(opts.Log, "dht publish failed", "err", err)
		return
	}
	defer func() { _ = conn.Close() }()
	srv, err := dht.NewServer(conn)
	if err != nil {
		logf(opts.Log, "dht publish failed", "err", err)
		return
	}
	defer srv.Close()
	nodes, err := dhtNodes(opts)
	if err != nil {
		logf(opts.Log, "dht publish failed", "err", err)
		return
	}

	pub := dht.NewPublisher(priv, filepath.Join(opts.Store.Root(), "dht-seq.json"))
	rec := dht.Record{InfoHash: ih, ManifestSHA256: sha, Revision: revision, Size: size, Manifest: manifestBytes}
	var lastErr error
	for _, node := range nodes {
		ctx, cancel := context.WithTimeout(context.Background(), dhtPublishTimeout)
		if err := pub.Put(ctx, srv, node, modelID, rec); err != nil {
			lastErr = err
			cancel()
			continue
		}
		cancel()
		logf(opts.Log, "dht record published", "model", modelID)
		return
	}
	if lastErr != nil {
		logf(opts.Log, "dht publish failed", "err", lastErr)
	}
}
