package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestRequiresReplaceIfNotTrimEqual(t *testing.T) {
	ctx := context.Background()

	run := func(state types.String, plan types.String) bool {
		req := planmodifier.StringRequest{StateValue: state, PlanValue: plan}
		resp := &stringplanmodifier.RequiresReplaceIfFuncResponse{}
		requiresReplaceIfNotTrimEqual(ctx, req, resp)
		return resp.RequiresReplace
	}

	// Whitespace-only change (trailing newline / surrounding spaces): no replace.
	if run(types.StringValue("ssh-ed25519 AAAA\n"), types.StringValue("ssh-ed25519 AAAA")) {
		t.Fatal("trailing-newline-only change must not require replacement")
	}
	if run(types.StringValue("  key  "), types.StringValue("key")) {
		t.Fatal("surrounding-whitespace-only change must not require replacement")
	}

	// Genuine content change: replace.
	if !run(types.StringValue("ssh-ed25519 AAAA"), types.StringValue("ssh-ed25519 BBBB")) {
		t.Fatal("a genuine key change must require replacement")
	}

	// Create (null prior state): never a replacement.
	if run(types.StringNull(), types.StringValue("ssh-ed25519 AAAA")) {
		t.Fatal("create (null prior state) must not require replacement")
	}

	// Unknown prior state: conservatively replace.
	if !run(types.StringUnknown(), types.StringValue("ssh-ed25519 AAAA")) {
		t.Fatal("unknown prior state should conservatively require replacement")
	}
}
