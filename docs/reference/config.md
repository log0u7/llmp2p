# Configuration reference

llmp2p is flag-and-environment driven in v0; there is no config file yet.

## Environment

| Variable | Used by | Meaning |
|---|---|---|
| `HF_TOKEN` | `pull` | Hugging Face access token when `--token` is absent |
| `XDG_DATA_HOME` | all | relocates the store (default `~/.local/share`) |
| `OLLAMA_HOST` | - | not read by llmp2p: pass `--host` explicitly |

## Store layout

Default root per OS:

| OS | Path |
|---|---|
| Linux | `$XDG_DATA_HOME/llmp2p` (`~/.local/share/llmp2p`) |
| macOS | `~/Library/Application Support/llmp2p` |
| Windows | `%LOCALAPPDATA%\llmp2p` |

Override with `--dir` (CLI) or `--dir` (daemon). Inside the root:

```
store/<owner>/<repo>/           model files, as laid out in the repo
  .llmp2p-manifest.json         manifest pointer pinning the local revision
manifests/<sha256>.json         content-addressed manifests
torrents/<infohash>.torrent     generated torrents
index.json                      local index (auto-published entries)
llmp2p.lock                     exclusive engine lock (flock)
```

## Locking

One engine at a time may use the store: `llmp2p pull`, `llmp2p seed`, and
`llmp2pd` all take the exclusive flock. `llmp2pd` waits and retries; CLI
commands fail fast after ~15 s.

## Bootstrap origins

Default: `https://raw.githubusercontent.com/log0u7/llmp2p/main`. Override or
add with `pull --bootstrap <url>` (repeatable, tried in order, first hit wins).
Each origin must serve `index.json` and `manifests/<sha256>.json`.
