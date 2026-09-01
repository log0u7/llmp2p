# Roadmap

llmp2p follows SemVer: the `0.x` series makes no stability promises. Anything below
is intent, not commitment.

## v0.1 - distribution hardening

- [ ] `llmp2pd pull <ref>`: let the daemon own downloads; CLI becomes a thin client
      (removes store lock contention between CLI and daemon).
- [ ] BEP 44 mutable DHT records for manifest publication (removes the HTTPS-only
      bootstrap path; see ADR-0004).
- [ ] Signed manifests (ed25519) and signer allowlists.
- [ ] Metrics endpoint for the daemon (Prometheus format).

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
