# Security Policy

## Reporting a vulnerability

Please report security issues through GitHub's private vulnerability
reporting: https://github.com/log0u7/llmp2p/security/advisories/new

Include: the affected version (`llmp2p --version`), the exact command or
setup, and your assessment of impact. Do not open a public issue for
security reports.

You will get an acknowledgment within 7 days and a fix or mitigation plan
for confirmed issues.

## Scope notes

- The status API (`127.0.0.1:8347`) is loopback-only by design; reports about
  remote access to it are welcome if you find a way to bind it elsewhere.
- The trust model (what is and is not protected against malicious peers) is
  documented in [docs/explanation/trust-model.md](docs/explanation/trust-model.md).
  Findings that break the documented chain (manifest sha256 pinning, final
  per-file verification) are high priority.

## Supported versions

The latest tagged release receives security fixes. This is a 0.x project:
expect the fix to land on `main` and ship with the next tag.
