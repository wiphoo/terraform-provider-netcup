# Architecture Decision Records

This directory holds Architecture Decision Records (ADRs) — short documents that
capture a single significant design decision, the alternatives weighed, and the
consequences. They complement the higher-level narrative in
[`../ARCHITECTURE.md`](../ARCHITECTURE.md): `ARCHITECTURE.md` describes the
system as it stands; ADRs record *why* specific choices were made and what was
rejected.

## Conventions

- One decision per file, named `NNNN-short-slug.md` (zero-padded, monotonically
  increasing).
- Status is one of `Proposed`, `Accepted`, `Superseded by ADR-XXXX`, or
  `Deprecated`. Never edit the decision of an accepted ADR in place — supersede
  it with a new one and cross-link both.
- Keep them short. An ADR is a decision, not a design doc.

## Index

| ADR | Title | Status |
|-----|-------|--------|
| [0001](0001-server-reinstall-resource-lifecycle.md) | `netcup_server_reinstall` resource lifecycle | Accepted |
