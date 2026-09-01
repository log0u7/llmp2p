# llmp2p

P2P distribution of LLM model artifacts. Pull Hugging Face model repositories through a
BitTorrent swarm when peers exist, fall back to HTTPS from the Hub when they do not,
and keep your models seeding for everyone else.

CI badge placeholder: see `.github/workflows/ci.yml` (vet, race tests, golangci-lint, gitleaks).

## Why

GGUF models are 1-100+ GB files served over plain HTTP. Every download hits the Hub
again. llmp2p treats models as what they are: large, immutable, content-addressed
blobs that a swarm can serve far better than a single origin. The more popular a
model, the faster it distributes.

## How it works

```
llmp2p pull hf:owner/repo
  |
  | resolve on the Hub (pin revision, list files, LFS sha256)
  |
  | bootstrap index hit?  ── yes ─> join swarm (BitTorrent, DHT)  ─┐
  |        | no                                                    |
  |        v                                                       |
  |  download over HTTPS (resume + sha256)                         |
  |        |                                                       |
  |        v                                                       |
  |  generate manifest + torrent, publish locally, seed            |
  |________________________________________________________________|
  |
  v
  final per-file sha256 verification  ->  store  ->  (optional) ollama import
```

Every byte is verified twice: against torrent piece hashes during transfer, and
against sha256 (Hub LFS oid) at the end. Details in
[docs/explanation/trust-model.md](docs/explanation/trust-model.md).

## Install

```sh
go install github.com/log0u7/llmp2p/cmd/llmp2p@latest
go install github.com/log0u7/llmp2p/cmd/llmp2pd@latest
```

Requires Go 1.25+ (the go.mod pins the exact toolchain; `GOTOOLCHAIN=auto` handles it).

## Quickstart

```sh
# Pull a small model (P2P if a swarm exists, HTTPS otherwise)
llmp2p pull hf:Qwen/Qwen2.5-0.5B-Instruct-GGUF

# Import it into Ollama
llmp2p import hf:Qwen/Qwen2.5-0.5B-Instruct-GGUF --name qwen2.5-0.5b

# Keep sharing: background seeder + local status API
llmp2pd
curl -s localhost:8347/api/v1/status | jq
```

New models you pull are automatically published to your local index
(`index.json` in the store). To make a model discoverable to everyone, open a PR
adding its entry to this repository's [index.json](index.json): see
[docs/how-to/contribute-index-entry.md](docs/how-to/contribute-index-entry.md).

## Commands

| Command | Purpose |
|---|---|
| `llmp2p pull <ref>` | pull a repo (P2P, then HTTP fallback) |
| `llmp2p seed <ref\|.torrent>` | seed until interrupted |
| `llmp2p import <ref\|.gguf>` | register the GGUF in Ollama |
| `llmp2p list` | show stored models |
| `llmp2pd` | background seeder + status API on `127.0.0.1:8347` |

Reference: [docs/reference/cli.md](docs/reference/cli.md) and
[docs/reference/daemon-api.md](docs/reference/daemon-api.md).

## Documentation

Organized as [Diátaxis](https://diataxis.fr/): see [docs/README.md](docs/README.md).

- Learn: [docs/tutorials/get-started.md](docs/tutorials/get-started.md)
- Tasks: [docs/how-to/](docs/how-to/)
- Facts: [docs/reference/](docs/reference/)
- Understanding: [docs/explanation/](docs/explanation/) (architecture, trust model, protocol)
- Decisions: [docs/adr/](docs/adr/) (MADR records)

## Status

v0.0.0: experimental. Single-file GGUF repos work end to end; expect protocol
changes before 1.0. See [ROADMAP.md](ROADMAP.md).

## License

[MIT](LICENSE)
