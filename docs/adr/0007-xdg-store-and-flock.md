# 7. XDG store layout with exclusive flock

* Status: accepted
* Date: 2026-09-01
* Deciders: log0u7

## Context and problem statement

Where do manifests, torrents, and model files live, and who may touch them at
the same time? Concurrent CLI + daemon access must not corrupt state.

## Decision drivers

- Follow platform conventions (XDG on Linux; the main target)
- Content-addressed subdirs (manifests by sha256, torrents by infohash)
- Simple and auditable concurrency story in v0
- Model files must land exactly as laid out in the repo (for Ollama/llama.cpp)

## Considered options

* **XDG data dir + flock (chosen)** - `~/.local/share/llmp2p/...`, one
  exclusive lock file serializing all engines
* **Per-model locks** - finer-grained, but manifest/torrent writes and index
  updates share state; locking each correctly is real complexity for v0
* **SQLite state database** - robust, but adds a dependency and an opaque store
  for what is fundamentally a file tree
* **System temp/cache dirs** - wrong lifetime semantics for 100 GB artifacts

## Decision outcome

XDG layout, single exclusive flock.

* Good, because `store/<owner>/<repo>/` mirrors the Hub layout: other tools
  (llama.cpp) consume it directly
* Good, because content addressing makes every subdir self-describing and
  garbage-collectable later
* Bad, because the daemon waits for CLI commands (and vice versa) instead of
  running concurrently; v0.1 moves pulling into the daemon to remove the wait
* Bad, because torrent data lands under `<owner>/<repo>` via the torrent root
  name being the repo basename: a constraint the manifest builder enforces
  (ADR-0002) and tests pin

## Links

* docs/reference/config.md (layout)
