# 5. Trust model rooted in Hub-published sha256

* Status: accepted
* Date: 2026-09-01
* Deciders: log0u7

## Context and problem statement

Strangers serve bytes over BitTorrent. We must decide what the root of trust
is, and be explicit about what is and is not protected in v0.

## Decision drivers

- Never expose the user to silently corrupted or poisoned models
- Avoid requiring signatures/key infrastructure in v0
- Reuse an authority that already exists and is hard to forge: the Hub

## Considered options

* **Hub LFS oid as root + manifest chain + final re-hash (chosen)** - every
  file re-verified after transfer; LFS files must equal the Hub-published sha256
* **Signed manifests from day one** - stronger, but requires key management
  and publisher identity we do not have yet
* **Trust the swarm (BitTorrent-native only)** - piece hashes protect transfer,
  not intent; a poisoned manifest with consistent hashes would ship

## Decision outcome

Layered verification with the Hub oid as the outer anchor.

* Good, because a malicious swarm cannot deliver a model that differs from the
  Hub's bytes for LFS-backed files (the common case for GGUFs)
* Good, because the chain is verifiable offline (manifest sha256 + file hashes)
* Good, because nothing is published to the store/index until every check passes
* Bad, because a compromised bootstrap origin plus a compromised Hub check is
  theoretical-but-possible; signatures are the real fix (roadmap v0.1)
* Bad, because small non-LFS files (configs, tokenizers) are pinned only by the
  manifest, not by the Hub

## Links

* docs/explanation/trust-model.md (user-facing contract)
