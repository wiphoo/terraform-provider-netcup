package provider

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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

func TestReconcileCreatedSSHKeyAdoptsOnlyNewKey(t *testing.T) {
	ctx := context.Background()
	// The account contains both a pre-existing matching key (id 5) and a
	// newly-created matching key (id 8) with identical name+content.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":5,"name":"k8s","key":"ssh-ed25519 AAAA"},{"id":8,"name":"k8s","key":"ssh-ed25519 AAAA"}]`))
	}))
	defer srv.Close()
	// A JWT-shaped token with an "id" claim so the SDK can build the account-scoped
	// /v1/users/{id}/ssh-keys path (the httptest handler ignores the path).
	tok := "h." + base64.RawURLEncoding.EncodeToString([]byte(`{"id":12345,"exp":9999999999}`)) + ".s"
	r := &sshKeyResource{client: netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken(tok))}

	// id 5 existed before the create → only the NEW id 8 may be adopted.
	got, err := r.reconcileCreatedSSHKey(ctx, "k8s", "ssh-ed25519 AAAA", map[int32]struct{}{5: {}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || got.ID != 8 {
		t.Fatalf("expected to adopt only the newly-appeared key id=8, got %+v", got)
	}

	// When every matching key pre-existed, adopt nothing (do not steal an
	// unmanaged key).
	got2, err := r.reconcileCreatedSSHKey(ctx, "k8s", "ssh-ed25519 AAAA", map[int32]struct{}{5: {}, 8: {}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got2 != nil {
		t.Fatalf("must not adopt a pre-existing key, got %+v", got2)
	}
}
