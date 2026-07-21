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
// value UNKNOWN (i.e. "known after apply") when a replacement is planned. A
// replacement is detected by comparing the plan's server_id against the state's
// server_id: when they differ (and both are known), server_id's RequiresReplace
// will force a destroy+create, so id/password must be recomputed. For every
// other plan (in-place updates such as toggling wait, or a no-op refresh) the
// prior state value is reused, preserving plan stability.

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
	if isRescueReplacement(ctx, req.State, req.Plan) {
		// Replacement: leave the value unknown so it is recomputed by Create.
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
	if isRescueReplacement(ctx, req.State, req.Plan) {
		return
	}
	resp.PlanValue = req.StateValue
}

// isRescueReplacement reports whether the current plan is a replacement of the
// rescue resource, i.e. server_id differs between prior state and plan. When the
// two server_id values are both known and unequal, server_id's RequiresReplace()
// forces a destroy+create, so computed attributes (id/password) must NOT be
// carried over from the old state. Any read/type error is treated conservatively
// as "not a replacement" so the fallback is the stable UseStateForUnknown
// behavior rather than spuriously churning the plan.
func isRescueReplacement(ctx context.Context, state, plan attributeReader) bool {
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
	return stateServerID.ValueString() != planServerID.ValueString()
}
