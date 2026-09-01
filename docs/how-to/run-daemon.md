# Run the daemon (llmp2pd)

You want your models to keep seeding in the background and a way to check on
them.

## Where the store lives

`llmp2pd` seeds whatever `llmp2p pull` stored. The default store root follows
platform conventions (override with `--dir`):

| OS | Path |
|---|---|
| Linux | `~/.local/share/llmp2p` (or `$XDG_DATA_HOME/llmp2p`) |
| macOS | `~/Library/Application Support/llmp2p` |
| Windows | `%LOCALAPPDATA%\llmp2p` |

## Run it (foreground)

```sh
llmp2pd [--dir <path>] [--addr 127.0.0.1:8347]
```

It waits for any running `llmp2p` command to finish (the store lock is
exclusive), then seeds every model it finds and serves the status API on
`127.0.0.1:8347` (loopback only). Stop with Ctrl-C.

## Linux: systemd (user service)

A unit file ships in [deploy/systemd/llmp2pd.service](../../deploy/systemd/llmp2pd.service):

```sh
mkdir -p ~/bin && cp llmp2pd ~/bin/
cp deploy/systemd/llmp2pd.service ~/.config/systemd/user/
# adjust ExecStart in the unit if the binary is not in ~/bin
systemctl --user daemon-reload
systemctl --user enable --now llmp2pd
journalctl --user -u llmp2pd -f
```

Run `loginctl enable-linger $USER` once so the daemon keeps running without an
active login session.

## macOS: launchd (LaunchAgent)

A plist template ships in [deploy/macos/llmp2pd.plist](../../deploy/macos/llmp2pd.plist):

```sh
cp llmp2pd-darwin-arm64 ~/bin/llmp2pd   # adjust arch
sed "s|/Users/YOU/llmp2pd|$HOME/bin/llmp2pd|" \
  deploy/macos/llmp2pd.plist > ~/Library/LaunchAgents/dev.log0u7.llmp2pd.plist
launchctl load ~/Library/LaunchAgents/dev.log0u7.llmp2pd.plist
tail -f /tmp/llmp2pd.log
```

Remove with `launchctl unload`.

## Windows: NSSM

[NSSM](https://nssm.cc/download) wraps the exe as a proper Windows service. A
ready-made script ships in [deploy/windows/nssm-llmp2pd.ps1](../../deploy/windows/nssm-llmp2pd.ps1)
(PowerShell as Administrator):

```powershell
.\deploy\windows\nssm-llmp2pd.ps1 -ExePath C:\llmp2p\llmp2pd.exe
```

It installs the service with auto start, log rotation under
`C:\llmp2p\logs\`, and restart-on-failure. Manage with `nssm status|restart|
remove llmp2pd`.

## Check status

```sh
curl -s localhost:8347/api/v1/status
```

```json
{"version":"0.0.0","uptimeSeconds":120,"models":3,"torrents":3,
 "uploadedBytes":5368709120,"seedingEngines":2}
```

Other endpoints: `/api/v1/models`, `/api/v1/torrents`. Full reference:
[reference/daemon-api.md](../reference/daemon-api.md).

## Models not seeding?

- The model must have been pulled with llmp2p (manifest + torrent present).
- A running `llmp2p pull` holds the store lock: the daemon logs
  `waiting for store lock` and takes over when it is free.
- Check `/api/v1/torrents`: `peers` counts connected peers; 0 peers usually
  means the swarm is empty or your port is unreachable (NAT). See
  [seed-a-model.md](seed-a-model.md) for port forwarding.
