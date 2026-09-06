# Configuration reference

llmp2p is flag-and-environment driven in v0; there is no config file yet.

## Environment

| Variable | Used by | Meaning |
|---|---|---|
| `HF_TOKEN` | `pull` | Hugging Face access token when `--token` is absent |
| `HF_ENDPOINT` | all | Hub mirror override (e.g. `https://hf-mirror.com`) |
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
manifests/<sha256>.json.sig     signature sidecars (publishers with a key)
torrents/<infohash>.torrent     generated torrents
index.json                      local index (auto-published entries)
dht-seq.json                    BEP 44 sequence counters (publishers with a key)
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

## DHT discovery and publication (v0.2)

`pull --dht --allowed-signers <hex keys>` resolves the swarm entry from BEP 44
mutable records (see ADR-0005) before consulting the bootstrap origins; the
manifest bytes keep flowing over HTTPS. Publication happens automatically
after every whole-repo pull when a publisher key exists (`llmp2p keygen`) and
`--dht` is set: it is best-effort, failures are logged and never fail the pull.
Without explicit test addresses, the public mainline routers are used.
