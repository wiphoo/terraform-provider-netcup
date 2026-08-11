package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestTrimStringModifier(t *testing.T) {
	ctx := context.Background()
	m := trimStringModifier{}

	// A trailing newline (as produced by file(...)) is trimmed, so a
	// whitespace-only change is not seen as a diff by the following
	// RequiresReplace modifier and does not trigger a (destructive) replacement.
	req := planmodifier.StringRequest{ConfigValue: types.StringValue("ssh-ed25519 AAAA\n")}
	resp := &planmodifier.StringResponse{PlanValue: req.ConfigValue}
	m.PlanModifyString(ctx, req, resp)
	if got := resp.PlanValue.ValueString(); got != "ssh-ed25519 AAAA" {
		t.Fatalf("got %q, want trimmed %q", got, "ssh-ed25519 AAAA")
	}

	// A null config value is left untouched.
	reqNull := planmodifier.StringRequest{ConfigValue: types.StringNull()}
	respNull := &planmodifier.StringResponse{PlanValue: types.StringValue("unchanged")}
	m.PlanModifyString(ctx, reqNull, respNull)
	if got := respNull.PlanValue.ValueString(); got != "unchanged" {
		t.Fatalf("null config should leave plan untouched, got %q", got)
	}

	// An unknown config value is left untouched.
	reqUnknown := planmodifier.StringRequest{ConfigValue: types.StringUnknown()}
	respUnknown := &planmodifier.StringResponse{PlanValue: types.StringValue("keep")}
	m.PlanModifyString(ctx, reqUnknown, respUnknown)
	if got := respUnknown.PlanValue.ValueString(); got != "keep" {
		t.Fatalf("unknown config should leave plan untouched, got %q", got)
	}
}
