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

- [ ] BEP 44 mutable DHT records for manifest publication (removes the HTTPS-only
      bootstrap path; see ADR-0004). First wire-level prototype exists; findings
      so far: the record expiration must be explicit (a zero Exp expires every
      record instantly), get replies must be parsed from raw bencode (the
      library response decoder loses dict/string shaped values), and the put
      query must carry the salt alongside the signature.
- [ ] Private swarm mode (shared keys for DHT-only discovery without the
      bootstrap index).

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
