# Pull, import, and share your first model

In this tutorial you will pull a small GGUF model into the llmp2p store, register
it in Ollama, and start sharing it with the swarm. You need Go 1.25+ (or any Go
with `GOTOOLCHAIN=auto`) and, for the import step, Ollama installed.

Every step prints something you can check before moving on.

## 1. Build the tools

```sh
git clone https://github.com/log0u7/llmp2p.git && cd llmp2p
make build
```

Check: `./bin/llmp2p --version` prints `llmp2p version 0.0.0`.

## 2. Pull a small model

```sh
./bin/llmp2p pull hf:HuggingFaceTB/SmolLM2-135M-Instruct-GGUF
```

The first pull downloads over HTTPS from the Hub (there is no swarm yet),
generates a manifest, and saves everything in the store.

Check: the output ends with `manifest <sha256>` and `infohash <40 hex>` lines.
`./bin/llmp2p list` shows the model with `@cafe...`-style revision and its size.

## 3. Verify the store layout

```sh
tree ~/.local/share/llmp2p
```

Check: you see `store/HuggingFaceTB/SmolLM2-135M-Instruct-GGUF/` with the GGUF
and its config, a `manifests/` directory with one content-addressed JSON, a
`torrents/` directory with the `.torrent`, and a local `index.json` that now
contains the model entry.

## 4. Import into Ollama

```sh
./bin/llmp2p import hf:HuggingFaceTB/SmolLM2-135M-Instruct-GGUF --name smollm2
```

Check: `ollama run smollm2` answers.

No Ollama? Skip this step; the pulled GGUF also works with llama.cpp directly
from the store path shown by `llmp2p list`.

## 5. Keep sharing

```sh
./bin/llmp2pd
```

Check: the log prints `llmp2pd listening addr=127.0.0.1:8347` and
`seeding model=...`. In another terminal:

```sh
curl -s localhost:8347/api/v1/status
```

The `torrents` count is 1. Stop the daemon with Ctrl-C when done.

## 6. (Optional) simulate a second peer

On the same machine, pull the same model into a second store while the daemon
seeds the first one:

```sh
mkdir -p /tmp/llmp2p-second-store
./bin/llmp2p pull hf:HuggingFaceTB/SmolLM2-135M-Instruct-GGUF \
  --dir /tmp/llmp2p-second-store \
  --bootstrap http://127.0.0.1:8347 # not yet: v0.1 serves the index over the daemon API
```

Note: in v0.0.0 the daemon does not serve the index; to reproduce a P2P pull
locally you need a bootstrap origin serving your `index.json` and `manifests/`.
See docs/explanation/protocol.md for the exact layout. On a real deployment,
once the model entry is merged into the public index, second pullers join the
swarm automatically.

## What you learned

- `pull` transparently falls back from P2P to HTTPS, and always verifies sha256.
- Everything is content-addressed: manifest, torrent, and files.
- `llmp2pd` turns your machine into swarm infrastructure.

Next: [seed-a-model.md](../how-to/seed-a-model.md) and
[trust-model.md](../explanation/trust-model.md) to understand what you just
verified.
