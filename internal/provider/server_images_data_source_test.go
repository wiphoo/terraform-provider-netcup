package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

func TestServerImagesDataSource_PopulatedList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/servers/42/imageflavours" {
			t.Errorf("path = %q, want /v1/servers/42/imageflavours", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[
			{"id":1,"name":"debian-12","alias":"Debian 12","text":"Debian 12 (Bookworm)","image":{"id":100,"name":"debian-bookworm"}},
			{"id":2,"name":"ubuntu-22","alias":"Ubuntu 22.04","text":"Ubuntu 22.04 LTS","image":null}
		]`))
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok123"))
	ctx := context.Background()
	ds := NewServerImagesDataSource().(datasource.DataSourceWithConfigure)

	var configResp datasource.ConfigureResponse
	ds.Configure(ctx, datasource.ConfigureRequest{ProviderData: client}, &configResp)
	if configResp.Diagnostics.HasError() {
		t.Fatalf("Configure() unexpected diagnostics: %v", configResp.Diagnostics.Errors())
	}

	var schemaResp datasource.SchemaResponse
	ds.Schema(ctx, datasource.SchemaRequest{}, &schemaResp)

	req := readRequest(t, schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "42"),
	})

	var resp datasource.ReadResponse
	resp.State.Schema = schemaResp.Schema

	ds.Read(ctx, req, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var state serverImagesDataSourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	if len(state.Images) != 2 {
		t.Fatalf("got %d images, want 2", len(state.Images))
	}

	img1 := state.Images[0]
	if img1.ID.ValueInt64() != 1 {
		t.Errorf("img1.ID = %d, want 1", img1.ID.ValueInt64())
	}
	if img1.Name.ValueString() != "debian-12" {
		t.Errorf("img1.Name = %q, want debian-12", img1.Name.ValueString())
	}
	if img1.Alias.ValueString() != "Debian 12" {
		t.Errorf("img1.Alias = %q, want Debian 12", img1.Alias.ValueString())
	}
	if img1.Text.ValueString() != "Debian 12 (Bookworm)" {
		t.Errorf("img1.Text = %q, want Debian 12 (Bookworm)", img1.Text.ValueString())
	}
	if img1.Image == nil {
		t.Fatal("img1.Image = nil, want non-nil")
	}
	if img1.Image.ID.ValueInt64() != 100 {
		t.Errorf("img1.Image.ID = %d, want 100", img1.Image.ID.ValueInt64())
	}
	if img1.Image.Name.ValueString() != "debian-bookworm" {
		t.Errorf("img1.Image.Name = %q, want debian-bookworm", img1.Image.Name.ValueString())
	}

	img2 := state.Images[1]
	if img2.ID.ValueInt64() != 2 {
		t.Errorf("img2.ID = %d, want 2", img2.ID.ValueInt64())
	}
	if img2.Name.ValueString() != "ubuntu-22" {
		t.Errorf("img2.Name = %q, want ubuntu-22", img2.Name.ValueString())
	}
	if img2.Image != nil {
		t.Errorf("img2.Image = %v, want nil", img2.Image)
	}

	if state.ServerID.ValueString() != "42" {
		t.Errorf("state.ServerID = %q, want 42", state.ServerID.ValueString())
	}
}

func TestServerImagesDataSource_EmptyList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok123"))
	ctx := context.Background()
	ds := NewServerImagesDataSource().(datasource.DataSourceWithConfigure)

	var configResp datasource.ConfigureResponse
	ds.Configure(ctx, datasource.ConfigureRequest{ProviderData: client}, &configResp)
	if configResp.Diagnostics.HasError() {
		t.Fatalf("Configure() unexpected diagnostics: %v", configResp.Diagnostics.Errors())
	}

	var schemaResp datasource.SchemaResponse
	ds.Schema(ctx, datasource.SchemaRequest{}, &schemaResp)

	req := readRequest(t, schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "99"),
	})

	var resp datasource.ReadResponse
	resp.State.Schema = schemaResp.Schema

	ds.Read(ctx, req, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var state serverImagesDataSourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	if len(state.Images) != 0 {
		t.Errorf("got %d images, want 0", len(state.Images))
	}
}

func TestServerImagesDataSource_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"invalid token"}`))
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("bad-token"))
	ctx := context.Background()
	ds := NewServerImagesDataSource().(datasource.DataSourceWithConfigure)

	var configResp datasource.ConfigureResponse
	ds.Configure(ctx, datasource.ConfigureRequest{ProviderData: client}, &configResp)
	if configResp.Diagnostics.HasError() {
		t.Fatalf("Configure() unexpected diagnostics: %v", configResp.Diagnostics.Errors())
	}

	var schemaResp datasource.SchemaResponse
	ds.Schema(ctx, datasource.SchemaRequest{}, &schemaResp)

	req := readRequest(t, schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "1"),
	})

	var resp datasource.ReadResponse
	resp.State.Schema = schemaResp.Schema

	ds.Read(ctx, req, &resp)

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
