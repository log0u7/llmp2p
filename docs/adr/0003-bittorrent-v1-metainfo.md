# 3. BitTorrent v1 metainfo, not v2 (BEP 52) yet

* Status: accepted
* Date: 2026-09-01
* Deciders: log0u7

## Context and problem statement

The manifest idea ("SHA256/Merkle chunks") maps naturally to BitTorrent v2,
which uses SHA-256 merkle trees per file. We must choose the metainfo version
for swarms.

## Decision drivers

- Transfer-level integrity is the only job of piece hashes; end-to-end
  verification is sha256 at the manifest layer either way
- Compatibility with the widest set of clients and tooling
- Library support for *creating* v2 torrents programmatically
- Determinism of infohash derivation

## Considered options

* **v1 (BEP 3, SHA-1 pieces)** - universal support, deterministic creation via
  anacrolix `Info.GeneratePieces`
* **v2 (BEP 52, SHA-256 merkle)** - elegant alignment with our hash story,
  but anacrolix/torrent v1.61.0 exposes no creation API for file trees/piece
  layers; hand-rolling BEP 52 risks interop bugs
* **Hybrid v1+v2** - best interop, same creation problem

## Decision outcome

v1 for v0.x.

* Good, because piece hashing is only transfer integrity: any poisoned content
  is caught by the sha256 manifest chain before publication (ADR-0002)
* Good, because BEP 9 metadata exchange works everywhere and magnets stay
  simple (`urn:btih`)
* Bad, because SHA-1 collision resistance is weak in principle; accepted
  because the attacker would still need the content to pass final sha256
* Bad, because we may revisit at larger scale; switching means a manifest
  schema bump (llmp2p/v2) since infohash is part of the manifest

## Links

* ADR-0002 (manifest schema)
* docs/explanation/trust-model.md
