package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

func resourcePlan(schemaResp resource.SchemaResponse, values map[string]tftypes.Value) tfsdk.Plan {
	ctx := context.Background()
	objType := schemaResp.Schema.Type().TerraformType(ctx)
	objAttrs := objType.(tftypes.Object).AttributeTypes

	raw := map[string]tftypes.Value{}
	for name, attrType := range objAttrs {
		if v, ok := values[name]; ok {
			raw[name] = v
		} else {
			raw[name] = tftypes.NewValue(attrType, nil)
		}
	}

	return tfsdk.Plan{
		Raw:    tftypes.NewValue(objType, raw),
		Schema: schemaResp.Schema,
	}
}

func resourceState(schemaResp resource.SchemaResponse, values map[string]tftypes.Value) tfsdk.State {
	ctx := context.Background()
	objType := schemaResp.Schema.Type().TerraformType(ctx)
	objAttrs := objType.(tftypes.Object).AttributeTypes

	raw := map[string]tftypes.Value{}
	for name, attrType := range objAttrs {
		if v, ok := values[name]; ok {
			raw[name] = v
		} else {
			raw[name] = tftypes.NewValue(attrType, nil)
		}
	}

	return tfsdk.State{
		Raw:    tftypes.NewValue(objType, raw),
		Schema: schemaResp.Schema,
	}
}

func configureRDNSResource(t *testing.T, client *netcup.Client) (resource.ResourceWithConfigure, resource.SchemaResponse) {
	t.Helper()
	r := NewRDNSResource().(resource.ResourceWithConfigure)
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

func TestRDNSResource_Create(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/rdns/ipv4":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ip":"1.2.3.4","rdns":"server.example.com"}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/rdns/ipv4/"):
			callCount++
			if callCount > 5 {
				t.Error("too many GetRDNS calls")
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"rdns":"server.example.com"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok123"))
	r, schemaResp := configureRDNSResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"ip_address": tftypes.NewValue(tftypes.String, "1.2.3.4"),
		"hostname":   tftypes.NewValue(tftypes.String, "server.example.com"),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Create() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var state rdnsResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if state.IPAddress.ValueString() != "1.2.3.4" {
		t.Errorf("IPAddress = %q, want 1.2.3.4", state.IPAddress.ValueString())
	}
	if state.Hostname.ValueString() != "server.example.com" {
		t.Errorf("Hostname = %q, want server.example.com", state.Hostname.ValueString())
	}
	if state.ID.ValueString() != "1.2.3.4" {
		t.Errorf("ID = %q, want 1.2.3.4", state.ID.ValueString())
	}
}

func TestRDNSResource_CreateConfirmWarning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/rdns/ipv4":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ip":"1.2.3.4","rdns":"server.example.com"}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/rdns/ipv4/"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"rdns":"wrong.example.com"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok123"))
	r, schemaResp := configureRDNSResource(t, client)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"ip_address": tftypes.NewValue(tftypes.String, "1.2.3.4"),
		"hostname":   tftypes.NewValue(tftypes.String, "server.example.com"),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Create() should not return error even with confirm failure: %v", resp.Diagnostics.Errors())
	}
	if len(resp.Diagnostics) == 0 {
		t.Fatal("expected at least one diagnostic (warning), got none")
	}
	var foundWarning bool
	for _, d := range resp.Diagnostics {
		if d.Summary() == "rDNS confirmation failed" {
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Fatalf("expected warning about confirm failure, got none. Total diagnostics: %d", len(resp.Diagnostics))
	}

	var state rdnsResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if state.ID.ValueString() != "1.2.3.4" {
		t.Errorf("state.ID = %q, want 1.2.3.4 (state should be persisted despite confirm failure)", state.ID.ValueString())
	}
}

func TestRDNSResource_Read(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/v1/rdns/ipv4/") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"rdns":"server.example.com"}`))
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok123"))
	r, schemaResp := configureRDNSResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":         tftypes.NewValue(tftypes.String, "1.2.3.4"),
		"ip_address": tftypes.NewValue(tftypes.String, "1.2.3.4"),
		"hostname":   tftypes.NewValue(tftypes.String, "server.example.com"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var result rdnsResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &result)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if result.Hostname.ValueString() != "server.example.com" {
		t.Errorf("Hostname = %q, want server.example.com", result.Hostname.ValueString())
	}
	if result.ID.ValueString() != "1.2.3.4" {
		t.Errorf("ID = %q, want 1.2.3.4", result.ID.ValueString())
	}
}

func TestRDNSResource_ReadNoPTR(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"rdns":null}`))
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok123"))
	r, schemaResp := configureRDNSResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":         tftypes.NewValue(tftypes.String, "1.2.3.4"),
		"ip_address": tftypes.NewValue(tftypes.String, "1.2.3.4"),
		"hostname":   tftypes.NewValue(tftypes.String, "server.example.com"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	if !resp.State.Raw.IsNull() {
		t.Error("State.Raw should be null after RemoveResource for missing PTR")
	}
}

func TestRDNSResource_Read404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok123"))
	r, schemaResp := configureRDNSResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":         tftypes.NewValue(tftypes.String, "1.2.3.4"),
		"ip_address": tftypes.NewValue(tftypes.String, "1.2.3.4"),
		"hostname":   tftypes.NewValue(tftypes.String, "server.example.com"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, resource.ReadRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	if !resp.State.Raw.IsNull() {
		t.Error("State.Raw should be null after RemoveResource for 404")
	}
}

func TestRDNSResource_Delete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || !strings.HasPrefix(r.URL.Path, "/v1/rdns/ipv4/") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok123"))
	r, schemaResp := configureRDNSResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":         tftypes.NewValue(tftypes.String, "1.2.3.4"),
		"ip_address": tftypes.NewValue(tftypes.String, "1.2.3.4"),
		"hostname":   tftypes.NewValue(tftypes.String, "server.example.com"),
	})

	var resp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
}

func TestRDNSResource_ImportState(t *testing.T) {
	r := NewRDNSResource().(interface {
		resource.ResourceWithImportState
		resource.ResourceWithConfigure
	})

	client := netcup.New(netcup.WithAccessToken("tok"))
	ctx := context.Background()

	var configResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: client}, &configResp)
	if configResp.Diagnostics.HasError() {
		t.Fatalf("Configure() unexpected diagnostics: %v", configResp.Diagnostics.Errors())
	}

	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	var resp resource.ImportStateResponse
	objType := schemaResp.Schema.Type().TerraformType(ctx)
	resp.State = tfsdk.State{
		Raw:    tftypes.NewValue(objType, nil),
		Schema: schemaResp.Schema,
	}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "203.0.113.10"}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("ImportState() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var id types.String
	resp.Diagnostics.Append(resp.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("GetAttribute() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if id.ValueString() != "203.0.113.10" {
		t.Errorf("id = %q, want 203.0.113.10", id.ValueString())
	}
}

func TestRDNSResource_Update(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/rdns/ipv4":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ip":"1.2.3.4","rdns":"new-host.example.com"}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/rdns/ipv4/"):
			callCount++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"rdns":"new-host.example.com"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok123"))
	r, schemaResp := configureRDNSResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"ip_address": tftypes.NewValue(tftypes.String, "1.2.3.4"),
		"hostname":   tftypes.NewValue(tftypes.String, "new-host.example.com"),
	})

	var resp resource.UpdateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Update(ctx, resource.UpdateRequest{Plan: plan}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Update() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var state rdnsResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if state.Hostname.ValueString() != "new-host.example.com" {
		t.Errorf("Hostname = %q, want new-host.example.com", state.Hostname.ValueString())
	}
}

func TestRDNSResource_CanonicalIPValidator(t *testing.T) {
	v := canonicalIPValidator{}

	tests := []struct {
		input   string
		hasDiag bool
	}{
		{"1.2.3.4", false},
		{"2a03:4000:6:b1d::1", false},
		{"::ffff:1.2.3.4", true},
		{"2A03:4000:0006:0B1D:0000:0000:0000:0001", true},
		{"not-an-ip", true},
		{"", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			var resp validator.StringResponse
			v.ValidateString(context.Background(), validator.StringRequest{
				ConfigValue: types.StringValue(tt.input),
			}, &resp)
			if tt.hasDiag && !resp.Diagnostics.HasError() {
				t.Errorf("expected diagnostic for %q, got none", tt.input)
			}
			if !tt.hasDiag && resp.Diagnostics.HasError() {
				t.Errorf("unexpected diagnostic for %q: %v", tt.input, resp.Diagnostics.Errors())
			}
		})
	}
}

func TestRDNSResource_CanonicalHostnameValidator(t *testing.T) {
	v := canonicalHostnameValidator{}

	tests := []struct {
		input   string
		hasDiag bool
	}{
		{"server.example.com", false},
		{"  host.example.com  ", true},
		{"", true},
		{"   ", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			var resp validator.StringResponse
			v.ValidateString(context.Background(), validator.StringRequest{
				ConfigValue: types.StringValue(tt.input),
			}, &resp)
			if tt.hasDiag && !resp.Diagnostics.HasError() {
				t.Errorf("expected diagnostic for %q, got none", tt.input)
			}
			if !tt.hasDiag && resp.Diagnostics.HasError() {
				t.Errorf("unexpected diagnostic for %q: %v", tt.input, resp.Diagnostics.Errors())
			}
		})
	}
}
