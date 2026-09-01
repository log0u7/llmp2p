# Daemon API reference

`llmp2pd` serves a JSON API on `127.0.0.1:8347` (loopback only, no auth, no
TLS: it is a local management surface).

## `GET /api/v1/status`

```json
{
  "version": "0.0.0",
  "uptimeSeconds": 3600,
  "models": 3,
  "torrents": 3,
  "uploadedBytes": 10737418240,
  "seedingEngines": 2
}
```

- `models`: models found in the store.
- `torrents`: torrents currently tracked by the daemon's engines.
- `uploadedBytes`: total uploaded since daemon start.
- `seedingEngines`: one engine per owner directory (data layout).

## `GET /api/v1/models`

Array of model ids: `["org/repo", ...]`.

## `GET /api/v1/torrents`

Array of torrent statuses:

```json
[{
  "name": "repo",
  "infoHash": "40hex",
  "completed": 123,
  "total": 123,
  "peers": 2,
  "complete": true,
  "seeding": true,
  "downloaded": 0,
  "uploaded": 52428800
}]
```

## Errors

Unrouted paths return Go's `404 page not found`. The server never exposes
anything but loopback; binding other interfaces is not supported in v0.
