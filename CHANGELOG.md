# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `llmp2p remove <ref>`: delete a model from the store (files, torrent,
  manifest, local index entry) with a size-aware confirmation prompt.
- `llmp2p verify <ref>`: re-hash a stored model against its manifest.
- Pull progress: self-rewriting swarm line and a fetched-file counter for the
  HTTP fallback.
- Daemon pull delegation: `POST /api/v1/pulls` job queue; the CLI detects a
  running daemon automatically and polls the job.
- Prometheus metrics: `GET /metrics` on the daemon.
- ed25519 signed manifests: `llmp2p keygen`, signature sidecars, and
  `pull --allowed-signers`.
- Prebuilt binaries attached to GitHub Releases (linux/macos/windows).
- Service units: systemd, launchd, NSSM ([deploy/](deploy/)).
- mise-based dev environment ([mise.toml](mise.toml)).
- CI: govulncheck job, Dependabot, actions pinned to commit SHAs.

### Fixed

- Corrupted cached files now trigger a transparent re-pull instead of a hard
  error.
- Platform-appropriate default store dirs (XDG / Library / LOCALAPPDATA).
- Go toolchain bumped to 1.25.14 and vulnerable modules updated:
  govulncheck reports zero reachable vulnerabilities.

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

[Unreleased]: https://github.com/log0u7/llmp2p/compare/v0.0.1...HEAD
[0.0.1]: https://github.com/log0u7/llmp2p/compare/v0.0.0...v0.0.1
[0.0.0]: https://github.com/log0u7/llmp2p/releases/tag/v0.0.0

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


