# 4. HTTPS bootstrap index for discovery; BEP 44 deferred

* Status: accepted
* Date: 2026-09-01
* Deciders: log0u7

## Context and problem statement

Piece hashes must exist before a P2P download can start. The first pull has
them only after an HTTP fallback, so *someone* must serve the manifest to later
pullers. Options for where that manifest lives.

## Decision drivers

- No central server to operate in v0
- Trust anchor must be understandable and auditable
- Eventually fully decentralized (roadmap)
- Client complexity budget for v0

## Considered options

* **HTTPS origins serving index.json + manifests (chosen)** - default origin is
  this repo's raw GitHub; PR-reviewed entries; user-extensible origin list
* **BEP 44 mutable DHT records (model -> infohash)** - decentralized publish,
  but requires per-publisher ed25519 keys, key distribution, spam/availability
  handling, and anacrolix/dht plumbing: too much for v0
* **Central registry service** - simplest to reason about, but a SPOF and an
  ops burden from day one
* **Trust-on-first-use from magnet links only** - no bootstrap story for new
  models at all

## Decision outcome

HTTPS bootstrap origins, BEP 44 deferred to v0.1+.

* Good, because the trust anchor is a git repo with review (same trust model as
  package indexes people already use)
* Good, because fallback HTTP pull + local index publication works with zero
  infrastructure; a model becomes shareable with one PR
* Good, because origins are ordered and mergeable: communities can run mirrors
* Bad, because discovery has a central point (the default origin) until BEP 44
  lands; mitigated by the final sha256 verification chain (ADR-0002)

## Links

* docs/how-to/contribute-index-entry.md
* docs/explanation/protocol.md
