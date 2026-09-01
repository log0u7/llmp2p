# 6. Ollama adapter via CLI invocation, not blob mutation

* Status: accepted
* Date: 2026-09-01
* Deciders: log0u7

## Context and problem statement

Users want pulled GGUFs available in Ollama. How should llmp2p integrate?

## Decision drivers

- Ollama's on-disk blob layout is an implementation detail, version-dependent
- Must not corrupt a user's existing Ollama installation
- Forking Ollama would fork its release cadence forever

## Considered options

* **`ollama create` with a generated Modelfile (chosen)** - stable, documented
  CLI surface; `FROM <absolute gguf path>`; host passthrough via OLLAMA_HOST
* **Write blobs directly into ~/.ollama/models** - fast but couples us to
  Ollama internals and can corrupt the store
* **Fork Ollama with P2P-aware pull** - the "obvious" idea from blog posts;
  turns every upstream release into a merge conflict
* **Expose an OpenAI-compatible server ourselves** - useful (roadmap) but does
  not answer "my model in Ollama" and duplicates a working runtime

## Decision outcome

Adapter via CLI.

* Good, because zero coupling to Ollama internals: any Ollama version works
* Good, because a fork would have to chase upstream forever; the adapter is
  200 lines and version-proof
* Bad, because requires the `ollama` binary in PATH (or `--ollama`)
* Bad, because split-shard GGUFs cannot be imported (Ollama limitation) -
  rejected explicitly with an actionable error instead of silently

## Links

* docs/how-to/import-into-ollama.md
