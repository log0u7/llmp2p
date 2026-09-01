# Manifest schema `llmp2p/v1`

The manifest is the bridge between a Hub repository and a BitTorrent swarm. It
is canonical JSON (struct field order, no whitespace) and content-addressed:
its own sha256 pins it.

## Fields

```json
{
  "schema": "llmp2p/v1",
  "model": "owner/repo",
  "revision": "pinned commit sha",
  "createdAt": "2026-09-01T00:00:00Z",
  "pieceLength": 4194304,
  "infoHash": "40 lowercase hex (v1 btih)",
  "files": [
    {"path": "model.gguf", "size": 18765432123, "sha256": "64 lowercase hex"},
    {"path": "config.json", "size": 512, "sha256": "64 lowercase hex"}
  ]
}
```

| Field | Meaning |
|---|---|
| `schema` | format version, exactly `llmp2p/v1` |
| `model` | `owner/repo` identifier |
| `revision` | commit sha the files belong to |
| `createdAt` | generation time, second-truncated UTC |
| `pieceLength` | BitTorrent piece size, fixed at 4 MiB for determinism |
| `infoHash` | sha1 of the bencoded info dict built from these files |
| `files` | every file, sorted strictly by `path` |

## Invariants enforced by validation

- `files` sorted strictly ascending by path (no duplicates).
- every `sha256` is 64 lowercase hex chars; `infoHash` is 40.
- the torrent root name derives from `model` (repo basename): two independent
  first pulls produce the same infohash and join the same swarm.
- `pieceLength` is positive and fixed; changing it changes every infohash.

## Lifecycle

1. **Generated** after an HTTP fallback pull: each file is hashed while landing
   on disk; LFS files must additionally match the Hub's published sha256.
2. **Fetched** on later pulls from a bootstrap origin
   (`<base>/manifests/<manifestSha256>.json`) and checked against the index
   entry (`manifestSha256`, `infoHash`, `revision`).
3. **Rebuilt** to a `.torrent` via the metainfo builder, which asserts the
   rebuilt infohash equals the pinned one before writing anything.

## Trust

The manifest is only as trustworthy as its origin chain; see
[explanation/trust-model.md](../explanation/trust-model.md).
