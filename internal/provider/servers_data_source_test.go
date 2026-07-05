package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

func TestServersDataSource_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/servers" {
			t.Errorf("path = %q, want /v1/servers", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[
			{"id":1,"name":"web-01","hostname":"web-01.example.com","nickname":"Web One","disabled":false,"template":{"id":10,"name":"VM 2000"}},
			{"id":2,"name":"db-01","hostname":"db-01.example.com","nickname":"","disabled":true,"template":null}
		]`))
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok123"))
	ctx := context.Background()
	ds := NewServersDataSource().(datasource.DataSourceWithConfigure)

	var configResp datasource.ConfigureResponse
	ds.Configure(ctx, datasource.ConfigureRequest{ProviderData: client}, &configResp)
	if configResp.Diagnostics.HasError() {
		t.Fatalf("Configure() unexpected diagnostics: %v", configResp.Diagnostics.Errors())
	}

	var schemaResp datasource.SchemaResponse
	ds.Schema(ctx, datasource.SchemaRequest{}, &schemaResp)

	var resp datasource.ReadResponse
	resp.State.Schema = schemaResp.Schema

	ds.Read(ctx, datasource.ReadRequest{}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var state serversDataSourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	if len(state.Servers) != 2 {
		t.Fatalf("got %d servers, want 2", len(state.Servers))
	}

	s1 := state.Servers[0]
	if s1.ID.ValueString() != "1" {
		t.Errorf("s1.ID = %q, want 1", s1.ID.ValueString())
	}
	if s1.Name.ValueString() != "web-01" {
		t.Errorf("s1.Name = %q, want web-01", s1.Name.ValueString())
	}
	if s1.Hostname.ValueString() != "web-01.example.com" {
		t.Errorf("s1.Hostname = %q, want web-01.example.com", s1.Hostname.ValueString())
	}
	if s1.Nickname.ValueString() != "Web One" {
		t.Errorf("s1.Nickname = %q, want Web One", s1.Nickname.ValueString())
	}
	if s1.Disabled.ValueBool() {
		t.Errorf("s1.Disabled = true, want false")
	}
	if s1.ProductName.ValueString() != "VM 2000" {
		t.Errorf("s1.ProductName = %q, want VM 2000", s1.ProductName.ValueString())
	}

	s2 := state.Servers[1]
	if s2.ID.ValueString() != "2" {
		t.Errorf("s2.ID = %q, want 2", s2.ID.ValueString())
	}
	if s2.Name.ValueString() != "db-01" {
		t.Errorf("s2.Name = %q, want db-01", s2.Name.ValueString())
	}
	if s2.Hostname.ValueString() != "db-01.example.com" {
		t.Errorf("s2.Hostname = %q, want db-01.example.com", s2.Hostname.ValueString())
	}
	if s2.Nickname.ValueString() != "" {
		t.Errorf("s2.Nickname = %q, want empty string", s2.Nickname.ValueString())
	}
	if !s2.Disabled.ValueBool() {
		t.Errorf("s2.Disabled = false, want true")
	}
	if s2.ProductName.ValueString() != "" {
		t.Errorf("s2.ProductName = %q, want empty string", s2.ProductName.ValueString())
	}
}

func TestServersDataSource_EmptyList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok123"))
	ctx := context.Background()
	ds := NewServersDataSource().(datasource.DataSourceWithConfigure)

	var configResp datasource.ConfigureResponse
	ds.Configure(ctx, datasource.ConfigureRequest{ProviderData: client}, &configResp)
	if configResp.Diagnostics.HasError() {
		t.Fatalf("Configure() unexpected diagnostics: %v", configResp.Diagnostics.Errors())
	}

	var schemaResp datasource.SchemaResponse
	ds.Schema(ctx, datasource.SchemaRequest{}, &schemaResp)

	var resp datasource.ReadResponse
	resp.State.Schema = schemaResp.Schema

	ds.Read(ctx, datasource.ReadRequest{}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var state serversDataSourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	if len(state.Servers) != 0 {
		t.Errorf("got %d servers, want 0", len(state.Servers))
	}
}

func TestServersDataSource_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"invalid token"}`))
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("bad-token"))
	ctx := context.Background()
	ds := NewServersDataSource().(datasource.DataSourceWithConfigure)

	var configResp datasource.ConfigureResponse
	ds.Configure(ctx, datasource.ConfigureRequest{ProviderData: client}, &configResp)
	if configResp.Diagnostics.HasError() {
		t.Fatalf("Configure() unexpected diagnostics: %v", configResp.Diagnostics.Errors())
	}

	var schemaResp datasource.SchemaResponse
	ds.Schema(ctx, datasource.SchemaRequest{}, &schemaResp)

	var resp datasource.ReadResponse
	resp.State.Schema = schemaResp.Schema

	ds.Read(ctx, datasource.ReadRequest{}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("Read() expected error diagnostic, got none")
	}

	var foundIPAllowlist bool
	var foundBearer bool
	for _, d := range resp.Diagnostics.Errors() {
		if strings.Contains(d.Detail(), "IP allowlist") {
			foundIPAllowlist = true
		}
		if strings.Contains(d.Detail(), "Bearer") {
			foundBearer = true
		}
	}
	if !foundIPAllowlist || !foundBearer {
		t.Errorf("auth diagnostic detail does not mention IP allowlist and Bearer token. Errors: %v", resp.Diagnostics.Errors())
	}
}
