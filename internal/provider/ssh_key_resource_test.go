package provider

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
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

func TestTrimEqualPlanState(t *testing.T) {
	if !trimEqualPlanState(types.StringValue("ssh-ed25519 AAAA\n"), types.StringValue("ssh-ed25519 AAAA")) {
		t.Fatal("a trailing-newline-only difference must be trim-equal (in-place, id preserved)")
	}
	if trimEqualPlanState(types.StringValue("key-a"), types.StringValue("key-b")) {
		t.Fatal("genuinely different values must not be trim-equal (replacement)")
	}
	// Unknown/null on either side can't be proven equal → treated as a replacement.
	if trimEqualPlanState(types.StringUnknown(), types.StringValue("k")) {
		t.Fatal("unknown planned value must not be trim-equal")
	}
	if trimEqualPlanState(types.StringValue("k"), types.StringNull()) {
		t.Fatal("null state value must not be trim-equal")
	}
}

func TestIsDefinitiveSSHKeyRejection(t *testing.T) {
	if !isDefinitiveSSHKeyRejection(fmt.Errorf("%w: token", netcup.ErrPreDispatch)) {
		t.Fatal("pre-dispatch error must be definitive")
	}
	if !isDefinitiveSSHKeyRejection(&netcup.APIError{StatusCode: 422}) {
		t.Fatal("a 4xx APIError must be definitive")
	}
	if isDefinitiveSSHKeyRejection(&netcup.APIError{StatusCode: 502}) {
		t.Fatal("a 5xx APIError must be ambiguous (not definitive)")
	}
	if isDefinitiveSSHKeyRejection(errors.New("connection reset after dispatch")) {
		t.Fatal("a plain transport error must be ambiguous (not definitive)")
	}
}
