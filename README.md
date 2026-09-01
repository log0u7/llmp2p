# llmp2p

[![CI](https://github.com/log0u7/llmp2p/actions/workflows/ci.yml/badge.svg)](https://github.com/log0u7/llmp2p/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/log0u7/llmp2p?sort=semver)](https://github.com/log0u7/llmp2p/releases)
[![Go](https://img.shields.io/badge/go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev/doc/devel/release)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

P2P distribution of LLM model artifacts: pull Hugging Face model repositories
through a BitTorrent swarm when peers exist, fall back to HTTPS from the Hub
when they do not, and keep your models seeding for everyone else.

## Why

GGUF models are 1-100+ GB files served over plain HTTP: every download hits the
Hub again. llmp2p treats models as what they are, large immutable blobs that a
swarm serves better than a single origin. The more popular a model, the faster
it distributes.

**Good fit**: sharing GGUF repos, homelabs with several machines, sparing the
Hub. **Bad fit**: first pull of a model nobody seeds.

## Quickstart

Install the CLI and the daemon (Go 1.25+; the `go.mod` pins the toolchain,
`GOTOOLCHAIN=auto` handles it):

```sh
go install github.com/log0u7/llmp2p/cmd/llmp2p@latest
go install github.com/log0u7/llmp2p/cmd/llmp2pd@latest
```

Then pull, import, share:

```sh
llmp2p pull hf:Qwen/Qwen2.5-0.5B-Instruct-GGUF
llmp2p import hf:Qwen/Qwen2.5-0.5B-Instruct-GGUF --name qwen2.5-0.5b
ollama run qwen2.5-0.5b

llmp2pd
curl -s localhost:8347/api/v1/status | jq
```

Expected result after `pull`: a `manifest <sha256>` / `infohash <40hex>` line
pair, and `llmp2p list` showing the model with its pinned revision.

### Alternative: build from source

Instead of `go install`, build from a clone.
[mise](https://mise.jdx.dev) pins the whole dev environment (`go`,
`golangci-lint`, `gitleaks`) in [mise.toml](mise.toml):

```sh
git clone https://github.com/log0u7/llmp2p.git && cd llmp2p
mise install      # first task run may ask for `mise trust`
make build        # bin/llmp2p + bin/llmp2pd
```

## How a pull works

```mermaid
flowchart TD
    A["llmp2p pull hf:owner/repo"] --> B["Resolve on the Hub<br>pin revision, list files, LFS sha256"]
    B --> C{"Local files already match<br>the pinned revision?"}
    C -->|yes| Z["cache hit"]
    C -->|no| D{"Bootstrap index entry<br>for this revision?"}
    D -->|yes| E["Join the swarm<br>BitTorrent DHT, BEP 9 metadata"]
    E -->|"no data within grace"| F
    D -->|no| F["HTTPS download from the Hub<br>resume, sha256"]
    E --> G["Final per-file<br>sha256 verification"]
    F --> G
    G --> H[("Store")]
    H --> I["llmp2pd keeps it seeding"]
```

Every byte is verified twice: against torrent piece hashes during transfer,
then against the manifest's per-file sha256 (Hub LFS oid).

## Commands

| Command | Purpose |
|---|---|
| `llmp2p pull <ref>` | pull a repo: swarm, then HTTP fallback |
| `llmp2p seed <ref\|.torrent>` | seed until interrupted |
| `llmp2p import <ref\|.gguf>` | register the GGUF in Ollama |
| `llmp2p list` | show stored models |
| `llmp2p verify <ref>` | re-verify a stored model's integrity |
| `llmp2p remove <ref>` | remove a model from the store |
| `llmp2pd` | background seeder + status API on `127.0.0.1:8347` |

Full grammar and flags: [docs/reference/cli.md](docs/reference/cli.md) ·
daemon API: [docs/reference/daemon-api.md](docs/reference/daemon-api.md).

## Make a model discoverable

Every pull publishes to your local index. To share with everyone, open a PR
adding the entry (and its manifest) to this repository's
[index.json](index.json): [docs/how-to/contribute-index-entry.md](docs/how-to/contribute-index-entry.md).

## Documentation

Organized as [Diátaxis](https://diataxis.fr/): start at
[docs/README.md](docs/README.md).

| I want to... | Read |
|---|---|
| learn hands-on, start to finish | [tutorials/get-started.md](docs/tutorials/get-started.md) |
| solve a specific task | [docs/how-to/](docs/how-to/) |
| look up a command, flag, or schema | [docs/reference/](docs/reference/) |
| understand how and why it works | [docs/explanation/](docs/explanation/) (architecture, trust model, protocol) |
| know why the code looks this way | [docs/adr/](docs/adr/) (7 MADR decision records) |

## Status

v0.0.0, experimental: single-file GGUF repos work end to end, the protocol may
still change before 1.0. See [ROADMAP.md](ROADMAP.md).

## Contributing

[CONTRIBUTING.md](CONTRIBUTING.md): Conventional Commits, atomic changes,
gitleaks pre-commit hook, tests required. Security issues: see
[docs/explanation/trust-model.md](docs/explanation/trust-model.md) for what is
protected before opening an issue.

## License

[MIT](LICENSE)
