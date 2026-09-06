# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.0] - 2026-09-06

### Added

- Embedded manifests in DHT records: canonical manifest JSON up to the BEP
  44 cap (700 bytes) travels inside the signed record and is digest-checked
  on decode, so `--dht` pulls can run without any HTTPS origin.
- Private swarm mode: `pull --swarm-key <hex|@file>` enables DHT-only
  discovery under the shared publisher key, ignores bootstrap origins and
  disables the Hub fallback (a pull that cannot obtain the manifest fails
  loudly instead of leaking to the Hub).
- `pull.Options.NoHTTPFallback` for library callers mirroring the private
  swarm semantics.
- Audit report: docs/audits/2026-09-06.md (verified findings, fixes, and the
  observations rejected with reasons).

### Fixed

Security and quality audit of v0.2.0 (docs/audits/2026-09-06.md):
- Manifest file paths are validated at parse time and before `Create`
  touches the filesystem: traversal (`..`), absolute, backslash and
  control-character paths are rejected.
- The daemon's `/metrics` renderer no longer reads the live pull-counters
  map while job-completion goroutines mutate it (data race, race-tested).
- `POST /api/v1/pulls` rejects bodies larger than 64 KiB instead of
  buffering unbounded input.
- Delegated pull jobs are bounded by a 2 h timeout: a stalled transfer
  cannot wedge the sequential queue forever.
- `llmp2pd` handles SIGTERM (the systemd default) and shuts down gracefully.
- `llmp2pd` receives the release version via ldflags: `--version` and the
  status endpoint no longer report 0.0.0.

## [0.2.0] - 2026-09-06

### Added

- BEP 44 mutable DHT records for swarm discovery (`pull --dht
  --allowed-signers <keys>`, ADR-0008): the manifest pointer (infohash,
  manifest sha256, revision, size) is a signed record addressed by the
  publisher's ed25519 key, so discovery no longer requires the bootstrap
  index. Manifest bytes keep flowing over HTTPS origins and keep their
  sidecar verification; publication after a pull is best-effort and needs
  `llmp2p keygen`.
- `pull --allowed-signers`: the previously documented but missing flag is
  now real; it drives both manifest sidecar verification and DHT record
  addressing.

## [0.1.1] - 2026-09-06

### Changed

- Test suite hardened: every internal package now sits at or above 80%
  coverage (pull 83%, store 92%, engine 90%, daemon 85%, hf 88%), and the
  CLI gained an end-to-end smoke suite driving the real cobra root against
  temporary stores, fake Hub/daemon servers, and a fake ollama binary.

### Fixed

- hf: the byte count reported by a download that the server answered with a
  full 200 response despite a resume Range request no longer double-counts
  the pre-existing offset.

## [0.1.0] - 2026-09-02

### Added

- Daemon pull delegation: `POST /api/v1/pulls` job queue; the CLI detects a
  running daemon automatically and polls the job.
- Prometheus metrics: `GET /metrics` on the daemon.
- ed25519 signed manifests: `llmp2p keygen`, signature sidecars, and
  `pull --allowed-signers`.

### Fixed

- Pull-job responses encoded the live job while the runner mutated it (caught
  by the race detector); handlers now serve snapshots.

## [0.0.1] - 2026-09-01

### Added

- `llmp2p remove <ref>`: delete a model from the store (files, torrent,
  manifest, local index entry) with a size-aware confirmation prompt.
- `llmp2p verify <ref>`: re-hash a stored model against its manifest, human
  or JSON output.
- Pull progress: self-rewriting swarm line (percent, bytes, peers) and a
  fetched-file counter for the HTTP fallback.
- Prebuilt binaries attached to GitHub Releases: llmp2p + llmp2pd for
  linux/amd64+arm64, darwin/amd64+arm64, windows/amd64.
- Service units: systemd user service, macOS LaunchAgent, NSSM script for
  Windows ([deploy/](deploy/)).
- Platform-appropriate default store dir: XDG on Linux, Application Support
  on macOS, LOCALAPPDATA on Windows.
- mise-based dev environment ([mise.toml](mise.toml)): go, golangci-lint and
  gitleaks pinned to the versions CI uses.
- `govulncheck` CI job, Dependabot (go modules + actions), SECURITY.md,
  .editorconfig, actions pinned to commit SHAs, main branch protected
  against force pushes.

### Changed

- README reworked: badges, mermaid diagrams, GitHub-compatible mermaid
  syntax, mutually exclusive install paths.
- GitHub Release workflow creates releases with generated notes.

### Fixed

- Corrupted cached files now trigger a transparent re-pull instead of a hard
  error.
- Bumped the Go toolchain to 1.25.14 and the vulnerable modules
  (x/net, gorilla/websocket, pion/dtls, pion/stun): govulncheck reports zero
  reachable vulnerabilities.

## [0.0.0] - 2026-09-01

Initial experimental release.

### Added

- `llmp2p pull <hf:owner/repo[@rev][#/path]>`: pull a model repository through a
  BitTorrent swarm with automatic HTTPS fallback to the Hub, resume support, and
  end-to-end sha256 verification.
- `llmp2p seed`: seed a stored model or a standalone `.torrent` until interrupted.
- `llmp2p import`: register a downloaded GGUF into Ollama via a generated Modelfile
  (rejects split-shard GGUF with an explicit error).
- `llmp2p list`: list stored models (human or JSON output).
- `llmp2pd`: background seeder with loopback status API (`/api/v1/status`,
  `/api/v1/models`, `/api/v1/torrents`).
- `llmp2p/v1` content-addressed manifest schema pinning revision, per-file sha256,
  and the v1 torrent infohash.
- Bootstrap index discovery over ordered HTTPS origins with strict entry validation.
- Local index publication: every pulled model becomes discoverable and seedable.
- Documentation set (Diataxis layout) and ADRs (MADR).
- CI: go vet, race tests, golangci-lint, gitleaks; pre-commit gitleaks hook.

[Unreleased]: https://github.com/log0u7/llmp2p/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/log0u7/llmp2p/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/log0u7/llmp2p/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/log0u7/llmp2p/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/log0u7/llmp2p/compare/v0.0.1...v0.1.0
[0.0.1]: https://github.com/log0u7/llmp2p/compare/v0.0.0...v0.0.1
[0.0.0]: https://github.com/log0u7/llmp2p/releases/tag/v0.0.0


