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

## `POST /api/v1/pulls`

Delegate a pull to the daemon (202 Accepted, sequential queue):

```json
{"ref": "hf:owner/repo", "httpOnly": false}
```

Response: `{"id": "hex16", "ref": "...", "status": "queued", ...}`.
Whole models only (`#/path` rejected). Requires no other pull to be running
(store lock); the job keeps running after the HTTP call returns.

## `GET /api/v1/pulls/{id}`

Job state: `status` is `queued`, `running`, `succeeded` or `failed`; a
successful job carries the pull `result` (same shape as
`llmp2p pull --json`).

## `GET /api/v1/pulls`

Array of all pull jobs, oldest first.

## `GET /metrics`

Prometheus text exposition (0.0.4): `llmp2pd_uptime_seconds`,
`llmp2pd_models`, `llmp2pd_torrents`, `llmp2pd_peers`,
`llmp2pd_seeding_engines`, `llmp2pd_uploaded_bytes_total`,
`llmp2pd_downloaded_bytes_total`, `llmp2pd_pulls_total{result}`.

## Errors

Unrouted paths return Go's `404 page not found`. The server never exposes
anything but loopback; binding other interfaces is not supported in v0.
