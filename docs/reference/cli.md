# CLI reference

`llmp2p` and `llmp2pd` binaries. Global flag (all `llmp2p` commands):

- `--dir <path>`: model store directory. Default: `$XDG_DATA_HOME/llmp2p`
  (`~/.local/share/llmp2p`).

## `llmp2p pull <ref>`

Pull a model repository: P2P swarm when discoverable, HTTPS from the Hub
otherwise, resume on interruption, sha256 verification always.

Reference grammar: `hf:owner/repo[@revision][#/path/to/file]`.

- `revision`: branch, tag, `refs/pr/N`, or commit sha. Default `main`. The pull
  is pinned to the resolved commit sha and stored against it.
- `#/path`: single artifact (see how-to/pull-a-file.md).

| Flag | Default | Meaning |
|---|---|---|
| `--http-only` | off | skip the index and swarm entirely |
| `--bootstrap <url>` | project index | bootstrap origin(s), repeatable, tried in order |
| `--grace <duration>` | `90s` | wait for swarm data before HTTP fallback |
| `--listen-port <port>` | random | BitTorrent TCP/UDP listen port |
| `--token <token>` | `$HF_TOKEN` | Hub access token (gated repos, rate limits) |
| `--json` | off | machine-readable result on stdout |

Result fields (`--json`): `model`, `revision`, `mode` (`cache`, `p2p`, `http`),
`files`, `size`, `infoHash`, `manifestSha256`.

## `llmp2p seed <ref|.torrent>`

Seed until interrupted. Arg is either a store reference or a path to a
`.torrent` file.

| Flag | Default | Meaning |
|---|---|---|
| `--listen-port <port>` | random | BitTorrent listen port |
| `--data-dir <path>` | cwd | data directory when seeding a `.torrent` file |

## `llmp2p import <ref|.gguf>`

Register a GGUF in Ollama via `ollama create` with a generated Modelfile.

| Flag | Default | Meaning |
|---|---|---|
| `--name <name>` | repo basename | Ollama model name |
| `--host <url>` | local daemon | `OLLAMA_HOST` value |
| `--ollama <path>` | `ollama` | binary path |

## `llmp2p list`

| Flag | Default | Meaning |
|---|---|---|
| `--json` | off | array of `{model, revision, files, size}` |

## `llmp2p verify <ref>`

Re-hashes every file of a stored model against its manifest (streaming
sha256, size checks). Whole models only (`#/path` rejected).

| Flag | Default | Meaning |
|---|---|---|
| `--json` | off | `{model, revision, files, size, ok, error}` |

Exit code is non-zero on the first verification failure.

## `llmp2p remove <ref>`

Delete a model from the store: files, torrent, content-addressed manifest
copy, and local index entry. Whole models only (`#/path` rejected). A size
aware confirmation prompt guards the operation.

| Flag | Default | Meaning |
|---|---|---|
| `--yes` | off | skip the confirmation prompt |
| `--json` | off | `{model, files, size, dirRemoved, torrentRemoved, manifestRemoved, indexEntryRemoved}` |

The store lock applies: a running daemon must be stopped (or finished) first.

## `llmp2p --version`, `--help`

Version is injected at build time (`make build`).

## Exit codes

- `0` success
- `1` any error (message on stderr, prefixed `llmp2p:`)
