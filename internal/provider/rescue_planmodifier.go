package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// attributeReader is satisfied by both tfsdk.State and tfsdk.Plan; it lets
// isRescueReplacement read a single attribute from either without depending on
// the concrete request type.
type attributeReader interface {
	GetAttribute(ctx context.Context, path path.Path, target interface{}) diag.Diagnostics
}

// Thread A fix: replacement-safe UseStateForUnknown.
//
// The stock stringplanmodifier.UseStateForUnknown()/boolplanmodifier variants
// copy the prior state value into the plan for any unknown attribute — including
// during a REPLACEMENT plan triggered by server_id's RequiresReplace(). During a
// replace, the prior state holds the OLD server's id/password, but Create runs
// against the NEW server_id and returns a NEW id + a freshly generated rescue
// password. Copying the stale value into the plan makes Terraform expect the old
// value and then reject the apply with "Provider produced inconsistent result
// after apply".
//
// These modifiers behave exactly like UseStateForUnknown EXCEPT they leave the
// value UNKNOWN (i.e. "known after apply") whenever a replacement is (or might
// be) planned. The prior state value is reused ONLY when the plan can be proven
// to target the same server: plan server_id known AND prior-state server_id
// known AND the two are equal. In every other case the value is left unknown.
//
// This conservative rule matters when the plan's server_id is UNKNOWN (e.g. it
// is derived from another resource's not-yet-known output). stringplanmodifier's
// RequiresReplace() treats an unknown-changed server_id as a replacement and
// schedules a destroy+create, but we cannot prove the target is the same server.
// If we copied the old id/password/active into the plan as known values and
// server_id then resolved to a DIFFERENT server, Create would return new
// id/password after the disruptive enable and Terraform would reject them as
// "Provider produced inconsistent result after apply". Leaving the value unknown
// mirrors stock UseStateForUnknown's own guard, which does nothing when the
// relevant value is unknown. For a genuine no-op (unchanged known server_id) the
// prior state value is still reused, preserving plan stability.

// useStateForUnknownUnlessReplacingString is the types.String variant.
type useStateForUnknownUnlessReplacingString struct{}

func (m useStateForUnknownUnlessReplacingString) Description(_ context.Context) string {
	return "Copies the prior state value into the plan when it is unknown, except when a replacement is planned (server_id changed), in which case the value is recomputed."
}

func (m useStateForUnknownUnlessReplacingString) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m useStateForUnknownUnlessReplacingString) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	// Nothing to do on create (no prior state) or destroy (no plan).
	if req.StateValue.IsNull() {
		return
	}
	if req.PlanValue.IsNull() {
		return
	}
	// Only act when the planned value is unknown; a known planned value (e.g. a
	// user-set config) is left untouched.
	if !req.PlanValue.IsUnknown() {
		return
	}
	if !rescueServerIDStableAndEqual(ctx, req.State, req.Plan) {
		// The plan cannot be proven to target the same server (server_id
		// changed, or is unknown/null on either side). A replacement is (or may
		// be) planned, so leave the value unknown for Create to recompute.
		return
	}
	resp.PlanValue = req.StateValue
}

// useStateForUnknownUnlessReplacingBool is the types.Bool variant.
type useStateForUnknownUnlessReplacingBool struct{}

func (m useStateForUnknownUnlessReplacingBool) Description(_ context.Context) string {
	return "Copies the prior state value into the plan when it is unknown, except when a replacement is planned (server_id changed), in which case the value is recomputed."
}

func (m useStateForUnknownUnlessReplacingBool) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m useStateForUnknownUnlessReplacingBool) PlanModifyBool(ctx context.Context, req planmodifier.BoolRequest, resp *planmodifier.BoolResponse) {
	if req.StateValue.IsNull() {
		return
	}
	if req.PlanValue.IsNull() {
		return
	}
	if !req.PlanValue.IsUnknown() {
		return
	}
	if !rescueServerIDStableAndEqual(ctx, req.State, req.Plan) {
		return
	}
	resp.PlanValue = req.StateValue
}

// rescueServerIDStableAndEqual reports whether the plan can be proven to target
// the SAME server as the prior state: plan server_id is known AND prior-state
// server_id is known AND the two are equal. Only in that case is it safe to
// carry the prior computed value (id/password/active) into an unknown plan
// value.
//
// It returns false in every ambiguous case — a changed server_id (RequiresReplace
// forces destroy+create), an unknown plan server_id (derived from another
// resource's not-yet-known output, which RequiresReplace also treats as a
// replacement), a null/unknown prior-state server_id, or any read/type error.
// Returning false there leaves the computed value unknown ("known after apply"),
// mirroring stock UseStateForUnknown's guard and avoiding "Provider produced
// inconsistent result after apply" if server_id resolves to a different server.
func rescueServerIDStableAndEqual(ctx context.Context, state, plan attributeReader) bool {
	var stateServerID types.String
	if diags := state.GetAttribute(ctx, path.Root("server_id"), &stateServerID); diags.HasError() {
		return false
	}
	var planServerID types.String
	if diags := plan.GetAttribute(ctx, path.Root("server_id"), &planServerID); diags.HasError() {
		return false
	}
	if stateServerID.IsNull() || stateServerID.IsUnknown() {
		return false
	}
	if planServerID.IsNull() || planServerID.IsUnknown() {
		return false
	}
	return stateServerID.ValueString() == planServerID.ValueString()
}
