# Why P2P for models

Model weights are the rare artifact that behaves like video games, Linux ISOs,
and scientific datasets: huge, immutable once published, and pulled by many
people at roughly the same time.

## The economics

A 30 GB quantized model that 50,000 people download costs 1.5 PB of egress.
Someone pays for that: the Hub, or mirrors, or you. BitTorrent turns the cost
curve around: every downloader adds upload capacity. The model becomes more
available as it becomes more popular, which is the exact opposite of a
centralized origin under load.

## Why not just `torrent` files?

You could hand-publish .torrents. llmp2p adds the parts that make it a
distribution *protocol* rather than a one-off:

- **Manifests tied to revisions**: a model id maps to a pinned commit sha and
  an exact file set, verified by sha256, not a moving HEAD.
- **Deterministic infohashes**: torrent metadata is derived from the file list
  itself, so independent people who pulled the same repo converge on the same
  swarm without coordination.
- **Hub compatibility**: discovery stays on huggingface.co; llmp2p only changes
  the transport layer, with automatic fallback.
- **Verification chain**: piece hashes plus end-to-end sha256 against
  Hub-published oids.

## Why BitTorrent and not IPFS/libp2p

- The mainline DHT is a Kademlia network that has run at internet scale for two
  decades, with real client diversity.
- libp2p-based content routing (IPFS) would work, but drags in a far larger
  runtime and a different identity model for the same transport benefit here
  (ADR-0001). BEP 44 (storing small records in the mainline DHT) covers the
  manifest publishing need (ADR-0004) without a new network.

## What P2P does not fix

- First pull of an unpopular model: the swarm is empty; HTTPS fallback is the
  intended path, not a failure.
- Availability guarantees: a model nobody seeds eventually goes cold. The Hub
  remains the archive; llmp2p is the distribution accelerator.
