# Contributing to terraform-provider-netcup

Thank you for contributing! This document covers everything you need to get
started — from setting up a dev environment to opening a pull request.

## Table of contents

- [Code of conduct](#code-of-conduct)
- [What can I contribute?](#what-can-i-contribute)
- [Development setup](#development-setup)
- [Running checks](#running-checks)
- [Branch and commit conventions](#branch-and-commit-conventions)
- [Pull request process](#pull-request-process)
- [Architecture overview](#architecture-overview)
- [Release process](#release-process)
- [Labels](#labels)

---

## Code of conduct

This project follows the [Contributor Covenant Code of Conduct](CODE_OF_CONDUCT.md).
By participating you agree to uphold it. Please report unacceptable behavior to
[wiphoo.m@toffoli.co.th](mailto:wiphoo.m@toffoli.co.th).

---

## What can I contribute?

- Bug reports and reproduction cases — open a [Bug report](https://github.com/wiphoo/terraform-provider-netcup/issues/new?template=bug_report.md) issue.
- Feature proposals — open a [Feature request](https://github.com/wiphoo/terraform-provider-netcup/issues/new?template=feature_request.md) issue.
- SDK, CLI, or provider code — see the workflow below.
- Documentation improvements — PRs welcome.
- Test coverage — especially acceptance tests against the live SCP API.

If you are planning a large change, open an issue first so we can align on
direction before you invest time writing code.

---

## Development setup

**Prerequisites:** Go 1.24+ · Git

```bash
git clone https://github.com/wiphoo/terraform-provider-netcup.git
cd terraform-provider-netcup
go mod download
```

Run the test suite:

```bash
go test ./...
```

Build the `netcupctl` CLI:

```bash
go build ./cmd/netcupctl
```

For Terraform provider development you will also need the
[Terraform CLI](https://developer.hashicorp.com/terraform/downloads) (v1.0+).

---

## Running checks

| Check | Command |
| --- | --- |
| Unit tests | `go test ./...` |
| Build | `go build ./...` |
| Vet | `go vet ./...` |
| Format | `gofmt -l .` |
| Lint | `golangci-lint run` (requires [golangci-lint](https://golangci-lint.run/usage/install/)) |

All checks must pass before a PR can be merged.

To validate the GoReleaser config or test a local snapshot build (signing and
publishing are skipped):

```bash
goreleaser check
goreleaser release --snapshot --clean --skip=sign,publish
```

---

## Branch and commit conventions

**Branch naming:** `<issue-number>/<short-description>`

```
9/set-reverse-dns
docs/opensource-hygiene
```

**Commit message:** start with the issue number (if applicable), then a short
imperative summary:

```
9: implement SetRDNS SDK method + netcupctl rdns set
docs: update CONTRIBUTING for open-source hygiene
```

Keep commits focused — one logical change per commit. Avoid unrelated
refactors or reformats bundled with feature work.

---

## Pull request process

1. Fork the repo and create a branch from `main`.
2. Make your changes, add or update tests, update docs.
3. Ensure all checks pass locally.
4. Open a PR against `main`. Fill in the PR template.
5. A maintainer will review. Address feedback; the reviewer resolves threads
   when satisfied.
6. Once approved and CI is green, the maintainer merges.

We do not require a CLA, but by submitting a PR you agree that your
contribution is licensed under the project's [MPL-2.0 license](LICENSE).

---

## Architecture overview

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the full design.
The short version:

```
Terraform config
  └─ Terraform Plugin Framework
       └─ Provider layer
            └─ Service layer
                 └─ SDK (pkg/netcup/)  ←── also used by netcupctl CLI
                      └─ API client layer
                           └─ Netcup SCP / CCP APIs
```

Key rules:
- The SDK (`pkg/netcup/`) must not import provider or CLI packages.
- Resources call service interfaces, not the API client directly.
- The CLI and provider share the same SDK — do not duplicate HTTP or auth logic.
- Do not edit generated code (once it exists); regenerate instead.

---

## Release process

Releases are cut by pushing a SemVer tag:

```bash
git tag v0.1.0
git push origin v0.1.0
```

GoReleaser builds binaries for linux, macOS, and Windows (amd64 + arm64),
attaches them to a GitHub Release, and signs the checksums file with
[cosign keyless](README.md#releasing). See the
[Releasing section in README.md](README.md#releasing) for the full process and
how to verify a download.

Versioning follows [Semantic Versioning](https://semver.org).

---

## Labels

| Label | Meaning |
| --- | --- |
| `type/bug` | Something is broken |
| `type/feature` | New capability |
| `type/chore` | Non-functional maintenance (CI, deps, docs) |
| `type/documentation` | Docs-only change |
| `type/refactor` | Code restructuring without behavior change |
| `area/sdk` | `pkg/netcup/` SDK |
| `area/cli` | `cmd/netcupctl/` CLI |
| `area/provider` | Terraform provider |
| `area/ci` | CI / build / release automation |
| `priority/high` | Blocks a milestone |
| `priority/medium` | Important but not blocking |
| `priority/low` | Nice to have |
| `needs-decision` | Requires a human decision before work can proceed |
| `ready` | Ready to be picked up |
