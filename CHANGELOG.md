# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/log0u7/llmp2p/compare/v0.0.0...HEAD
[0.0.0]: https://github.com/log0u7/llmp2p/releases/tag/v0.0.0
