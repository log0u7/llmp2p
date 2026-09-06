# Run a private swarm

Share models with a closed group (homelab, team) without the public bootstrap
index: discovery happens through BEP 44 records signed by one publisher key,
and the pull never falls back to the Hub.

## What you need

- One **publisher** who owns the key: `llmp2p keygen` creates it in the store
  and prints the public key.
- Every puller needs the **public key** (64 hex chars): share it out-of-band
  (password manager, SSH, printed paper), never the private key.
- Small models only for a fully origin-free flow: the manifest must fit the
  DHT record embed cap (700 bytes of canonical JSON), i.e. typical repos with
  a handful of files. Bigger manifests keep needing an HTTPS origin.

## 1. Publisher: pull with `--dht`

```sh
llmp2p keygen                     # once; prints "public key: <64 hex>"
llmp2p pull hf:org/private-model --dht
```

The pull publishes a signed record (infohash, manifest digest, revision,
size, and the manifest itself when it fits) addressed by your key. Publish
again after every update: mutable records hold the current revision.

## 2. Puller: share the key, then pull

```sh
echo "3d0c9e...publisher-pubkey..." > swarm.pub
llmp2p pull hf:org/private-model --swarm-key @swarm.pub
```

`--swarm-key` accepts the hex inline or `@file`. It implies:

- `--dht --allowed-signers <key>`: only records signed by the swarm key are
  addressable;
- bootstrap origins are ignored;
- the Hub download fallback is disabled: a pull either runs on the trusted
  path or fails loudly.

## Limits

- **Current revision only**: a mutable record is overwritten on update;
  pinned older revisions are not discoverable via DHT.
- **Torrent peer discovery** still uses the public mainline DHT unless you
  inject peers; the record's privacy covers discovery of *what to download*,
  not the swarm handshake.
- **Big manifests**: when the record stays pointer-only and no origin serves
  the manifest, the pull fails with "HTTP fallback is disabled". Serve the
  manifest over HTTPS or keep the model under the embed cap.

## Why this is safe

The chain is unchanged (see the [trust model](../explanation/trust-model.md)):
the record pins the manifest sha256, the torrent infohash, piece hashes and
the final per-file sha256 all verify as usual. What changes is the trust
anchor: your allowlist instead of a PR-reviewed index, and a publisher gone
rogue is fixed by rotating the swarm key.

Decisions: [ADR-0008](../adr/0008-bep44-dht-discovery.md).
