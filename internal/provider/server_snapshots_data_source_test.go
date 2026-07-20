package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// snapshotsReadRequest builds a datasource.ReadRequest for netcup_server_snapshots with server_id set.
func snapshotsReadRequest(t *testing.T, schemaResp datasource.SchemaResponse, serverID string) datasource.ReadRequest {
	t.Helper()
	ctx := context.Background()
	objType := schemaResp.Schema.Type().TerraformType(ctx)
	objAttrs := objType.(tftypes.Object).AttributeTypes

	raw := map[string]tftypes.Value{}
	for name, attrType := range objAttrs {
		if name == "server_id" {
			raw[name] = tftypes.NewValue(tftypes.String, serverID)
		} else {
			raw[name] = tftypes.NewValue(attrType, nil)
		}
	}

	return datasource.ReadRequest{
		Config: tfsdk.Config{
			Raw:    tftypes.NewValue(objType, raw),
			Schema: schemaResp.Schema,
		},
	}
}

func newSnapshotsDSClient(t *testing.T, srv *httptest.Server) datasource.DataSourceWithConfigure {
	t.Helper()
	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok123"))
	ds := NewServerSnapshotsDataSource().(datasource.DataSourceWithConfigure)
	ctx := context.Background()

	var configResp datasource.ConfigureResponse
	ds.Configure(ctx, datasource.ConfigureRequest{ProviderData: client}, &configResp)
	if configResp.Diagnostics.HasError() {
		t.Fatalf("Configure() unexpected diagnostics: %v", configResp.Diagnostics.Errors())
	}
	return ds
}

func TestServerSnapshotsDataSource_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/servers/42/snapshots" {
			t.Errorf("path = %q, want /v1/servers/42/snapshots", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[
			{
				"uuid": "snap-001",
				"name": "daily-backup",
				"description": "Daily backup snapshot",
				"disks": ["disk-a", "disk-b"],
				"creationTime": "2024-01-15T10:30:00Z",
				"state": "available",
				"online": false,
				"exported": true,
				"exportedSizeInKiB": 102400
			},
			{
				"uuid": "snap-002",
				"name": "manual-snap",
				"description": null,
				"disks": [],
				"creationTime": "2024-01-20T08:00:00Z",
				"state": "creating",
				"online": true,
				"exported": false,
				"exportedSizeInKiB": null
			}
		]`))
	}))
	defer srv.Close()

	ctx := context.Background()
	ds := newSnapshotsDSClient(t, srv)

	var schemaResp datasource.SchemaResponse
	ds.Schema(ctx, datasource.SchemaRequest{}, &schemaResp)

	req := snapshotsReadRequest(t, schemaResp, "42")

	var resp datasource.ReadResponse
	resp.State.Schema = schemaResp.Schema

	ds.Read(ctx, req, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var state serverSnapshotsDataSourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	if len(state.Snapshots) != 2 {
		t.Fatalf("got %d snapshots, want 2", len(state.Snapshots))
	}

	// First snapshot: fully populated.
	s1 := state.Snapshots[0]
	if s1.UUID.ValueString() != "snap-001" {
		t.Errorf("s1.UUID = %q, want snap-001", s1.UUID.ValueString())
	}
	if s1.Name.ValueString() != "daily-backup" {
		t.Errorf("s1.Name = %q, want daily-backup", s1.Name.ValueString())
	}
	if s1.Description.IsNull() {
		t.Error("s1.Description is null, want non-null")
	} else if s1.Description.ValueString() != "Daily backup snapshot" {
		t.Errorf("s1.Description = %q, want 'Daily backup snapshot'", s1.Description.ValueString())
	}
	if s1.State.ValueString() != "available" {
		t.Errorf("s1.State = %q, want available", s1.State.ValueString())
	}
	if s1.Online.ValueBool() {
		t.Error("s1.Online = true, want false")
	}
	if !s1.Exported.ValueBool() {
		t.Error("s1.Exported = false, want true")
	}
	if s1.ExportedSizeInKiB.IsNull() {
		t.Error("s1.ExportedSizeInKiB is null, want non-null")
	} else if s1.ExportedSizeInKiB.ValueInt64() != 102400 {
		t.Errorf("s1.ExportedSizeInKiB = %d, want 102400", s1.ExportedSizeInKiB.ValueInt64())
	}

	// Verify disks list.
	var disks []string
	if diags := s1.Disks.ElementsAs(ctx, &disks, false); diags.HasError() {
		t.Fatalf("s1.Disks.ElementsAs: %v", diags.Errors())
	}
	if len(disks) != 2 || disks[0] != "disk-a" || disks[1] != "disk-b" {
		t.Errorf("s1.Disks = %v, want [disk-a disk-b]", disks)
	}

	// Verify creation_time is non-empty.
	if s1.CreationTime.ValueString() == "" {
		t.Error("s1.CreationTime is empty, want non-empty")
	}

	// Second snapshot: null description and null exported size.
	s2 := state.Snapshots[1]
	if s2.UUID.ValueString() != "snap-002" {
		t.Errorf("s2.UUID = %q, want snap-002", s2.UUID.ValueString())
	}
	if !s2.Description.IsNull() {
		t.Errorf("s2.Description = %q, want null", s2.Description.ValueString())
	}
	if !s2.ExportedSizeInKiB.IsNull() {
		t.Errorf("s2.ExportedSizeInKiB = %d, want null", s2.ExportedSizeInKiB.ValueInt64())
	}
	if !s2.Online.ValueBool() {
		t.Error("s2.Online = false, want true")
	}
	if s2.Exported.ValueBool() {
		t.Error("s2.Exported = true, want false")
	}

	// Verify empty disks list.
	var disks2 []string
	if diags := s2.Disks.ElementsAs(ctx, &disks2, false); diags.HasError() {
		t.Fatalf("s2.Disks.ElementsAs: %v", diags.Errors())
	}
	if len(disks2) != 0 {
		t.Errorf("s2.Disks = %v, want empty", disks2)
	}
}

func TestServerSnapshotsDataSource_EmptyList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/servers/99/snapshots" {
			t.Errorf("path = %q, want /v1/servers/99/snapshots", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	ctx := context.Background()
	ds := newSnapshotsDSClient(t, srv)

	var schemaResp datasource.SchemaResponse
	ds.Schema(ctx, datasource.SchemaRequest{}, &schemaResp)

	req := snapshotsReadRequest(t, schemaResp, "99")

	var resp datasource.ReadResponse
	resp.State.Schema = schemaResp.Schema

	ds.Read(ctx, req, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	var state serverSnapshotsDataSourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}

	if len(state.Snapshots) != 0 {
		t.Errorf("got %d snapshots, want 0", len(state.Snapshots))
	}
}

func TestServerSnapshotsDataSource_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"invalid token"}`))
	}))
	defer srv.Close()

	ctx := context.Background()
	ds := newSnapshotsDSClient(t, srv)

	var schemaResp datasource.SchemaResponse
	ds.Schema(ctx, datasource.SchemaRequest{}, &schemaResp)

	req := snapshotsReadRequest(t, schemaResp, "1")

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

func TestServerSnapshotsDataSource_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"server not found"}`))
	}))
	defer srv.Close()

	ctx := context.Background()
	ds := newSnapshotsDSClient(t, srv)

	var schemaResp datasource.SchemaResponse
	ds.Schema(ctx, datasource.SchemaRequest{}, &schemaResp)

	req := snapshotsReadRequest(t, schemaResp, "999")

	var resp datasource.ReadResponse
	resp.State.Schema = schemaResp.Schema

	ds.Read(ctx, req, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("Read() expected error diagnostic for 404, got none")
	}
	var foundNotFound bool
	for _, d := range resp.Diagnostics.Errors() {
		if strings.Contains(d.Detail(), "not found") || strings.Contains(d.Summary(), "not found") {
			foundNotFound = true
		}
	}
	if !foundNotFound {
		t.Errorf("404 diagnostic does not mention 'not found'. Errors: %v", resp.Diagnostics.Errors())
	}
}
