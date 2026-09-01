# Contribute a model to the public index

You pulled a model with llmp2p and want other people's pulls to join the swarm
instead of hammering the Hub.

The bootstrap index is `index.json` at the root of this repository, fetched by
every client from `https://raw.githubusercontent.com/log0u7/llmp2p/main/`.
Manifests are served next to it under `manifests/<sha256>.json`.

## Steps

1. Pull the model you want to share (this generates the manifest and torrent):

   ```sh
   llmp2p pull hf:owner/repo
   ```

2. Read the entry llmp2p wrote to your local index:

   ```sh
   cat ~/.local/share/llmp2p/index.json
   ```

   ```json
   {"entries":{"owner/repo":{"model":"owner/repo","infoHash":"40hex",
     "manifestSha256":"64hex","revision":"commitsha","size":123,"addedAt":"..."}}}
   ```

3. Fork the repo, copy that entry into the root `index.json`, and copy the
   manifest file `~/.local/share/llmp2p/manifests/<manifestSha256>.json` to
   `manifests/<manifestSha256>.json` in your fork.

4. Open a PR titled `feat(index): add owner/repo`. CI validates JSON shape and
   a maintainer reviews the entry.

5. Once merged, every `llmp2p pull hf:owner/repo` discovers the swarm:
   the client fetches the manifest from the same origin, verifies its sha256
   against the index entry, and joins the BitTorrent swarm keyed by the
   infohash.

## Rules for the public index

- Only content you have the right to distribute (license check first).
- One entry per model id; conflicting manifest hashes for the same id are
  rejected by review (the client also keeps the first origin it trusts).
- Keep the revision pinned: entries are revision-specific by design.

## Why a PR and not the DHT (yet)?

Publishing straight into the mainline DHT (BEP 44) is on the roadmap
(ADR-0004); the PR flow is the v0 trust anchor: HTTPS + code review.
