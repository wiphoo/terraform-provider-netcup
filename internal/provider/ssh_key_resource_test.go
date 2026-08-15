package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// testTokenWithUserID builds a JWT-shaped access token whose "id" claim is
// 10001, so the SDK can locally resolve the account-scoped ssh-keys path without
// an HTTP /users/me call (which go-vcr cannot intercept).
func testTokenWithUserID() string {
	return "h.eyJpZCI6MTAwMDF9.s"
}

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

func TestFindMatchingAccountKey(t *testing.T) {
	accountKeys := []netcup.SSHKey{
		{ID: 101, Name: "smb-prod-key", Key: "ssh-ed25519 AAAA existing"},
		{ID: 102, Name: "other-key", Key: "ssh-ed25519 BBBB unrelated"},
	}

	cases := []struct {
		name      string
		keyName   string
		publicKey string
		wantID    int32
	}{
		// Exact name+content match → the pre-existing key is returned.
		{"exact match", "smb-prod-key", "ssh-ed25519 AAAA existing", 101},
		// Surrounding whitespace in config or stored key must not hide a match
		// (matches the trimmed semantics used by the resource).
		{"padded config", " smb-prod-key ", "ssh-ed25519 AAAA existing\n", 101},
		{"padded listed key", "smb-prod-key", "  ssh-ed25519 AAAA existing  ", 101},
		// Same name, different content → a distinct key, not a match.
		{"name-only collision", "smb-prod-key", "ssh-ed25519 CCCC changed", 0},
		// Unknown name → no match.
		{"unknown key", "smb-prod-key", "ssh-ed25519 DDDD never-created", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := findMatchingAccountKey(accountKeys, tc.keyName, tc.publicKey)
			if tc.wantID == 0 {
				if got != nil {
					t.Fatalf("expected no match, got found key id %d", got.ID)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected a match with id %d, got none", tc.wantID)
			}
			if got.ID != tc.wantID {
				t.Fatalf("expected matching id %d, got %d", tc.wantID, got.ID)
			}
		})
	}
}

func configureSSHKeyResource(t *testing.T, client *netcup.Client) (resource.ResourceWithConfigure, resource.SchemaResponse) {
	t.Helper()
	r := NewSSHKeyResource().(resource.ResourceWithConfigure)
	ctx := context.Background()

	var configResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: client}, &configResp)
	if configResp.Diagnostics.HasError() {
		t.Fatalf("Configure() unexpected diagnostics: %v", configResp.Diagnostics.Errors())
	}

	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	return r, schemaResp
}

// sshKeyPlan is a convenience wrapper over resourcePlan for a fresh ssh-key plan.
func sshKeyPlan(schemaResp resource.SchemaResponse, name, publicKey string) tfsdk.Plan {
	return resourcePlan(schemaResp, map[string]tftypes.Value{
		"name":       tftypes.NewValue(tftypes.String, name),
		"public_key": tftypes.NewValue(tftypes.String, publicKey),
	})
}

// TestSSHKeyResource_CreateRefusesExistingKey verifies that Create refuses to
// register a key the account already holds (same trimmed name and content): it
// must emit an import instruction and MUST NOT send a POST.
func TestSSHKeyResource_CreateRefusesExistingKey(t *testing.T) {
	var postCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/users/10001/ssh-keys":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id":101,"name":"smb-prod-key","key":"ssh-ed25519 AAAA existing"}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/users/10001/ssh-keys":
			postCalled = true
			w.WriteHeader(http.StatusInternalServerError)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken(testTokenWithUserID()))
	r, schemaResp := configureSSHKeyResource(t, client)

	ctx := context.Background()
	plan := sshKeyPlan(schemaResp, "smb-prod-key", "ssh-ed25519 AAAA existing")

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if postCalled {
		t.Fatal("Create POSTed a key that already exists in the account")
	}
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic when the key already exists")
	}
	summary := resp.Diagnostics.Errors()[0].Summary()
	if !strings.Contains(summary, "already exists") {
		t.Errorf("error summary %q does not mention that the key already exists", summary)
	}
	if !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "terraform import") {
		t.Errorf("error detail does not include an import instruction: %q", resp.Diagnostics.Errors()[0].Detail())
	}
	if !resp.State.Raw.IsNull() {
		t.Error("no state may be persisted when Create is refused")
	}
}

// TestSSHKeyResource_CreateRegistersNewKey verifies the happy path: an empty
// account listing leads to a POST, and the created key's id is persisted.
func TestSSHKeyResource_CreateRegistersNewKey(t *testing.T) {
	var postReceived string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/users/10001/ssh-keys":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/users/10001/ssh-keys":
			b, _ := io.ReadAll(r.Body)
			postReceived = string(b)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":77,"name":"smb-prod-key","key":"ssh-ed25519 AAAA new"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken(testTokenWithUserID()))
	r, schemaResp := configureSSHKeyResource(t, client)

	ctx := context.Background()
	plan := sshKeyPlan(schemaResp, " smb-prod-key ", " ssh-ed25519 AAAA new\n")

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Create() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if postReceived == "" {
		t.Fatal("Create did not POST the new key")
	}
	// The SDK trims name and key before dispatch; assert the payload was trimmed.
	if !strings.Contains(postReceived, `"name":"smb-prod-key"`) || !strings.Contains(postReceived, `"key":"ssh-ed25519 AAAA new"`) {
		t.Errorf("POST body %q was not trimmed before dispatch", postReceived)
	}

	var state sshKeyResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if state.ID.ValueString() != "77" {
		t.Errorf("ID = %q, want 77", state.ID.ValueString())
	}
	// The configured (padded) value is persisted verbatim; the SDK trims only what
	// it dispatches. Server-side normalization differences are tolerated by the
	// whitespace-agnostic RequiresReplace compare, so state ≠ server payload is fine.
	if state.Name.ValueString() != " smb-prod-key " {
		t.Errorf("Name = %q, want the configured padded value", state.Name.ValueString())
	}
}

// TestSSHKeyResource_CreateListErrorRefuses verifies that when the account's
// keys cannot be listed, Create emits a hard error and does NOT POST — the
// missing-proof-of-absence guard.
func TestSSHKeyResource_CreateListErrorRefuses(t *testing.T) {
	var postCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/users/10001/ssh-keys":
			w.WriteHeader(http.StatusInternalServerError)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/users/10001/ssh-keys":
			postCalled = true
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken(testTokenWithUserID()))
	r, schemaResp := configureSSHKeyResource(t, client)

	ctx := context.Background()
	plan := sshKeyPlan(schemaResp, "smb-prod-key", "ssh-ed25519 AAAA")

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if postCalled {
		t.Fatal("Create POSTed a key although the account listing failed")
	}
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic when the account listing fails")
	}
	if summary := resp.Diagnostics.Errors()[0].Summary(); !strings.Contains(summary, "cannot verify") {
		t.Errorf("error summary %q should explain the listing could not be verified", summary)
	}
	if !resp.State.Raw.IsNull() {
		t.Error("no state may be persisted when Create is refused")
	}
}
