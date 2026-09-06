# Roadmap

llmp2p follows SemVer: the `0.x` series makes no stability promises. Anything below
is intent, not commitment.

## v0.1 - distribution hardening (shipped)

- [x] `llmp2p remove` / `llmp2p verify`: store lifecycle commands.
- [x] Pull progress display; transparent re-pull on corrupted cache.
- [x] Platform-appropriate store dirs (XDG / Library / LOCALAPPDATA) and
      prebuilt binaries attached to releases (linux, macos, windows).
- [x] Service units: systemd, launchd, NSSM ([deploy/](deploy/)).
- [x] `llmp2pd pull` delegation: the daemon owns downloads, the CLI is a thin
      client with auto-detection (removes store lock contention).
- [x] Signed manifests (ed25519 sidecars) with signer allowlists.
- [x] Metrics endpoint for the daemon (`/metrics`, Prometheus text format).
- [x] Security: govulncheck CI job, Dependabot, SECURITY.md, actions pinned
      to commit SHAs, main branch protection.

## v0.2 - decentralized discovery

- [x] BEP 44 mutable DHT records for manifest discovery (ADR-0008, shipped
      as an additional `pull --dht` origin; the HTTPS bootstrap index stays
      the default). Prototype findings that shaped the implementation: the
      record expiration must be explicit (a zero Exp expires every record
      instantly), get replies are parsed from raw bencode (the library
      response decoder loses dict/string shaped values), and the put query
      must carry the salt alongside the signature.
- [x] Swarm-distributed manifest bytes: manifests up to the BEP 44 record
      cap (700 bytes of canonical JSON) are embedded in the record itself,
      digest-checked on decode; v0.3 `--swarm-key` builds a fully
      origin-free private discovery path on top.
- [x] Private swarm mode (v0.3, `--swarm-key <hex|@file>`): DHT-only
      discovery under a shared publisher key without the bootstrap index,
      Hub fallback disabled.

## v0.2 - ecosystem

- [ ] llama.cpp adapter: serve pulled GGUFs directly (llama-server) or drop them in
      llama.cpp expected layouts.
- [ ] OpenAI-compatible proxy in front of llama.cpp for pulled models.
- [ ] Split-shard GGUF support aligned with upstream Ollama support.
- [ ] Progress bars (rich TUI) instead of log lines.

## v0.3+ - beyond GGUF

- [ ] Safetensors, LoRA adapters, tokenizers, embeddings, diffusion models (VAE,
      UNet), datasets: the manifest and swarm logic are artifact-agnostic already.
- [ ] Private swarm mode (shared secret / closed tracker) for teams.
- [ ] Web UI for the daemon.

## Non-goals

- Being a Hub replacement: llmp2p is a distribution layer, discovery stays on the
  Hub and in the index.
- Seeding arbitrarily from strangers without verification: every byte is checked
  against the manifest chain; see docs/explanation/trust-model.md.
