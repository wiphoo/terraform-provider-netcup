package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// configureServerSnapshotResource wires up a serverSnapshotResource against the
// given client and returns the configured resource and its schema response.
func configureServerSnapshotResource(t *testing.T, client *netcup.Client) (resource.ResourceWithConfigure, resource.SchemaResponse) {
	t.Helper()
	r := NewServerSnapshotResource().(resource.ResourceWithConfigure)
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

// snapshotStringListVal builds a tftypes list-of-string value for the disks attribute.
func snapshotStringListVal(vals ...string) tftypes.Value {
	elems := make([]tftypes.Value, len(vals))
	for i, v := range vals {
		elems[i] = tftypes.NewValue(tftypes.String, v)
	}
	return tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, elems)
}

// snapshotListEntry renders one SnapshotMinimal for a mock listing response.
func snapshotListEntry(t *testing.T, uuid, name, description, creationTime, serverState, disk string, online, exported bool, sizeKiB *int64) string {
	t.Helper()
	disks := []string{}
	if disk != "" {
		disks = []string{disk}
	}
	m := map[string]any{
		"uuid":              uuid,
		"name":              name,
		"disks":             disks,
		"creationTime":      creationTime,
		"state":             serverState,
		"online":            online,
		"exported":          exported,
		"exportedSizeInKiB": sizeKiB,
	}
	if description != "" {
		m["description"] = description
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal listing entry: %v", err)
	}
	return string(b)
}

func TestServerSnapshotResource_Schema(t *testing.T) {
	_, schemaResp := configureServerSnapshotResource(t, netcup.New())
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("Schema() diagnostics = %v", schemaResp.Diagnostics)
	}
	s := schemaResp.Schema

	type wantAttr struct {
		required        bool
		optional        bool
		computed        bool
		requiresReplace bool
	}
	want := map[string]wantAttr{
		"server_id":            {required: true, requiresReplace: true},
		"name":                 {required: true, requiresReplace: true},
		"description":          {optional: true, requiresReplace: true},
		"disk_name":            {optional: true, requiresReplace: true},
		"online_snapshot":      {optional: true, computed: true, requiresReplace: true},
		"wait":                 {optional: true, computed: true, requiresReplace: true},
		"id":                   {computed: true},
		"uuid":                 {computed: true},
		"creation_time":        {computed: true},
		"state":                {computed: true},
		"online":               {computed: true},
		"exported":             {computed: true},
		"exported_size_in_kib": {computed: true},
		"disks":                {computed: true},
		"task_id":              {computed: true},
	}
	if len(s.Attributes) != len(want) {
		t.Fatalf("schema has %d attributes, want %d: %v", len(s.Attributes), len(want), s.Attributes)
	}
	for name, w := range want {
		attr, ok := s.Attributes[name]
		if !ok {
			t.Errorf("missing attribute %q", name)
			continue
		}
		var required, optional, computed bool
		var modifiers int
		var defaultSet bool
		switch a := attr.(type) {
		case schema.StringAttribute:
			required, optional, computed = a.Required, a.Optional, a.Computed
			modifiers = len(a.PlanModifiers)
		case schema.BoolAttribute:
			required, optional, computed = a.Required, a.Optional, a.Computed
			modifiers = len(a.PlanModifiers)
			defaultSet = a.Default != nil
		case schema.Int64Attribute:
			required, optional, computed = a.Required, a.Optional, a.Computed
			modifiers = len(a.PlanModifiers)
		case schema.ListAttribute:
			required, optional, computed = a.Required, a.Optional, a.Computed
			modifiers = len(a.PlanModifiers)
			if a.ElementType != types.StringType {
				t.Errorf("%s: element type = %v, want string", name, a.ElementType)
			}
		default:
			t.Errorf("%s: unexpected attribute type %T", name, attr)
			continue
		}
		if required != w.required || optional != w.optional || computed != w.computed {
			t.Errorf("%s: (required,optional,computed) = (%v,%v,%v), want (%v,%v,%v)",
				name, required, optional, computed, w.required, w.optional, w.computed)
		}
		if w.requiresReplace && modifiers == 0 {
			t.Errorf("%s: expected a RequiresReplace plan modifier, got none", name)
		}
		if !w.requiresReplace && modifiers != 0 {
			t.Errorf("%s: unexpected plan modifiers", name)
		}
		if w.computed && name != "online_snapshot" && name != "wait" && defaultSet {
			t.Errorf("%s: unexpected default on a plain computed attribute", name)
		}
	}
	// The defaults must be present exactly where the schema declares them.
	if sa, ok := s.Attributes["online_snapshot"].(schema.BoolAttribute); !ok || sa.Default == nil {
		t.Error("online_snapshot: expected a default value")
	}
	if sa, ok := s.Attributes["wait"].(schema.BoolAttribute); !ok || sa.Default == nil {
		t.Error("wait: expected a default value")
	}
	// name carries the length validator.
	if na, ok := s.Attributes["name"].(schema.StringAttribute); !ok || len(na.Validators) != 1 {
		t.Error("name: expected exactly one validator (length)")
	}
}

func TestValidateSnapshotMode(t *testing.T) {
	cases := []struct {
		name     string
		online   types.Bool
		diskName types.String
		wantErr  bool
	}{
		{"offline default with disk", types.BoolNull(), types.StringValue("system"), false},
		{"offline explicit false with disk", types.BoolValue(false), types.StringValue("system"), false},
		{"online without disk", types.BoolValue(true), types.StringNull(), false},
		{"online with disk", types.BoolValue(true), types.StringValue("system"), true},
		{"offline default without disk", types.BoolNull(), types.StringNull(), true},
		{"offline explicit false without disk", types.BoolValue(false), types.StringNull(), true},
		{"offline whitespace-only disk", types.BoolNull(), types.StringValue("   "), true},
		{"unknown online skips validation", types.BoolUnknown(), types.StringNull(), false},
		{"unknown disk skips validation", types.BoolValue(false), types.StringUnknown(), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var diags diag.Diagnostics
			validateSnapshotMode(tc.online, tc.diskName, &diags)
			if diags.HasError() != tc.wantErr {
				t.Fatalf("diagnostics = %v, wantErr = %v", diags, tc.wantErr)
			}
		})
	}
}

func TestIsDefinitiveSnapshotRejection(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"pre-dispatch", fmt.Errorf("%w: snapshot name is required", netcup.ErrPreDispatch), true},
		{"422 validation", &netcup.APIError{StatusCode: 422, Status: "422 Unprocessable Entity"}, true},
		{"404 not found", &netcup.APIError{StatusCode: 404, Status: "404 Not Found"}, true},
		{"401 unauthorized", &netcup.APIError{StatusCode: 401, Status: "401 Unauthorized"}, true},
		{"502 bad gateway", &netcup.APIError{StatusCode: 502, Status: "502 Bad Gateway"}, false},
		{"500 server error", &netcup.APIError{StatusCode: 500, Status: "500 Internal Server Error"}, false},
		{"transport error", errors.New("connection refused"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDefinitiveSnapshotRejection(tc.err); got != tc.want {
				t.Errorf("isDefinitiveSnapshotRejection(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestApplySnapshot(t *testing.T) {
	desc := "backup"
	size := int64(12345)
	created := time.Date(2026, 1, 2, 3, 4, 5, 123000000, time.UTC)
	s := netcup.SnapshotMinimal{
		UUID:              "snap-uuid-1",
		Name:              "pre-upgrade",
		Description:       &desc,
		Disks:             []string{"system", "data"},
		CreationTime:      created,
		State:             "SHUTOFF",
		Online:            false,
		Exported:          true,
		ExportedSizeInKiB: &size,
	}
	m := &serverSnapshotResourceModel{TaskID: types.StringValue("keep-me")}
	if diags := applySnapshot(context.Background(), m, s); diags.HasError() {
		t.Fatalf("applySnapshot() diagnostics = %v", diags)
	}
	if m.ID.ValueString() != "snap-uuid-1" || m.UUID.ValueString() != "snap-uuid-1" {
		t.Errorf("id/uuid = %q/%q, want snap-uuid-1", m.ID.ValueString(), m.UUID.ValueString())
	}
	if m.CreationTime.ValueString() != "2026-01-02T03:04:05.123Z" {
		t.Errorf("creation_time = %q, want RFC3339Nano of the API timestamp", m.CreationTime.ValueString())
	}
	if m.State.ValueString() != "SHUTOFF" || m.Online.ValueBool() || !m.Exported.ValueBool() {
		t.Errorf("state/online/exported = %v/%v/%v, want SHUTOFF/false/true", m.State.ValueString(), m.Online.ValueBool(), m.Exported.ValueBool())
	}
	if !m.ExportedSizeInKiB.Equal(types.Int64Value(12345)) {
		t.Errorf("exported_size_in_kib = %v, want 12345", m.ExportedSizeInKiB)
	}
	var disks []string
	if diags := m.Disks.ElementsAs(context.Background(), &disks, false); diags.HasError() {
		t.Fatalf("disks elements: %v", diags)
	}
	if len(disks) != 2 || disks[0] != "system" || disks[1] != "data" {
		t.Errorf("disks = %v, want [system data]", disks)
	}
	// task_id (and inputs) are untouched.
	if m.TaskID.ValueString() != "keep-me" {
		t.Errorf("task_id = %q, want keep-me (untouched)", m.TaskID.ValueString())
	}

	// A nil exported size maps to a null attribute.
	s.Exported = false
	s.ExportedSizeInKiB = nil
	m2 := &serverSnapshotResourceModel{}
	if diags := applySnapshot(context.Background(), m2, s); diags.HasError() {
		t.Fatalf("applySnapshot() diagnostics = %v", diags)
	}
	if !m2.ExportedSizeInKiB.IsNull() {
		t.Errorf("exported_size_in_kib = %v, want null", m2.ExportedSizeInKiB)
	}
}

func TestServerSnapshotResource_Create_Async(t *testing.T) {
	taskPolled := false
	listed := false
	var rawBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/servers/123/snapshots":
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &rawBody); err != nil {
				t.Errorf("decode request body: %v", err)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-1","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-1":
			taskPolled = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-1","state":"FINISHED"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			listed = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[" + snapshotListEntry(t, "snap-uuid-1", "pre-upgrade", "d", "2026-01-02T03:04:05Z", "SHUTOFF", "system", false, true, int64Ptr(12345)) + "]"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":   tftypes.NewValue(tftypes.String, "123"),
		"name":        tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"description": tftypes.NewValue(tftypes.String, "d"),
		"disk_name":   tftypes.NewValue(tftypes.String, "system"),
		"wait":        tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Create() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if !taskPolled || !listed {
		t.Errorf("expected the task to be polled (%v) and the snapshots listed (%v) with wait=true", taskPolled, listed)
	}
	// The request body carries the configured inputs; an offline snapshot omits
	// onlineSnapshot entirely (the SDK field is omitempty and defaults to false).
	if rawBody["name"] != "pre-upgrade" || rawBody["description"] != "d" || rawBody["diskName"] != "system" {
		t.Errorf("request body = %v, want name/description/diskName set", rawBody)
	}
	if _, ok := rawBody["onlineSnapshot"]; ok {
		t.Errorf("request body = %v, want onlineSnapshot omitted for an offline snapshot", rawBody)
	}

	var state serverSnapshotResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if state.ID.ValueString() != "snap-uuid-1" || state.UUID.ValueString() != "snap-uuid-1" {
		t.Errorf("id/uuid = %q/%q, want snap-uuid-1 (adopted from the listing)", state.ID.ValueString(), state.UUID.ValueString())
	}
	if state.TaskID.ValueString() != "task-1" {
		t.Errorf("task_id = %q, want task-1", state.TaskID.ValueString())
	}
	if state.CreationTime.ValueString() != "2026-01-02T03:04:05Z" || state.State.ValueString() != "SHUTOFF" {
		t.Errorf("creation_time/state = %q/%q, want 2026-01-02T03:04:05Z/SHUTOFF", state.CreationTime.ValueString(), state.State.ValueString())
	}
	if state.Online.ValueBool() {
		t.Errorf("online = %v, want false", state.Online.ValueBool())
	}
	if !state.Exported.ValueBool() {
		t.Errorf("exported = %v, want true", state.Exported.ValueBool())
	}
	if !state.ExportedSizeInKiB.Equal(types.Int64Value(12345)) {
		t.Errorf("exported_size_in_kib = %v, want 12345", state.ExportedSizeInKiB)
	}
	var disks []string
	resp.Diagnostics.Append(state.Disks.ElementsAs(ctx, &disks, false)...)
	if len(disks) != 1 || disks[0] != "system" {
		t.Errorf("disks = %v, want [system]", disks)
	}
}

func TestServerSnapshotResource_Create_NoWait(t *testing.T) {
	polled := false
	listed := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/servers/123/snapshots":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-9","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			listed = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		case strings.HasPrefix(r.URL.Path, "/v1/tasks/"):
			polled = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-9","state":"FINISHED"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "123"),
		"name":      tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"disk_name": tftypes.NewValue(tftypes.String, "system"),
		"wait":      tftypes.NewValue(tftypes.Bool, false),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Create() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if polled || listed {
		t.Errorf("wait=false must not poll the task (%v) nor list snapshots (%v)", polled, listed)
	}
	var state serverSnapshotResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if state.TaskID.ValueString() != "task-9" {
		t.Errorf("task_id = %q, want task-9", state.TaskID.ValueString())
	}
	if !state.ID.IsNull() || !state.UUID.IsNull() {
		t.Errorf("id/uuid = %v/%v, want null (unconfirmed until the next refresh)", state.ID, state.UUID)
	}
}

func TestServerSnapshotResource_Create_TaskFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/servers/123/snapshots":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-err","state":"PENDING"}`))
		case r.URL.Path == "/v1/tasks/task-err":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-err","state":"ERROR","message":"snapshot failed"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "123"),
		"name":      tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"disk_name": tftypes.NewValue(tftypes.String, "system"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic on terminal task failure")
	}
	if !resp.State.Raw.IsNull() {
		t.Errorf("expected null state after task failure, got %v", resp.State.Raw)
	}
}

func TestServerSnapshotResource_Create_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/servers/123/snapshots" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"code":"VALIDATION","message":"disk not found"}`))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "123"),
		"name":      tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"disk_name": tftypes.NewValue(tftypes.String, "nope"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic on 422 API error")
	}
	if !resp.State.Raw.IsNull() {
		t.Errorf("expected null state after API error, got %v", resp.State.Raw)
	}
}

func TestServerSnapshotResource_Create_5xxAmbiguous(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/servers/123/snapshots" {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"message":"upstream timeout"}`))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "123"),
		"name":      tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"disk_name": tftypes.NewValue(tftypes.String, "system"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("ambiguous 5xx must not error; got: %v", resp.Diagnostics.Errors())
	}
	if len(resp.Diagnostics.Warnings()) == 0 {
		t.Error("expected a warning diagnostic for an ambiguous 5xx response")
	}
	if resp.State.Raw.IsNull() {
		t.Error("expected state to be persisted after an ambiguous 5xx response")
	}
	var state serverSnapshotResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if !state.ID.IsNull() || !state.UUID.IsNull() || !state.TaskID.IsNull() {
		t.Errorf("id/uuid/task_id = %v/%v/%v, want all null (unconfirmed)", state.ID, state.UUID, state.TaskID)
	}
}

func TestServerSnapshotResource_Create_DispatchDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/servers/123/snapshots" {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":`)) // truncated body → decode error
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "123"),
		"name":      tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"disk_name": tftypes.NewValue(tftypes.String, "system"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	// A decode failure AFTER dispatch is ambiguous: the snapshot may exist.
	if resp.Diagnostics.HasError() {
		t.Fatalf("ambiguous post-dispatch failure must not error; got: %v", resp.Diagnostics.Errors())
	}
	if len(resp.Diagnostics.Warnings()) == 0 {
		t.Error("expected a warning diagnostic for an ambiguous dispatch outcome")
	}
	if resp.State.Raw.IsNull() {
		t.Error("expected state to be persisted after an ambiguous dispatch failure")
	}
	var state serverSnapshotResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if !state.ID.IsNull() || !state.TaskID.IsNull() {
		t.Errorf("id/task_id = %v/%v, want null (no 202 body decoded)", state.ID, state.TaskID)
	}
}

func TestServerSnapshotResource_Create_ListNotFoundAfterFinish(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/servers/123/snapshots":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-1","state":"PENDING"}`))
		case r.URL.Path == "/v1/tasks/task-1":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-1","state":"FINISHED"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "123"),
		"name":      tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"disk_name": tftypes.NewValue(tftypes.String, "system"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	// Finished but unlisted: persist + warn, not an error.
	if resp.Diagnostics.HasError() {
		t.Fatalf("finished-but-unlisted must not error; got: %v", resp.Diagnostics.Errors())
	}
	if len(resp.Diagnostics.Warnings()) == 0 {
		t.Error("expected a warning diagnostic when the finished snapshot is not listed")
	}
	if resp.State.Raw.IsNull() {
		t.Error("expected state to be persisted when the finished snapshot is not listed")
	}
	var state serverSnapshotResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if state.TaskID.ValueString() != "task-1" {
		t.Errorf("task_id = %q, want task-1 (the accepted task is known)", state.TaskID.ValueString())
	}
	if !state.UUID.IsNull() {
		t.Errorf("uuid = %v, want null (not adoptable from the listing)", state.UUID)
	}
}

func TestServerSnapshotResource_Create_ListErrorAfterFinish(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/servers/123/snapshots":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-1","state":"PENDING"}`))
		case r.URL.Path == "/v1/tasks/task-1":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-1","state":"FINISHED"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"boom"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "123"),
		"name":      tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"disk_name": tftypes.NewValue(tftypes.String, "system"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("a listing failure after a finished task must not error; got: %v", resp.Diagnostics.Errors())
	}
	if len(resp.Diagnostics.Warnings()) == 0 {
		t.Error("expected a warning diagnostic when the post-create listing fails")
	}
	if resp.State.Raw.IsNull() {
		t.Error("expected state to be persisted when the post-create listing fails")
	}
}

func TestServerSnapshotResource_Create_OnlineSnapshotBody(t *testing.T) {
	var rawBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/servers/123/snapshots":
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &rawBody)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-2","state":"PENDING"}`))
		case r.URL.Path == "/v1/tasks/task-2":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-2","state":"FINISHED"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[" + snapshotListEntry(t, "snap-uuid-2", "online-snap", "", "2026-01-02T03:04:05Z", "RUNNING", "", true, false, nil) + "]"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "123"),
		"name":            tftypes.NewValue(tftypes.String, "online-snap"),
		"online_snapshot": tftypes.NewValue(tftypes.Bool, true),
		"wait":            tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Create() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if rawBody["onlineSnapshot"] != true {
		t.Errorf("request body = %v, want onlineSnapshot true", rawBody)
	}
	if _, ok := rawBody["diskName"]; ok {
		t.Errorf("request body = %v, want diskName omitted for an online snapshot", rawBody)
	}
	var state serverSnapshotResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if state.UUID.ValueString() != "snap-uuid-2" || !state.Online.ValueBool() {
		t.Errorf("uuid/online = %v/%v, want snap-uuid-2/true", state.UUID.ValueString(), state.Online.ValueBool())
	}
}

func TestServerSnapshotResource_Read_ByUUID(t *testing.T) {
	// Two snapshots share the name; the OTHER one is newer. A name-newest match
	// would adopt the wrong snapshot — Read must match by UUID.
	entries := []string{
		snapshotListEntry(t, "other-uuid", "pre-upgrade", "other", "2026-06-01T00:00:00Z", "SHUTOFF", "system", false, false, nil),
		snapshotListEntry(t, "snap-uuid-1", "pre-upgrade", "api-desc", "2026-01-02T03:04:05Z", "SHUTOFF", "system", false, true, int64Ptr(12345)),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[" + strings.Join(entries, ",") + "]"))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "123"),
		"name":            tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"description":     tftypes.NewValue(tftypes.String, "cfg-desc"),
		"disk_name":       tftypes.NewValue(tftypes.String, "system"),
		"online_snapshot": tftypes.NewValue(tftypes.Bool, false),
		"wait":            tftypes.NewValue(tftypes.Bool, true),
		"id":              tftypes.NewValue(tftypes.String, "snap-uuid-1"),
		"uuid":            tftypes.NewValue(tftypes.String, "snap-uuid-1"),
		"task_id":         tftypes.NewValue(tftypes.String, "task-1"),
	})

	var resp resource.ReadResponse
	resp.State = state
	r.(resource.Resource).Read(ctx, resource.ReadRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	var got serverSnapshotResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &got)...)
	if got.UUID.ValueString() != "snap-uuid-1" {
		t.Errorf("uuid = %q, want snap-uuid-1 (matched by UUID, not name-newest)", got.UUID.ValueString())
	}
	// Inputs are never overwritten by the API; computed fields are refreshed.
	if got.Description.ValueString() != "cfg-desc" {
		t.Errorf("description = %q, want cfg-desc (config value preserved)", got.Description.ValueString())
	}
	if !got.Exported.ValueBool() || !got.ExportedSizeInKiB.Equal(types.Int64Value(12345)) {
		t.Errorf("exported/exported_size_in_kib = %v/%v, want true/12345 (refreshed from API)", got.Exported.ValueBool(), got.ExportedSizeInKiB)
	}
	if got.TaskID.ValueString() != "task-1" {
		t.Errorf("task_id = %q, want task-1 (untouched by Read)", got.TaskID.ValueString())
	}
}

func TestServerSnapshotResource_Read_NameFallback(t *testing.T) {
	// Import: only server_id + name are seeded. Read must resolve the UUID by
	// name and backfill the null inputs and computed fields.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[" + snapshotListEntry(t, "snap-uuid-2", "pre-upgrade", "d", "2026-01-02T03:04:05Z", "SHUTOFF", "system", false, false, nil) + "]"))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "123"),
		"name":      tftypes.NewValue(tftypes.String, "pre-upgrade"),
	})

	var resp resource.ReadResponse
	resp.State = state
	r.(resource.Resource).Read(ctx, resource.ReadRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	var got serverSnapshotResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &got)...)
	if got.ID.ValueString() != "snap-uuid-2" || got.UUID.ValueString() != "snap-uuid-2" {
		t.Errorf("id/uuid = %q/%q, want snap-uuid-2 (resolved by name)", got.ID.ValueString(), got.UUID.ValueString())
	}
	if got.Description.ValueString() != "d" {
		t.Errorf("description = %q, want d (backfilled from API)", got.Description.ValueString())
	}
	if got.DiskName.ValueString() != "system" {
		t.Errorf("disk_name = %q, want system (backfilled: offline snapshot with a single disk)", got.DiskName.ValueString())
	}
	if got.OnlineSnapshot.IsNull() || got.OnlineSnapshot.ValueBool() {
		t.Errorf("online_snapshot = %v, want false (backfilled from API)", got.OnlineSnapshot)
	}
	if got.Wait.IsNull() || !got.Wait.ValueBool() {
		t.Errorf("wait = %v, want true (normalized default)", got.Wait)
	}
	if !got.ExportedSizeInKiB.IsNull() {
		t.Errorf("exported_size_in_kib = %v, want null (not exported)", got.ExportedSizeInKiB)
	}
}

func TestServerSnapshotResource_Read_Gone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[" + snapshotListEntry(t, "other-uuid", "other-name", "", "2026-01-01T00:00:00Z", "SHUTOFF", "system", false, false, nil) + "]"))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "123"),
		"name":      tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"uuid":      tftypes.NewValue(tftypes.String, "gone-uuid"),
	})

	var resp resource.ReadResponse
	resp.State = state
	r.(resource.Resource).Read(ctx, resource.ReadRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if !resp.State.Raw.IsNull() {
		t.Error("expected the resource to be removed from state when the snapshot is gone")
	}
}

func TestServerSnapshotResource_Read_List404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"no such server"}`))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "123"),
		"name":      tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"uuid":      tftypes.NewValue(tftypes.String, "snap-uuid-1"),
	})

	var resp resource.ReadResponse
	resp.State = state
	r.(resource.Resource).Read(ctx, resource.ReadRequest{State: state}, &resp)

	// The server is gone, so its snapshots are gone too: remove, don't error.
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if !resp.State.Raw.IsNull() {
		t.Error("expected the resource to be removed from state when the server 404s")
	}
}

func TestServerSnapshotResource_Read_ListError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"boom"}`))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "123"),
		"name":      tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"uuid":      tftypes.NewValue(tftypes.String, "snap-uuid-1"),
	})

	var resp resource.ReadResponse
	resp.State = state
	r.(resource.Resource).Read(ctx, resource.ReadRequest{State: state}, &resp)

	// A listing failure (other than 404) does NOT prove the snapshot is gone.
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic when the snapshot listing fails")
	}
	if resp.State.Raw.IsNull() {
		t.Error("expected the resource to stay in state when the listing fails")
	}
}

func TestServerSnapshotResource_Delete_Success(t *testing.T) {
	deleted := false
	polled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/servers/123/snapshots/pre-upgrade":
			deleted = true
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-del","state":"PENDING"}`))
		case r.URL.Path == "/v1/tasks/task-del":
			polled = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-del","state":"FINISHED"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "123"),
		"name":      tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
		"uuid":      tftypes.NewValue(tftypes.String, "snap-uuid-1"),
	})

	var resp resource.DeleteResponse
	resp.State = state
	r.(resource.Resource).Delete(ctx, resource.DeleteRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if !deleted {
		t.Error("expected DELETE /v1/servers/123/snapshots/pre-upgrade to be called (name-addressed)")
	}
	if !polled {
		t.Error("expected the delete task to be polled with wait=true")
	}
}

func TestServerSnapshotResource_Delete_Gone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.URL.Path == "/v1/servers/123/snapshots/pre-upgrade" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"no such snapshot"}`))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "123"),
		"name":      tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.DeleteResponse
	resp.State = state
	r.(resource.Resource).Delete(ctx, resource.DeleteRequest{State: state}, &resp)

	// Already gone: the desired end state is reached, so no error.
	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete() must not error on 404 (already gone); got: %v", resp.Diagnostics.Errors())
	}
}

func TestServerSnapshotResource_Delete_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.URL.Path == "/v1/servers/123/snapshots/pre-upgrade" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"boom"}`))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "123"),
		"name":      tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
	})

	var resp resource.DeleteResponse
	resp.State = state
	r.(resource.Resource).Delete(ctx, resource.DeleteRequest{State: state}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic on a 500 delete failure")
	}
}

func TestServerSnapshotResource_Delete_NoWait(t *testing.T) {
	polled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/servers/123/snapshots/pre-upgrade":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-del","state":"PENDING"}`))
		case r.URL.Path == "/v1/tasks/task-del":
			polled = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-del","state":"FINISHED"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "123"),
		"name":      tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"wait":      tftypes.NewValue(tftypes.Bool, false),
	})

	var resp resource.DeleteResponse
	resp.State = state
	r.(resource.Resource).Delete(ctx, resource.DeleteRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if polled {
		t.Error("did not expect the delete task to be polled with wait=false")
	}
}

func TestServerSnapshotResource_Update_CarriesComputed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("Update must not call the API; got %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	plan := resourcePlan(schemaResp, map[string]tftypes.Value{
		"server_id":       tftypes.NewValue(tftypes.String, "123"),
		"name":            tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"disk_name":       tftypes.NewValue(tftypes.String, "system"),
		"online_snapshot": tftypes.NewValue(tftypes.Bool, false),
		"wait":            tftypes.NewValue(tftypes.Bool, true),
	})
	prior := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":            tftypes.NewValue(tftypes.String, "123"),
		"name":                 tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"disk_name":            tftypes.NewValue(tftypes.String, "system"),
		"online_snapshot":      tftypes.NewValue(tftypes.Bool, false),
		"wait":                 tftypes.NewValue(tftypes.Bool, true),
		"id":                   tftypes.NewValue(tftypes.String, "snap-uuid-1"),
		"uuid":                 tftypes.NewValue(tftypes.String, "snap-uuid-1"),
		"task_id":              tftypes.NewValue(tftypes.String, "task-1"),
		"creation_time":        tftypes.NewValue(tftypes.String, "2026-01-02T03:04:05Z"),
		"state":                tftypes.NewValue(tftypes.String, "SHUTOFF"),
		"online":               tftypes.NewValue(tftypes.Bool, false),
		"exported":             tftypes.NewValue(tftypes.Bool, false),
		"exported_size_in_kib": tftypes.NewValue(tftypes.Number, nil),
		"disks":                snapshotStringListVal("system"),
	})

	var resp resource.UpdateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.(resource.Resource).Update(ctx, resource.UpdateRequest{Plan: plan, State: prior}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Update() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	var got serverSnapshotResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &got)...)
	// The computed attributes are carried forward from the prior state, so an
	// in-place update never implies a snapshot operation.
	if got.ID.ValueString() != "snap-uuid-1" || got.UUID.ValueString() != "snap-uuid-1" {
		t.Errorf("id/uuid = %q/%q, want snap-uuid-1 (carried from prior state)", got.ID.ValueString(), got.UUID.ValueString())
	}
	if got.TaskID.ValueString() != "task-1" {
		t.Errorf("task_id = %q, want task-1 (carried from prior state)", got.TaskID.ValueString())
	}
	var disks []string
	resp.Diagnostics.Append(got.Disks.ElementsAs(ctx, &disks, false)...)
	if len(disks) != 1 || disks[0] != "system" {
		t.Errorf("disks = %v, want [system] (carried from prior state)", disks)
	}
}

func TestServerSnapshotResource_ImportState(t *testing.T) {
	r := NewServerSnapshotResource().(resource.ResourceWithImportState)
	_, schemaResp := configureServerSnapshotResource(t, netcup.New())
	ctx := context.Background()

	run := func(id string) *resource.ImportStateResponse {
		var resp resource.ImportStateResponse
		resp.State = resourceState(schemaResp, nil)
		r.ImportState(ctx, resource.ImportStateRequest{ID: id}, &resp)
		return &resp
	}

	valid := run("123:pre-upgrade")
	if valid.Diagnostics.HasError() {
		t.Fatalf("ImportState() unexpected diagnostics: %v", valid.Diagnostics.Errors())
	}
	var serverID, name types.String
	valid.State.GetAttribute(ctx, path.Root("server_id"), &serverID)
	valid.State.GetAttribute(ctx, path.Root("name"), &name)
	if serverID.ValueString() != "123" || name.ValueString() != "pre-upgrade" {
		t.Errorf("import set server_id=%q name=%q, want 123/pre-upgrade", serverID.ValueString(), name.ValueString())
	}

	// The name may contain colons: split on the FIRST colon only.
	colon := run("123:my:snap")
	if colon.Diagnostics.HasError() {
		t.Fatalf("ImportState() unexpected diagnostics: %v", colon.Diagnostics.Errors())
	}
	var colonName types.String
	colon.State.GetAttribute(ctx, path.Root("name"), &colonName)
	if colonName.ValueString() != "my:snap" {
		t.Errorf("import name = %q, want my:snap (split on the first colon)", colonName.ValueString())
	}

	for _, id := range []string{"123", "abc:pre-upgrade", "123:", ":pre-upgrade", ""} {
		resp := run(id)
		if !resp.Diagnostics.HasError() {
			t.Errorf("ImportState(%q): expected an error diagnostic", id)
		}
	}
}

func int64Ptr(v int64) *int64 { return &v }
