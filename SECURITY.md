# Security Policy

## Supported versions

| Version | Supported |
| --- | --- |
| latest release | ✅ |
| older releases | ❌ (please upgrade) |

We support only the latest released version. Security fixes are released as
patch versions (e.g. `v0.1.1`) and announced in the GitHub Release notes.

## Reporting a vulnerability

**Please do not open a public GitHub issue for security vulnerabilities.**

Report security issues privately to
[wiphoo.m@toffoli.co.th](mailto:wiphoo.m@toffoli.co.th). Include:

- A description of the vulnerability and its impact.
- Steps to reproduce or a proof-of-concept (not a working exploit).
- The version(s) affected.
- Any suggested mitigations, if known.

You will receive an acknowledgement within 5 business days and a resolution
timeline once the issue has been assessed. We will credit reporters who wish
to be named in the release notes.

## Scope

Security issues in this project include:

- Authentication or token-handling bugs (e.g. tokens logged, leaked via API,
  or stored insecurely).
- Supply-chain concerns in the published binaries (checksum mismatch, unsigned
  release, tampered dependency).
- Privilege escalation or sandbox escape in the Terraform provider.
- Injection vulnerabilities in API request construction.

**Out of scope** (report to Netcup directly):

- Vulnerabilities in the Netcup SCP / CCP / DNS APIs themselves.
- Issues requiring a compromised Netcup account or API credentials.

## Verifying release integrity

Published binaries can be verified against the signed checksums file — see the
[Releasing section in README.md](README.md#releasing).
