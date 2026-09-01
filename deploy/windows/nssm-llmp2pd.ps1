# llmp2pd as a Windows service via NSSM (the Non-Sucking Service Manager).
#
# Prerequisites:
#   1. Download nssm: https://nssm.cc/download (nssm.exe on PATH, e.g. C:\Tools\nssm)
#   2. Unpack the release binaries, e.g. C:\llmp2p\llmp2pd-windows-amd64.exe -> C:\llmp2p\llmp2pd.exe
#
# Install (PowerShell as Administrator):
#   .\deploy\windows\nssm-llmp2pd.ps1 -ExePath C:\llmp2p\llmp2pd.exe
#
# Manage:
#   nssm status llmp2pd
#   nssm restart llmp2pd
#   nssm remove llmp2pd confirm
param(
    [Parameter(Mandatory = $true)]
    [string]$ExePath
)

if (-not (Get-Command nssm -ErrorAction SilentlyContinue)) {
    Write-Error "nssm.exe not found on PATH. Download https://nssm.cc/download and add it to PATH."
    exit 1
}
if (-not (Test-Path $ExePath)) {
    Write-Error "Executable not found: $ExePath"
    exit 1
}
$ExePath = (Resolve-Path $ExePath).Path
$AppDir = Split-Path $ExePath -Parent
$LogDir = Join-Path $AppDir "logs"
New-Item -ItemType Directory -Force -Path $LogDir | Out-Null

nssm install llmp2pd $ExePath
nssm set llmp2pd AppDirectory $AppDir
nssm set llmp2pd DisplayName "llmp2pd model swarm seeder"
nssm set llmp2pd Description "Keeps llmp2p models seeding in the background (BitTorrent DHT) and serves the local status API on 127.0.0.1:8347."
nssm set llmp2pd Start SERVICE_AUTO_START
nssm set llmp2pd AppStdout (Join-Path $LogDir "llmp2pd.out.log")
nssm set llmp2pd AppStderr (Join-Path $LogDir "llmp2pd.err.log")
nssm set llmp2pd AppRotateFiles 1
nssm set llmp2pd AppRotateOnline 1
nssm set llmp2pd AppRotateBytes 10485760
nssm set llmp2pd AppExit Default Restart
nssm set llmp2pd AppRestartDelay 5000

nssm start llmp2pd
Write-Host "llmp2pd installed and started. Status API: http://127.0.0.1:8347/api/v1/status"
