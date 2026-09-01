# Seed a model

Your store already contains a model (from `llmp2p pull`) and you want to give
back: upload bytes to peers.

## Seed a stored model

```sh
llmp2p seed hf:HuggingFaceTB/SmolLM2-135M-Instruct-GGUF
```

The command resolves the model manifest in the store, loads the matching
`.torrent`, verifies your data piece by piece, and uploads. Stats print every
10 seconds:

```
SmolLM2-135M-Instruct-Q8_0.gguf peers=3 up=524288000 down=0
```

Stop with Ctrl-C.

## Seed a torrent file you got from elsewhere

```sh
llmp2p seed ~/models/model-Q4_K_M.gguf.torrent --data-dir ~/models
```

`--data-dir` must be the directory that contains the torrent's data directory
(the torrent root name, e.g. `~/models/<repo>/...`).

## Seed everything, all the time

Use [llmp2pd](run-daemon.md): it seeds every stored model automatically and
survives across shells.

## Behind NAT?

Set a fixed port so it can be forwarded:

```sh
llmp2pd --addr 127.0.0.1:8347 # status API
llmp2p seed hf:org/repo --listen-port 51413
```

Forward TCP 51413 to this machine. llmp2p participates in the mainline DHT, so
even without a forwarded port reachable peers still find you.
