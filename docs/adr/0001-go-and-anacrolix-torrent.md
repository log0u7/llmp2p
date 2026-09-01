# 1. Go and anacrolix/torrent as the network stack

* Status: accepted
* Date: 2026-09-01
* Deciders: log0u7

## Context and problem statement

llmp2p needs a BitTorrent client with mainline DHT, metadata exchange (BEP 9),
resume, and seeding, embedded in a cross-platform CLI/daemon. Language and
library choice drive maintenance for the whole project.

## Decision drivers

- Battle-tested networking (DHT at internet scale, NAT traversal realities)
- Pure-Go preferred for cross-compilation and single-binary distribution
- Concurrency-friendly runtime for many simultaneous swarms
- API flexible enough to express our pull/seed orchestration

## Considered options

* **Go + github.com/anacrolix/torrent** - pure Go client+DHT, used as a
  library, storage backends, active maintenance
* **Go + go-libp2p (custom Kademlia + transfer)** - full protocol control, but
  everything is custom and non-interop with BitTorrent
* **Rust + libtorrent bindings** - the most mature C++ client, but FFI,
  cross-compile and memory-safety story cost more than they return here
* **Python + libtorrent** - fast to prototype, poor single-binary story

## Decision outcome

Go + anacrolix/torrent.

* Good, because BitTorrent mainline DHT is a production-grade Kademlia we do
  not have to build or babysit
* Good, because pure Go gives cross-platform single binaries and a sane
  concurrency story for a daemon seeding many models
* Good, because the library exposes the pieces we need (AddMagnet, GotInfo,
  storage backends, custom listen config)
* Bad, because some upstream sharp edges exist (nil rate-limiter panic, config
  defaults required) - documented in engine.go and covered by tests
* Bad, because v2-only torrent features (BEP 52) are not fully exposed by the
  creation API (see ADR-0003)

## Links

* ADR-0003 (BitTorrent v1 vs v2)
