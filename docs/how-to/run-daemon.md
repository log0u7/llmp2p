# Run the daemon (llmp2pd)

You want your models to keep seeding in the background and a way to check on
them.

## Steps

1. Start it:

   ```sh
   llmp2pd
   ```

   It waits for any running `llmp2p` command to finish (the store lock is
   exclusive), then seeds every model it finds and serves the status API on
   `127.0.0.1:8347` (loopback only).

2. Check status:

   ```sh
   curl -s localhost:8347/api/v1/status
   ```

   ```json
   {"version":"0.0.0","uptimeSeconds":120,"models":3,"torrents":3,
    "uploadedBytes":5368709120,"seedingEngines":2}
   ```

3. Other endpoints: `/api/v1/models`, `/api/v1/torrents`. Full reference:
   [reference/daemon-api.md](../reference/daemon-api.md).

4. Stop with Ctrl-C; the API shuts down gracefully and uploads wind down.

## Options

```sh
llmp2pd --addr 127.0.0.1:8347 --dir ~/.local/share/llmp2p
```

## Models not seeding?

- The model must have been pulled with llmp2p (manifest + torrent present).
- A running `llmp2p pull` holds the store lock: the daemon logs
  `waiting for store lock` and takes over when it is free.
- Check `/api/v1/torrents`: `peers` counts connected peers; 0 peers usually
  means the swarm is empty or your port is unreachable (NAT). See
  [seed-a-model.md](seed-a-model.md) for port forwarding.

## Run it as a service (systemd example)

```ini
[Unit]
Description=llmp2p daemon (model swarm seeder)
After=network-online.target

[Service]
ExecStart=%h/go/bin/llmp2pd
Restart=on-failure

[Install]
WantedBy=default.target
```
