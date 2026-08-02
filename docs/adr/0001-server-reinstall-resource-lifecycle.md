# ADR-0001: `netcup_server_reinstall` resource lifecycle

- **Status:** Accepted
- **Date:** 2026-07-31
- **Context issue:** [#113](https://github.com/wiphoo/terraform-provider-netcup/issues/113) (v0.6.0 epic [#112](https://github.com/wiphoo/terraform-provider-netcup/issues/112))
- **Deciders:** @wiphoo

## Context

v0.6.0 exposes native OS (re)install through a Terraform provider resource,
`netcup_server_reinstall`, wrapping the SDK `ReinstallServer` /
`ServerImageSetup` proven in v0.5.0 (`POST /v1/servers/{serverId}/image`).

Reinstall is fundamentally an **action**, not a persistent object:

- It is **destructive** — it wipes the server; all data is lost.
- The API returns a `202 TaskInfo`; completion is asynchronous.
- It leaves **no durable "reinstall" resource** behind to `GET` and reconcile
  against. Once the install task finishes, the only artifact is a freshly
  installed server.

Terraform's resource model assumes a create/read/update/delete lifecycle over a
persistent object. Modelling a one-shot destructive action as a managed resource
forces a decision about how re-runs are triggered and how the destructive intent
is surfaced in `terraform plan`. Getting this wrong risks either silently
wiping a server on an innocuous-looking diff, or making legitimate re-bootstrap
impossible.

This ADR follows the safety stance already set by `netcup_server_power`
(ADR-adjacent: Delete is a documented no-op — destroying the resource must not
power the server off) and the v0.5.0 CLI, where every reinstall is opt-in and
loudly warns before wiping.

## Decision

Model `netcup_server_reinstall` as a managed resource with the following
lifecycle:

1. **Install inputs use `RequiresReplace`.** `image_flavour_id`, `custom_script`,
   `hostname`, `ssh_key_ids`, and the other `ServerImageSetup` fields carry a
   `RequiresReplace` plan modifier. Changing any of them plans a **replace**, so
   the destructive action is rendered as `-/+ must be replaced` in
   `terraform plan` — visible before apply. `server_id` is also `RequiresReplace`
   (a different server is a different install).

2. **An optional `triggers` map forces a re-run on unchanged config.** A
   `map(string)` attribute (à la `terraform_data` / `null_resource`), also
   `RequiresReplace`, lets users deliberately re-run the reinstall without
   changing the install inputs — e.g. keying on a script-content hash, an image
   version, or a rotation timestamp. This is the escape hatch for re-bootstrapping
   with identical inputs, which `RequiresReplace` on the inputs alone cannot
   express.

3. **No in-place `Update` that reinstalls.** The provider does not silently wipe
   a server behind an in-place `~ update` diff. `Update` only reconciles
   non-reinstalling metadata (if any); every reinstall goes through the
   replace path so it is always visible as a replacement.

4. **`Delete` is a strict no-op** (state-only removal). Destroying
   `netcup_server_reinstall` never wipes or reinstalls the server. Since replace
   = delete-then-create, this makes the delete half of a replace harmless; the
   create half performs the reinstall. `create_before_destroy` is unsupported
   and documented as such (a server cannot host two concurrent installs).

5. **`Read` is deliberately thin.** There is no reinstall object to fetch. `Read`
   confirms the server still exists (`404` → remove from state) and preserves the
   last-applied inputs. It does **not** attempt to detect drift in `custom_script`
   or other install inputs — post-install state is not recoverable from the API,
   so treating it as drift would produce permanent spurious diffs.

6. **Task failure taints the resource.** When `wait = true` (default) and the
   async install task reaches a non-terminal/failed state, Create/Update returns
   a hard diagnostic. Terraform then marks the resource **tainted**, so the next
   apply retries the reinstall. A failed wipe must not be reported as success.

## Options considered

### Option A — `RequiresReplace` on install inputs (chosen, part 1)

Immutable inputs; any change destroys and recreates the resource instance,
Create runs the reinstall.

- **Pros:** idiomatic plugin-framework; destructive intent visible as a replace
  in `plan`; only Create + no-op Delete + thin Read to implement; mirrors "AMI
  change replaces instance".
- **Cons:** replace = delete-then-create, so the plan says "destroyed" even
  though Delete is a no-op (mildly misleading); cannot re-run on identical
  config; `create_before_destroy` is meaningless here.

### Option B — in-place `Update` runs the reinstall (rejected)

Mutable inputs; a changed input triggers `ReinstallServer` on the same instance.

- **Pros:** cleanest state semantics (one server ↔ one persistent instance,
  never "destroyed"); full control over wait/task handling in `Update`.
- **Cons:** **hides destruction** behind an in-place `~ update` diff — the exact
  failure mode this milestone exists to prevent for a data-loss operation;
  fights Terraform's "mutate the existing thing" mental model against the reality
  of a full reimage; more bespoke Update logic. Rejected on safety grounds.

### Option C — `triggers` map, `null_resource`-style (chosen, part 2)

Opaque `triggers` map with `RequiresReplace`; re-runs driven by user-supplied
trigger values.

- **Pros:** most explicit — re-running a wipe is an opt-in change to `triggers`;
  enables re-bootstrap on identical install config; a well-understood pattern.
- **Cons:** extra concept to document; if `triggers` were the *only* re-run
  driver, a changed `custom_script` would show no diff (a footgun). Adopting A
  **and** C together resolves this: real inputs already force replace, and
  `triggers` adds deliberate re-runs on top.

## Consequences

- Every reinstall — whether from an input change or a `triggers` bump — appears
  in `terraform plan` as a resource **replacement**, keeping the destructive
  action loud and reviewable.
- Users can force a re-bootstrap on unchanged install inputs via `triggers`.
- The implementation stays small: Create (reinstall + optional `WaitForTask`),
  thin Read, no-op Delete, minimal Update; no drift detection on install inputs.
- Documentation must state plainly that (a) reinstall wipes the server, (b) a
  plan showing this resource being replaced means a reinstall will run, and
  (c) destroying the resource does **not** reinstall or wipe the server.
- A failed install task taints the resource so the next apply retries, rather
  than leaving Terraform believing a failed wipe succeeded.

## References

- Epic [#112](https://github.com/wiphoo/terraform-provider-netcup/issues/112),
  resource issue [#113](https://github.com/wiphoo/terraform-provider-netcup/issues/113)
- SDK `ReinstallServer` / `ServerImageSetup` — `pkg/netcup/reinstall.go` (v0.5.0, #101)
- Prior-art safety stance — `netcup_server_power` Delete no-op
  (`internal/provider/server_power_resource.go`, v0.4.0)
- Async task polling — `WaitForTask` (`pkg/netcup/tasks.go`, #60)
- [`../REINSTALL.md`](../REINSTALL.md), [`../SCP-API-NOTES.md`](../SCP-API-NOTES.md)
