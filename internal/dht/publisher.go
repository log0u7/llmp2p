package dht

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	anadht "github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/bep44"
)

// DefaultRecordExpiration is how long a stored record stays served by our
// own DHT nodes. It must never be zero: the bep44 store wrapper expires
// every record instantly when the expiration is unset.
const DefaultRecordExpiration = 24 * time.Hour

// NewServer binds a DHT server on conn using the upstream defaults, with
// the record expiration made explicit.
func NewServer(conn net.PacketConn) (*anadht.Server, error) {
	cfg := anadht.NewDefaultServerConfig()
	cfg.Conn = conn
	cfg.WaitToReply = true
	if cfg.Exp <= 0 {
		cfg.Exp = DefaultRecordExpiration
	}
	return anadht.NewServer(cfg)
}

// ListenUDP binds a UDP socket suitable for NewServer.
func ListenUDP() (net.PacketConn, error) {
	return net.ListenUDP("udp", &net.UDPAddr{})
}

// seqState persists the per-salt sequence counters. Mutable puts require a
// strictly increasing sequence per target; restarting below a previously
// stored value gets the put rejected by every node holding the record.
type seqState struct {
	mu   sync.Mutex
	path string
	seqs map[string]int64
}

func loadSeqState(path string) *seqState {
	s := &seqState{path: path, seqs: map[string]int64{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &s.seqs)
	}
	return s
}

func (s *seqState) next(salt []byte) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.seqs) == 0 {
		if b, err := os.ReadFile(s.path); err == nil {
			_ = json.Unmarshal(b, &s.seqs)
		}
	}
	key := fmt.Sprintf("%x", salt)
	s.seqs[key]++
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return 0, err
	}
	b, err := json.Marshal(s.seqs)
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(s.path, b, 0o600); err != nil {
		return 0, err
	}
	return s.seqs[key], nil
}

// Publisher signs and stores mutable records under a publisher key.
type Publisher struct {
	priv ed25519.PrivateKey
	seqs *seqState
}

// NewPublisher returns a Publisher persisting sequence counters at
// statePath (created on first put).
func NewPublisher(priv ed25519.PrivateKey, statePath string) *Publisher {
	return &Publisher{priv: priv, seqs: loadSeqState(statePath)}
}

// Put stores rec for modelID on the given node: fetch a write token from
// the node, sign the record with an incremented sequence, put it. The salt
// travels with the signature (a put without its salt produces an invalid
// signature and the wrong target).
func (p *Publisher) Put(ctx context.Context, srv *anadht.Server, node anadht.Addr, modelID string, rec Record) error {
	if len(rec.Manifest) > MaxEmbeddedManifest {
		return fmt.Errorf("dht: embedded manifest is %d bytes, cap is %d", len(rec.Manifest), MaxEmbeddedManifest)
	}
	salt := SaltFor(modelID)
	seq, err := p.seqs.next(salt)
	if err != nil {
		return fmt.Errorf("dht: sequence state: %w", err)
	}
	item, err := bep44.NewItem(rec, salt, seq, 0, p.priv)
	if err != nil {
		return fmt.Errorf("dht: new mutable item: %w", err)
	}

	qr := srv.Get(ctx, node, item.Target(), nil, anadht.QueryRateLimiting{})
	if err := qr.ToError(); err != nil {
		return fmt.Errorf("dht: write token get: %w", err)
	}
	if qr.Reply.R == nil || qr.Reply.R.Token == nil {
		return fmt.Errorf("dht: node returned no write token")
	}

	qr = srv.Put(ctx, node, item.ToPut(), *qr.Reply.R.Token, anadht.QueryRateLimiting{})
	if err := qr.ToError(); err != nil {
		return fmt.Errorf("dht: put: %w", err)
	}
	return nil
}
