# 5. BEP 44 mutable records as an additional discovery origin

* Status: accepted
* Date: 2026-09-06
* Deciders: log0u7

## Context and problem statement

ADR-0004 shipped HTTPS bootstrap origins as the only swarm discovery path and
deferred BEP 44. The remaining problem: discovery has a central point (the
default origin), and a model becomes swarm-discoverable only after a PR.

## Decision drivers

- Preserve the trust chain end to end (ADR-0002): nothing may be trusted from
  the DHT alone
- The publisher already owns an ed25519 key (`llmp2p keygen`) for manifest
  sidecars
- BEP 44 mutable records cap the value at 1000 bytes: records can only be
  pointers, never model or manifest bytes
- A zero record expiration expires every stored record instantly (anacrolix
  bep44.Wrapper behavior); get replies must be parsed from raw bencode; the
  put must carry the salt alongside the signature (prototype findings)

## Considered options

* **DHT as an additional origin, flag-gated (chosen)** - `pull --dht` resolves
  a signed pointer before consulting the bootstrap index; bytes still come
  from HTTPS origins; publication is best-effort after each whole-repo pull
* **DHT replacing the bootstrap index** - breaks first-pull discovery (no
  peers without a manifest, no manifest without a swarm) and puts the whole
  trust anchor on the DHT
* **Immutable records per revision** - no updates possible, target spam, and
  every revision leaks into the DHT forever

## Decision outcome

DHT discovery is an additional, opt-in origin; the HTTPS bootstrap index
stays the default.

* Good, because the record is signed by the same key whose sidecar signature
  is verified anyway: one allowlist drives both
* Good, because a pull with `--dht --allowed-signers <key>` works with an
  empty bootstrap index: discovery is fully decentralized, byte distribution
  is not (yet)
* Good, because publication is best-effort: a missing key or dead nodes log a
  warning and never fail a pull
* Bad, because manifest bytes still need an HTTPS origin; distributing them
  through the swarm itself is future work
* Bad, because only the current revision of a model is discoverable: a
  mutable record is overwritten, older revisions keep using the index
* Bad, because get is direct-node (bootstrap routers or explicit addresses),
  not a full iterative traversal

## Implementation notes

- Record value: bencode dict `{i: infohash, m: manifest sha256, r: revision,
  s: size}`, ~110 bytes, never model bytes
- Salt: `sha256(model id)` (32 bytes), so the target is
  `SHA1(publisher key + salt)` and one sequence counter exists per
  (publisher, model)
- Sequence counters persist in the store (`dht-seq.json`): a restarted
  publisher cannot replay a stale sequence
- Expiration is always explicit (24h default); get replies are parsed from
  raw bencode by a strict decoder in internal/dht
- The pull allowlist (`--allowed-signers`) drives both the sidecar check and
  the DHT target derivation: records signed by an unknown key are not even
  addressable

## Links

* ADR-0004 (HTTPS bootstrap index, this ADR's predecessor)
* docs/explanation/trust-model.md
* docs/reference/config.md
