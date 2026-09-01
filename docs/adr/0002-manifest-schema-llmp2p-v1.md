# 2. Content-addressed manifest schema llmp2p/v1

* Status: accepted
* Date: 2026-09-01
* Deciders: log0u7

## Context and problem statement

Peers need to agree on what bytes a model consists of before any transfer: the
Hub only publishes whole-file sha256 for LFS files. We need an artifact that
binds model identity (repo + revision) to verifiable content, is small, and can
be discovered and checked without a live Hub.

## Decision drivers

- Cryptographic verification independent of transfer channel
- Determinism: same files must produce the same artifact and the same swarm
- Small enough to serve over HTTPS from a static origin
- Extensible without breaking peers (schema field)

## Considered options

* **Canonical JSON manifest with schema field (llmp2p/v1)** - struct-ordered,
  no-whitespace encoding, self-hashable
* **Store the .torrent itself as the manifest** - BitTorrent-native but
  bencode-opaque, no room for Hub cross-check metadata, harder to evolve
* **ORAS/OCI artifacts** - reuse existing tooling but heavyweight and
  registry-oriented; pull model does not match swarm semantics
* **IPFS CIDs** - content addressing solved, but drags IPFS identity/network

## Decision outcome

Canonical JSON `llmp2p/v1`.

* Good, because the manifest is content-addressed by its own sha256 and
  trivially servable by any static origin
* Good, because files are pinned with sha256 and the infohash is *derived*
  from them: independent pullers converge on one swarm
* Good, because `schema` makes future revisions explicit and non-breaking
* Bad, because a JSON schema needs strict validation code (implemented and
  tested in internal/manifest)

## Links

* docs/reference/manifest-schema.md
