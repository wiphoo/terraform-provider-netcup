# ADR-0002: `netcup_server_snapshot_restore` resource lifecycle

- **Status:** Accepted
- **Date:** 2026-08-27
- **Context issue:** [#143](https://github.com/wiphoo/terraform-provider-netcup/issues/143) (v0.7.1 epic [#141](https://github.com/wiphoo/terraform-provider-netcup/issues/141))
- **Deciders:** @wiphoo

## Context

v0.7.1 exposes snapshot restore through a Terraform provider resource,
`netcup_server_snapshot_restore`, wrapping the SDK `RestoreSnapshot` /
`POST /v1/servers/{id}/snapshots/{name}/revert`.

Restore is fundamentally an **action**, not a persistent object:

- It is **destructive** — it reverts the server to a snapshot; all changes since the snapshot are lost.
- The API returns a `202 TaskInfo`; completion is asynchronous.
- It leaves **no durable "restore" resource** behind to `GET` and reconcile
  against. Once the restore task finishes, the only artifact is a server restored to the snapshot state.

Terraform's resource model assumes a create/read/update/delete lifecycle over a
persistent object. Modelling a one-shot destructive action as a managed resource
forces a decision about how re-runs are triggered and how the destructive intent
is surfaced in `terraform plan`. Getting this wrong risks either silently
reverting a server on an innocuous-looking diff, or making legitimate restore
impossible.

This ADR follows the safety stance set by `netcup_server_power` (Delete is a documented no-op) and mirrors ADR-0001 (reinstall).

## Decision

Model `netcup_server_snapshot_restore` as a managed resource with the following
lifecycle:

1. **Install inputs use `RequiresReplace`.** `server_id`, `snapshot_name`,
   and the other fields (`triggers`) carry a `RequiresReplace` plan modifier.
   Changing any of them plans a **replace**, so the destructive action is
   rendered as `-/+ must be replaced` in `terraform plan` — visible before
   apply. `server_id` is also `RequiresReplace` (a different server is a
   different restore).

2. **An optional `triggers` map forces a re-run on unchanged config.** A
   `map(string)` attribute (à la `terraform_data` / `null_resource`), also
   `RequiresReplace`, lets users deliberately re-run the restore without
   changing the restore inputs — e.g. keying on a script-content hash, a
   timestamp, or a rotation marker. This is the escape hatch for re-running
   with identical inputs, which `RequiresReplace` on the inputs alone cannot
   express.

3. **No in-place `Update` that restores.** The provider does not silently
   revert a server behind an in-place `~ update` diff. `Update` only reconciles
   non-restoring metadata (if any); every restore goes through the
   replace path so it is always visible as a replacement.

4. **`Delete` is a strict no-op** (state-only removal). Destroying
   `netcup_server_snapshot_restore` never reverts or restores the server. Since
   replace = delete-then-create, this makes the delete half of a replace
   harmless; the create half performs the restore. `create_before_destroy` is
   unsupported and documented as such (a server cannot host two concurrent
   restores).

5. **`Read` is deliberately thin.** There is no restore object to fetch. `Read`
   confirms the server still exists (`404` → remove) and preserves the
   last-applied inputs. It does **not** attempt to detect drift in
   `triggers` or other restore inputs — post-restore state is not recoverable
   from the API, so treating it as drift would produce permanent spurious diffs.

6. **Task failure taints the resource.** When `wait = true` (default) and the
   async restore task reaches a non-terminal/failed state, Create/Update returns
   a hard diagnostic. Terraform then marks the resource **tainted**, so the
   next apply retries the restore. A failed restore must not be reported as
   success.

## Options considered

### Option A — `RequiresReplace` on restore inputs (chosen, part 1)

Immutable inputs; any change destroys and recreates the resource instance,
Create runs the restore.

- **Pros:** idiomatic plugin-framework; destructive intent visible as a replace
  in `plan`; only Create + no-op Delete + thin Read to implement; mirrors "AMI
  change replaces instance".
- **Cons:** replace = delete-then-create, so the plan says "destroyed" even
  though Delete is a no-op (mildly misleading); cannot re-run on identical
  config; `create_before_destroy` is meaningless here.

### Option B — in-place `Update` runs the restore (rejected)

Mutable inputs; a changed input triggers `RestoreSnapshot` on the same instance.

- **Pros:** cleanest state semantics (one server ↔ one persistent instance,
  never "destroyed"); full control over wait/task handling in `Update`.
- **Cons:** **hides destruction** behind an in-place `~ update` diff — the exact
  failure mode this milestone exists to prevent for a data-loss operation;
  fights Terraform's "mutate the existing thing" mental model against the
  reality of a full revert; more bespoke Update logic. Rejected on safety grounds.

### Option C — `triggers` map, `null_resource`-style (chosen, part 2)

Opaque `triggers` map with `RequiresReplace`; re-runs driven by user-supplied
trigger values.

- **Pros:** most explicit — re-running a restore is an opt-in change to `triggers`;
  enables re-running on identical restore config; a well-understood pattern.
- **Cons:** extra concept to document; if `triggers` were the *only* re-run
  driver, a changed `server_id` would show no diff (a footgun). Adopting A
  **and** C together resolves this: real inputs already force replace, and
  `triggers` adds deliberate re-runs on top.

## Consequences

- Every restore — whether from an input change or a `triggers` bump — appears
  in `terraform plan` as a resource **replacement**, keeping the destructive
  action loud and reviewable.
- Users can force a re-run on unchanged restore inputs via `triggers`.
- The implementation stays small: Create (restore + optional `WaitForTask`),
  thin Read, no-op Delete, minimal Update; no drift detection on restore inputs.
- Documentation must state plainly that (a) restore reverts the server, (b) a
  plan showing this resource being replaced means a restore will run, and
  (c) destroying the resource does **not** revert or restore the server.
- A failed restore task taints the resource so the next apply retries, rather
  than leaving Terraform believing a failed restore succeeded.

## References

- Epic [#141](https://github.com/wiphoo/terraform-provider-netcup/issues/141),
  resource issue [#143](https://github.com/wiphoo/terraform-provider-netcup/issues/143)
- SDK `RestoreSnapshot` — `pkg/netcup/snapshot.go` (RestoreSnapshot function)
- Prior-art safety stance — `netcup_server_power` Delete no-op
  (`internal/provider/server_power_resource.go`, v0.4.0)
- Async task polling — `WaitForTask` (`pkg/netcup/tasks.go`, #60)
- ADR-0001: `netcup_server_reinstall` resource lifecycle
  (`docs/adr/0001-server-reinstall-resource-lifecycle.md`)