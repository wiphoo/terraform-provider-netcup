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
		"create_requested_at":  {computed: true},
		"pre_create_uuids":     {computed: true},
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
	listCalls := 0
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
			// First call is the pre-create reconciliation (no snapshots yet);
			// the second is the post-finish adoption listing.
			listCalls++
			w.WriteHeader(http.StatusOK)
			if listCalls == 1 {
				_, _ = w.Write([]byte(`[]`))
			} else {
				_, _ = w.Write([]byte("[" + snapshotListEntry(t, "snap-uuid-1", "pre-upgrade", "d", "2026-01-02T03:04:05Z", "SHUTOFF", "system", false, true, int64Ptr(12345)) + "]"))
			}
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
	if !taskPolled || listCalls != 2 {
		t.Errorf("expected the task to be polled (%v) and exactly two snapshot listings: pre-create + post-finish (got %d)", taskPolled, listCalls)
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
	listCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/servers/123/snapshots":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-9","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			listCalls++
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
	if polled || listCalls != 1 {
		t.Errorf("wait=false must not poll the task (%v) nor list more than the pre-create reconciliation (got %d listings)", polled, listCalls)
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
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
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
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/servers/123/snapshots":
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"code":"VALIDATION","message":"disk not found"}`))
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
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/servers/123/snapshots":
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"message":"upstream timeout"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			w.WriteHeader(http.StatusOK)
			// A pre-existing snapshot with a DIFFERENT name: the create may
			// be dispatched (no same-name collision).
			_, _ = w.Write([]byte("[" +
				snapshotListEntry(t, "snap-uuid-pre", "old-backup", "", "2026-01-02T02:00:00Z", "SHUTOFF", "system", false, false, nil) +
				"]"))
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
	if state.CreateRequestedAt.IsNull() {
		t.Error("create_requested_at must be recorded so the next refresh can bound adoption")
	}
	var preUUIDs []string
	resp.Diagnostics.Append(state.PreCreateUUIDs.ElementsAs(ctx, &preUUIDs, false)...)
	if len(preUUIDs) != 1 || preUUIDs[0] != "snap-uuid-pre" {
		t.Errorf("pre_create_uuids = %v, want [snap-uuid-pre] (the pre-existing set persisted for identity-based exclusion)", preUUIDs)
	}
}

func TestServerSnapshotResource_Create_DispatchDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/servers/123/snapshots":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":`)) // truncated body → decode error
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
	listCalls := 0
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
			// Pre-create listing and the post-finish listing are both empty.
			listCalls++
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
	listCalls := 0
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
			// The pre-create listing succeeds; the post-finish listing fails.
			listCalls++
			if listCalls == 1 {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`[]`))
				return
			}
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
	listCalls := 0
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
			// Pre-create listing is empty; the post-finish listing carries the
			// new online snapshot.
			listCalls++
			w.WriteHeader(http.StatusOK)
			if listCalls == 1 {
				_, _ = w.Write([]byte(`[]`))
			} else {
				_, _ = w.Write([]byte("[" + snapshotListEntry(t, "snap-uuid-2", "online-snap", "", "2026-01-02T03:04:05Z", "RUNNING", "", true, false, nil) + "]"))
			}
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

// TestServerSnapshotResource_Create_ExcludesPreExistingSnapshot verifies that
// when the server already holds a same-name snapshot, post-create adoption
// takes the NEW snapshot (created after the pre-create reconciliation), not
// the pre-existing one — even though the pre-existing one is the newest by
// name that the old code would have grabbed during a listing lag.
// TestServerSnapshotResource_Create_PreExistingSameNameRefuses verifies that
// a create is refused BEFORE dispatch when the server already holds a
// snapshot with the requested name: a second same-name snapshot would make
// the created one undeletable (deletion is addressed by name).
func TestServerSnapshotResource_Create_PreExistingSameNameRefuses(t *testing.T) {
	postCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/servers/123/snapshots":
			postCalled = true
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-1","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			_, _ = w.Write([]byte("[" + snapshotListEntry(t, "snap-uuid-old", "pre-upgrade", "", "2025-01-01T00:00:00Z", "SHUTOFF", "system", false, false, nil) + "]"))
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
		t.Fatal("expected an error diagnostic when a same-name snapshot already exists")
	}
	if postCalled {
		t.Error("the create request must not be dispatched when a same-name snapshot already exists")
	}
	if !resp.State.Raw.IsNull() {
		t.Error("no state may be persisted when the create is refused before dispatch")
	}
}

// TestServerSnapshotResource_Create_PreListError verifies that a failing
// pre-create listing blocks the create (the existing snapshot set cannot be
// confirmed) and persists no state, so the next apply retries.
func TestServerSnapshotResource_Create_PreListError(t *testing.T) {
	postCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/servers/123/snapshots":
			postCalled = true
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-1","state":"PENDING"}`))
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

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic when the pre-create listing fails")
	}
	if postCalled {
		t.Error("the create must not be dispatched when the pre-create listing fails")
	}
	if !resp.State.Raw.IsNull() {
		t.Errorf("expected null state when the pre-create listing fails, got %v", resp.State.Raw)
	}
}

// TestServerSnapshotResource_Create_PreList404 verifies that a 404 pre-create
// listing (the server itself is gone) blocks the create with an error.
func TestServerSnapshotResource_Create_PreList404(t *testing.T) {
	postCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/servers/123/snapshots":
			postCalled = true
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-1","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"no such server"}`))
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
		t.Fatal("expected an error diagnostic when the server 404s on the pre-create listing")
	}
	if postCalled {
		t.Error("the create must not be dispatched when the server is gone")
	}
	if !resp.State.Raw.IsNull() {
		t.Errorf("expected null state, got %v", resp.State.Raw)
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

// TestServerSnapshotResource_Read_InFlightTaskKeepsState verifies that a
// refresh of an unconfirmed create (null uuid, task still PENDING) keeps the
// state instead of deleting it: the snapshot does not appear in the listing
// while the task is running, so absence is not evidence of failure.
func TestServerSnapshotResource_Read_InFlightTaskKeepsState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/tasks/task-1":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-1","state":"PENDING","startedAt":"2026-01-02T03:00:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[" + snapshotListEntry(t, "snap-uuid-old", "pre-upgrade", "", "2025-01-01T00:00:00Z", "SHUTOFF", "system", false, false, nil) + "]"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, nil),
		"server_id": tftypes.NewValue(tftypes.String, "123"),
		"name":      tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"uuid":      tftypes.NewValue(tftypes.String, nil),
		"task_id":   tftypes.NewValue(tftypes.String, "task-1"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	resp.State.Raw = state.Raw
	r.Read(ctx, resource.ReadRequest{State: tfsdk.State{Schema: schemaResp.Schema, Raw: state.Raw}}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if resp.State.Raw.IsNull() {
		t.Fatal("state must not be removed while the create task is still running")
	}
	var got serverSnapshotResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &got)...)
	if !got.UUID.IsNull() {
		t.Errorf("uuid = %v, want null (nothing may be adopted while the task is running)", got.UUID)
	}
}

// TestServerSnapshotResource_Read_TaskFailedRemovesState verifies that a
// terminal task failure (ERROR) is definitive absence: the unconfirmed
// create is removed from state.
func TestServerSnapshotResource_Read_TaskFailedRemovesState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/tasks/task-err":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-err","state":"ERROR","startedAt":"2026-01-02T03:00:00Z","failedAt":"2026-01-02T03:05:00Z","errorMessage":"disk full"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[" + snapshotListEntry(t, "snap-uuid-old", "pre-upgrade", "", "2025-01-01T00:00:00Z", "SHUTOFF", "system", false, false, nil) + "]"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, nil),
		"server_id": tftypes.NewValue(tftypes.String, "123"),
		"name":      tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"uuid":      tftypes.NewValue(tftypes.String, nil),
		"task_id":   tftypes.NewValue(tftypes.String, "task-err"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	resp.State.Raw = state.Raw
	r.Read(ctx, resource.ReadRequest{State: tfsdk.State{Schema: schemaResp.Schema, Raw: state.Raw}}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if !resp.State.Raw.IsNull() {
		t.Error("state must be removed when the create task failed terminally")
	}
}

// TestServerSnapshotResource_Read_FinishedTaskWindowAdopt verifies that a
// FINISHED task adopts the same-name snapshot created at/after the task's
// start time, NOT the pre-existing same-name snapshot from before it.
func TestServerSnapshotResource_Read_FinishedTaskWindowAdopt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/tasks/task-1":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-1","state":"FINISHED","startedAt":"2026-01-02T03:00:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[" +
				snapshotListEntry(t, "snap-uuid-old", "pre-upgrade", "", "2025-01-01T00:00:00Z", "SHUTOFF", "system", false, false, nil) + "," +
				snapshotListEntry(t, "snap-uuid-new", "pre-upgrade", "", "2026-01-02T04:00:00Z", "SHUTOFF", "system", false, false, nil) +
				"]"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, nil),
		"server_id": tftypes.NewValue(tftypes.String, "123"),
		"name":      tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"uuid":      tftypes.NewValue(tftypes.String, nil),
		"task_id":   tftypes.NewValue(tftypes.String, "task-1"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	resp.State.Raw = state.Raw
	r.Read(ctx, resource.ReadRequest{State: tfsdk.State{Schema: schemaResp.Schema, Raw: state.Raw}}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if resp.State.Raw.IsNull() {
		t.Fatal("state must be kept and the new snapshot adopted")
	}
	var got serverSnapshotResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &got)...)
	if got.UUID.ValueString() != "snap-uuid-new" {
		t.Errorf("uuid = %q, want snap-uuid-new (created after the task started)", got.UUID.ValueString())
	}
	if got.TaskID.ValueString() != "task-1" {
		t.Errorf("task_id = %q, want task-1 (retained after confirmation)", got.TaskID.ValueString())
	}
}

// TestServerSnapshotResource_Read_FinishedTaskNoWindowMatchKeeps verifies
// that a FINISHED task whose start-time window matches no same-name snapshot
// keeps the state (with a warning) instead of adopting a pre-existing one.
func TestServerSnapshotResource_Read_FinishedTaskNoWindowMatchKeeps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/tasks/task-1":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-1","state":"FINISHED","startedAt":"2026-01-02T03:00:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[" + snapshotListEntry(t, "snap-uuid-old", "pre-upgrade", "", "2025-01-01T00:00:00Z", "SHUTOFF", "system", false, false, nil) + "]"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, nil),
		"server_id": tftypes.NewValue(tftypes.String, "123"),
		"name":      tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"uuid":      tftypes.NewValue(tftypes.String, nil),
		"task_id":   tftypes.NewValue(tftypes.String, "task-1"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	resp.State.Raw = state.Raw
	r.Read(ctx, resource.ReadRequest{State: tfsdk.State{Schema: schemaResp.Schema, Raw: state.Raw}}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if resp.State.Raw.IsNull() {
		t.Fatal("state must be kept when the finished task produced no adoptable snapshot")
	}
	if len(resp.Diagnostics.Warnings()) == 0 {
		t.Error("expected a warning when the snapshot is not listed")
	}
	var got serverSnapshotResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &got)...)
	if !got.UUID.IsNull() || got.UUID.ValueString() == "snap-uuid-old" {
		t.Errorf("uuid = %v, want null (the pre-existing snapshot must not be adopted)", got.UUID)
	}
}

// TestServerSnapshotResource_Read_NoTaskNotListedKeepsState verifies the
// wait=false case: no task_id and the snapshot not yet listed keeps the state
// with a warning (the snapshot may still appear). The create_requested_at
// window means only a snapshot created no earlier than the dispatch is
// adoptable, so a pre-existing same-name snapshot is not grabbed.
func TestServerSnapshotResource_Read_NoTaskNotListedKeepsState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots" {
			w.WriteHeader(http.StatusOK)
			// Only a pre-existing same-name snapshot (created before the
			// request) is listed.
			_, _ = w.Write([]byte("[" + snapshotListEntry(t, "snap-uuid-old", "pre-upgrade", "", "2025-01-01T00:00:00Z", "SHUTOFF", "system", false, false, nil) + "]"))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":           tftypes.NewValue(tftypes.String, "123"),
		"name":                tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"wait":                tftypes.NewValue(tftypes.Bool, false),
		"uuid":                tftypes.NewValue(tftypes.String, nil),
		"task_id":             tftypes.NewValue(tftypes.String, nil),
		"create_requested_at": tftypes.NewValue(tftypes.String, "2026-01-02T02:59:00Z"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	resp.State.Raw = state.Raw
	r.Read(ctx, resource.ReadRequest{State: tfsdk.State{Schema: schemaResp.Schema, Raw: state.Raw}}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if resp.State.Raw.IsNull() {
		t.Fatal("state must be kept when an unconfirmed create has no task and is not listed yet")
	}
	if len(resp.Diagnostics.Warnings()) == 0 {
		t.Error("expected a warning when the snapshot is not listed")
	}
	var got serverSnapshotResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &got)...)
	if !got.UUID.IsNull() || got.UUID.ValueString() == "snap-uuid-old" {
		t.Errorf("uuid = %v, want null (the pre-existing snapshot must not be adopted)", got.UUID)
	}
}

// TestServerSnapshotResource_Read_ImportNotListedRemovesState verifies that
// an import (null wait, no task, no dispatch time) of a snapshot that is not
// listed removes the state: a successful empty listing is definitive absence
// for an import, not a pending create.
func TestServerSnapshotResource_Read_ImportNotListedRemovesState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
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
		"uuid":      tftypes.NewValue(tftypes.String, nil),
		"task_id":   tftypes.NewValue(tftypes.String, nil),
		// wait and create_requested_at stay null: ImportState only carries
		// server_id and name.
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	resp.State.Raw = state.Raw
	r.Read(ctx, resource.ReadRequest{State: tfsdk.State{Schema: schemaResp.Schema, Raw: state.Raw}}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if !resp.State.Raw.IsNull() {
		t.Error("state must be removed when an import's snapshot is not listed")
	}
}

// TestServerSnapshotResource_Read_ImportAmbiguousRefuses verifies that an
// import whose name matches several snapshots is refused with an error
// instead of adopting the newest match arbitrarily.
func TestServerSnapshotResource_Read_ImportAmbiguousRefuses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[" +
				snapshotListEntry(t, "snap-uuid-a", "pre-upgrade", "", "2026-01-02T03:01:00Z", "SHUTOFF", "system", false, false, nil) + "," +
				snapshotListEntry(t, "snap-uuid-b", "pre-upgrade", "", "2026-01-02T03:02:00Z", "SHUTOFF", "system", false, false, nil) +
				"]"))
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
		"uuid":      tftypes.NewValue(tftypes.String, nil),
		"task_id":   tftypes.NewValue(tftypes.String, nil),
		// wait and create_requested_at stay null: ImportState only carries
		// server_id and name.
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	resp.State.Raw = state.Raw
	r.Read(ctx, resource.ReadRequest{State: tfsdk.State{Schema: schemaResp.Schema, Raw: state.Raw}}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("Read() expected an ambiguous-import error, got none")
	}
	if msg := resp.Diagnostics.Errors()[0].Summary(); msg != "Ambiguous snapshot import" {
		t.Errorf("error summary = %q, want %q", msg, "Ambiguous snapshot import")
	}
	if resp.State.Raw.IsNull() {
		t.Error("state must be kept (not removed) when an import is ambiguous")
	}
	var got serverSnapshotResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &got)...)
	if !got.UUID.IsNull() {
		t.Errorf("uuid = %v, want null (no snapshot may be adopted while ambiguous)", got.UUID)
	}
}

// TestServerSnapshotResource_Read_NoTaskWindowAdopt verifies that a persisted
// create without a task adopts a same-name snapshot created no earlier than
// the recorded dispatch time, not a pre-existing one from before it.
func TestServerSnapshotResource_Read_NoTaskWindowAdopt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[" +
				snapshotListEntry(t, "snap-uuid-old", "pre-upgrade", "", "2025-01-01T00:00:00Z", "SHUTOFF", "system", false, false, nil) + "," +
				snapshotListEntry(t, "snap-uuid-new", "pre-upgrade", "", "2026-01-02T03:05:00Z", "SHUTOFF", "system", false, false, nil) +
				"]"))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":           tftypes.NewValue(tftypes.String, "123"),
		"name":                tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"wait":                tftypes.NewValue(tftypes.Bool, true),
		"uuid":                tftypes.NewValue(tftypes.String, nil),
		"task_id":             tftypes.NewValue(tftypes.String, nil),
		"create_requested_at": tftypes.NewValue(tftypes.String, "2026-01-02T02:59:00Z"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	resp.State.Raw = state.Raw
	r.Read(ctx, resource.ReadRequest{State: tfsdk.State{Schema: schemaResp.Schema, Raw: state.Raw}}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if resp.State.Raw.IsNull() {
		t.Fatal("state must be kept and the new snapshot adopted")
	}
	var got serverSnapshotResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &got)...)
	if got.UUID.ValueString() != "snap-uuid-new" {
		t.Errorf("uuid = %q, want snap-uuid-new (created after the dispatch)", got.UUID.ValueString())
	}
}

// TestServerSnapshotResource_Read_NoTaskSameSecondNotAdopted verifies the
// sub-second dispatch bound: a pre-existing same-name snapshot created earlier
// in the SAME second as the recorded dispatch is still excluded, because
// create_requested_at carries sub-second precision (whole-second truncation
// would let it qualify).
func TestServerSnapshotResource_Read_NoTaskSameSecondNotAdopted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[" + snapshotListEntry(t, "snap-uuid-old", "pre-upgrade", "", "2026-01-02T02:59:00.250000000Z", "SHUTOFF", "system", false, false, nil) + "]"))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":           tftypes.NewValue(tftypes.String, "123"),
		"name":                tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"wait":                tftypes.NewValue(tftypes.Bool, false),
		"uuid":                tftypes.NewValue(tftypes.String, nil),
		"task_id":             tftypes.NewValue(tftypes.String, nil),
		"create_requested_at": tftypes.NewValue(tftypes.String, "2026-01-02T02:59:00.500000000Z"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	resp.State.Raw = state.Raw
	r.Read(ctx, resource.ReadRequest{State: tfsdk.State{Schema: schemaResp.Schema, Raw: state.Raw}}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if resp.State.Raw.IsNull() {
		t.Fatal("state must be kept when the requested snapshot is not listed yet")
	}
	var got serverSnapshotResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &got)...)
	if !got.UUID.IsNull() || got.UUID.ValueString() == "snap-uuid-old" {
		t.Errorf("uuid = %v, want null (the pre-existing snapshot created earlier in the same second must not be adopted)", got.UUID)
	}
}

// TestServerSnapshotResource_Read_TaskPollErrorHardErrors verifies that a
// non-404 task-polling error hard-errors (state kept) instead of guessing
// the task outcome.
func TestServerSnapshotResource_Read_TaskPollErrorHardErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/tasks/task-1":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"boom"}`))
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
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":        tftypes.NewValue(tftypes.String, nil),
		"server_id": tftypes.NewValue(tftypes.String, "123"),
		"name":      tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"uuid":      tftypes.NewValue(tftypes.String, nil),
		"task_id":   tftypes.NewValue(tftypes.String, "task-1"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	resp.State.Raw = state.Raw
	r.Read(ctx, resource.ReadRequest{State: tfsdk.State{Schema: schemaResp.Schema, Raw: state.Raw}}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic when task polling fails with a non-404")
	}
	if resp.State.Raw.IsNull() {
		t.Error("state must be kept when task polling fails")
	}
}

// TestServerSnapshotResource_Read_TaskGonePreExistingNotAdopted verifies that
// when the recorded create task is gone (404, outcome unknown), a pre-existing
// same-name snapshot created before the dispatch is NOT adopted: the state is
// kept (with a warning) until the requested snapshot becomes identifiable.
func TestServerSnapshotResource_Read_TaskGonePreExistingNotAdopted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/tasks/task-gone":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"no such task"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[" + snapshotListEntry(t, "snap-uuid-old", "pre-upgrade", "", "2026-01-02T02:00:00Z", "SHUTOFF", "system", false, false, nil) + "]"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":                  tftypes.NewValue(tftypes.String, nil),
		"server_id":           tftypes.NewValue(tftypes.String, "123"),
		"name":                tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"wait":                tftypes.NewValue(tftypes.Bool, true),
		"uuid":                tftypes.NewValue(tftypes.String, nil),
		"task_id":             tftypes.NewValue(tftypes.String, "task-gone"),
		"create_requested_at": tftypes.NewValue(tftypes.String, "2026-01-02T02:59:00Z"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	resp.State.Raw = state.Raw
	r.Read(ctx, resource.ReadRequest{State: tfsdk.State{Schema: schemaResp.Schema, Raw: state.Raw}}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if resp.State.Raw.IsNull() {
		t.Fatal("state must be kept when the create task is unreadable")
	}
	if len(resp.Diagnostics.Warnings()) == 0 {
		t.Error("expected a warning when the requested snapshot cannot be identified")
	}
	var got serverSnapshotResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &got)...)
	if !got.UUID.IsNull() || got.UUID.ValueString() == "snap-uuid-old" {
		t.Errorf("uuid = %v, want null (the pre-existing snapshot must not be adopted)", got.UUID)
	}
}

// TestServerSnapshotResource_Read_TaskGoneWindowAdopt verifies that when the
// create task is gone (404), a same-name snapshot created no earlier than the
// recorded dispatch IS adopted — the create window still identifies the
// requested snapshot once it is listed.
func TestServerSnapshotResource_Read_TaskGoneWindowAdopt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/tasks/task-gone":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"no such task"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[" +
				snapshotListEntry(t, "snap-uuid-old", "pre-upgrade", "", "2026-01-02T02:00:00Z", "SHUTOFF", "system", false, false, nil) + "," +
				snapshotListEntry(t, "snap-uuid-new", "pre-upgrade", "", "2026-01-02T03:05:00Z", "SHUTOFF", "system", false, false, nil) +
				"]"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"id":                  tftypes.NewValue(tftypes.String, nil),
		"server_id":           tftypes.NewValue(tftypes.String, "123"),
		"name":                tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"wait":                tftypes.NewValue(tftypes.Bool, true),
		"uuid":                tftypes.NewValue(tftypes.String, nil),
		"task_id":             tftypes.NewValue(tftypes.String, "task-gone"),
		"create_requested_at": tftypes.NewValue(tftypes.String, "2026-01-02T02:59:00Z"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	resp.State.Raw = state.Raw
	r.Read(ctx, resource.ReadRequest{State: tfsdk.State{Schema: schemaResp.Schema, Raw: state.Raw}}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if resp.State.Raw.IsNull() {
		t.Fatal("state must be kept and the new snapshot adopted")
	}
	var got serverSnapshotResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &got)...)
	if got.UUID.ValueString() != "snap-uuid-new" {
		t.Errorf("uuid = %q, want snap-uuid-new (created after the dispatch)", got.UUID.ValueString())
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
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			_, _ = w.Write([]byte("[" +
				snapshotListEntry(t, "snap-uuid-1", "pre-upgrade", "", "2026-01-02T03:00:00Z", "SHUTOFF", "system", false, false, nil) +
				"]"))
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
	deleted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/servers/123/snapshots/pre-upgrade":
			deleted = true
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"no such snapshot"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			// The confirmed snapshot is no longer listed: already gone.
			_, _ = w.Write([]byte("[]"))
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

	// Already gone: the desired end state is reached, so no error — and the
	// name-based delete must not be issued for a snapshot that is not listed.
	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete() must not error when the confirmed snapshot is no longer listed; got: %v", resp.Diagnostics.Errors())
	}
	if deleted {
		t.Error("the name-based delete must not run when the snapshot is already gone")
	}
}

func TestServerSnapshotResource_Delete_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/servers/123/snapshots/pre-upgrade":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"boom"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			_, _ = w.Write([]byte("[" +
				snapshotListEntry(t, "snap-uuid-1", "pre-upgrade", "", "2026-01-02T03:00:00Z", "SHUTOFF", "system", false, false, nil) +
				"]"))
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
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			_, _ = w.Write([]byte("[" +
				snapshotListEntry(t, "snap-uuid-1", "pre-upgrade", "", "2026-01-02T03:00:00Z", "SHUTOFF", "system", false, false, nil) +
				"]"))
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
		"uuid":      tftypes.NewValue(tftypes.String, "snap-uuid-1"),
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

// TestServerSnapshotResource_Delete_InFlightCreateTaskResolvesThenDeletes
// verifies that deleting an unconfirmed create (null uuid, task still running)
// first resolves the create task, then deletes by name — so the in-flight
// snapshot is not mistaken for "already gone" and a pre-existing same-name
// snapshot is not deleted while the create is still running.
func TestServerSnapshotResource_Delete_InFlightCreateTaskResolvesThenDeletes(t *testing.T) {
	deleted := false
	taskPolls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/servers/123/snapshots/pre-upgrade":
			deleted = true
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-del","state":"PENDING"}`))
		case r.URL.Path == "/v1/tasks/task-1":
			// First read: still running; then the wait loop polls to FINISHED.
			taskPolls++
			state := "PENDING"
			if taskPolls > 1 {
				state = "FINISHED"
			}
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{"uuid":"task-1","state":"%s","startedAt":"2026-01-02T03:00:00Z"}`, state)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			// Post-finish listing: the created snapshot is now visible.
			_, _ = w.Write([]byte("[" +
				snapshotListEntry(t, "snap-uuid-new", "pre-upgrade", "", "2026-01-02T03:01:00Z", "SHUTOFF", "system", false, false, nil) +
				"]"))
		case r.URL.Path == "/v1/tasks/task-del":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-del","state":"FINISHED"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"), netcup.WithTaskPollInterval(time.Millisecond))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id": tftypes.NewValue(tftypes.String, "123"),
		"name":      tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"wait":      tftypes.NewValue(tftypes.Bool, true),
		"uuid":      tftypes.NewValue(tftypes.String, nil),
		"task_id":   tftypes.NewValue(tftypes.String, "task-1"),
	})

	var resp resource.DeleteResponse
	resp.State = state
	r.(resource.Resource).Delete(ctx, resource.DeleteRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if !deleted {
		t.Error("expected the name-based delete to run after the create task finished")
	}
	if taskPolls < 2 {
		t.Errorf("expected the in-flight create task to be polled to a terminal state (polls=%d)", taskPolls)
	}
}

// TestServerSnapshotResource_Delete_CreateTaskFailedNoDelete verifies that
// deleting an unconfirmed create whose task failed terminally does NOT issue
// the name-based delete (nothing of the resource's exists, and a pre-existing
// same-name snapshot must not be deleted).
func TestServerSnapshotResource_Delete_CreateTaskFailedNoDelete(t *testing.T) {
	deleted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodDelete:
			deleted = true
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-del","state":"PENDING"}`))
		case r.URL.Path == "/v1/tasks/task-err":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-err","state":"ERROR","startedAt":"2026-01-02T03:00:00Z","failedAt":"2026-01-02T03:05:00Z","errorMessage":"disk full"}`))
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
		"uuid":      tftypes.NewValue(tftypes.String, nil),
		"task_id":   tftypes.NewValue(tftypes.String, "task-err"),
	})

	var resp resource.DeleteResponse
	resp.State = state
	r.(resource.Resource).Delete(ctx, resource.DeleteRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if deleted {
		t.Error("the name-based delete must not run when the create task failed terminally")
	}
}

// TestServerSnapshotResource_Delete_TaskGoneErrors verifies that a 404 create
// task (outcome unknown) keeps the resource in state with an error instead of
// falling through to a name-based delete: deleting by name could remove an
// unrelated same-name snapshot or forget a snapshot still in flight.
func TestServerSnapshotResource_Delete_TaskGoneErrors(t *testing.T) {
	deleted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/servers/123/snapshots/pre-upgrade":
			deleted = true
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"no such snapshot"}`))
		case r.URL.Path == "/v1/tasks/task-gone":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"no such task"}`))
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
		"uuid":      tftypes.NewValue(tftypes.String, nil),
		"task_id":   tftypes.NewValue(tftypes.String, "task-gone"),
	})

	var resp resource.DeleteResponse
	resp.State = state
	r.(resource.Resource).Delete(ctx, resource.DeleteRequest{State: state}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic when the create task's outcome is unknown")
	}
	if deleted {
		t.Error("the name-based delete must not run when the create task's outcome is unknown")
	}
}

// TestServerSnapshotResource_Delete_AmbiguousNameRefuses verifies that when
// several snapshots share the resource's name, the name-only delete endpoint
// is not invoked: it could remove a different same-name snapshot.
func TestServerSnapshotResource_Delete_AmbiguousNameRefuses(t *testing.T) {
	deleted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/servers/123/snapshots/pre-upgrade":
			deleted = true
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-del","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			_, _ = w.Write([]byte("[" +
				snapshotListEntry(t, "snap-uuid-1", "pre-upgrade", "", "2026-01-02T03:00:00Z", "SHUTOFF", "system", false, false, nil) + "," +
				snapshotListEntry(t, "snap-uuid-2", "pre-upgrade", "", "2026-01-03T03:00:00Z", "SHUTOFF", "system", false, false, nil) +
				"]"))
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

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic when several snapshots share the name")
	}
	if deleted {
		t.Error("the name-based delete must not run when the name is ambiguous")
	}
}

// TestServerSnapshotResource_Delete_UUIDMismatchRefuses verifies that a
// confirmed resource whose recorded UUID does not match the only listed
// same-name snapshot is not deleted by name: that would remove a different
// snapshot.
func TestServerSnapshotResource_Delete_UUIDMismatchRefuses(t *testing.T) {
	deleted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/servers/123/snapshots/pre-upgrade":
			deleted = true
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-del","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			_, _ = w.Write([]byte("[" +
				snapshotListEntry(t, "snap-uuid-other", "pre-upgrade", "", "2026-01-02T03:00:00Z", "SHUTOFF", "system", false, false, nil) +
				"]"))
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

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic when the listed snapshot is not the one the resource owns")
	}
	if deleted {
		t.Error("the name-based delete must not run when the listed snapshot has a different UUID")
	}
}

// TestServerSnapshotResource_Delete_UnconfirmedNotListedErrors verifies that an
// unconfirmed create (null uuid, no task) whose snapshot is not listed is not
// treated as "already gone": the snapshot may still be in flight or in the
// post-finish visibility lag.
func TestServerSnapshotResource_Delete_UnconfirmedNotListedErrors(t *testing.T) {
	deleted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/servers/123/snapshots/pre-upgrade":
			deleted = true
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-del","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			_, _ = w.Write([]byte("[]"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":           tftypes.NewValue(tftypes.String, "123"),
		"name":                tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"wait":                tftypes.NewValue(tftypes.Bool, false),
		"uuid":                tftypes.NewValue(tftypes.String, nil),
		"create_requested_at": tftypes.NewValue(tftypes.String, "2026-01-02T02:59:00Z"),
	})

	var resp resource.DeleteResponse
	resp.State = state
	r.(resource.Resource).Delete(ctx, resource.DeleteRequest{State: state}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic when an unconfirmed create's snapshot is not listed")
	}
	if deleted {
		t.Error("the name-based delete must not run when the unconfirmed snapshot is not listed")
	}
}

// TestServerSnapshotResource_Delete_UnconfirmedPreExistingOnlyErrors verifies
// that an unconfirmed create whose listing shows only a same-name snapshot from
// BEFORE the dispatch is not deleted: that snapshot is an unrelated pre-existing
// one, and the created snapshot is not listed yet.
func TestServerSnapshotResource_Delete_UnconfirmedPreExistingOnlyErrors(t *testing.T) {
	deleted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/servers/123/snapshots/pre-upgrade":
			deleted = true
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-del","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			// The only same-name snapshot was created before the dispatch.
			_, _ = w.Write([]byte("[" +
				snapshotListEntry(t, "snap-uuid-old", "pre-upgrade", "", "2026-01-02T02:00:00Z", "SHUTOFF", "system", false, false, nil) +
				"]"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":           tftypes.NewValue(tftypes.String, "123"),
		"name":                tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"wait":                tftypes.NewValue(tftypes.Bool, false),
		"uuid":                tftypes.NewValue(tftypes.String, nil),
		"create_requested_at": tftypes.NewValue(tftypes.String, "2026-01-02T02:59:00Z"),
	})

	var resp resource.DeleteResponse
	resp.State = state
	r.(resource.Resource).Delete(ctx, resource.DeleteRequest{State: state}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic when only a pre-existing same-name snapshot is listed")
	}
	if deleted {
		t.Error("the name-based delete must not run when the listed snapshot predates the dispatch")
	}
}

// TestServerSnapshotResource_Read_FinishedTaskNoStartedAtPreCreateExcluded
// verifies that a FINISHED task WITHOUT startedAt does not silently widen to
// name-only matching: with a persisted pre-create set, the pre-existing
// same-name snapshot (even one created after the host-clock dispatch) is never
// adopted during the listing lag.
func TestServerSnapshotResource_Read_FinishedTaskNoStartedAtPreCreateExcluded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-ns":
			// FINISHED with no startedAt at all.
			_, _ = w.Write([]byte(`{"uuid":"task-ns","state":"FINISHED"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			// The pre-existing same-name snapshot was created AFTER the
			// recorded dispatch, so only the persisted UUID set can exclude it.
			_, _ = w.Write([]byte("[" +
				snapshotListEntry(t, "snap-uuid-old", "pre-upgrade", "", "2026-01-02T03:00:00Z", "SHUTOFF", "system", false, false, nil) +
				"]"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":           tftypes.NewValue(tftypes.String, "123"),
		"name":                tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"wait":                tftypes.NewValue(tftypes.Bool, true),
		"uuid":                tftypes.NewValue(tftypes.String, nil),
		"task_id":             tftypes.NewValue(tftypes.String, "task-ns"),
		"create_requested_at": tftypes.NewValue(tftypes.String, "2026-01-02T02:59:00Z"),
		"pre_create_uuids":    snapshotStringListVal("snap-uuid-old"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	resp.State.Raw = state.Raw
	r.Read(ctx, resource.ReadRequest{State: tfsdk.State{Schema: schemaResp.Schema, Raw: state.Raw}}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if resp.State.Raw.IsNull() {
		t.Fatal("state must be kept while the created snapshot is not listed")
	}
	var got serverSnapshotResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &got)...)
	if !got.UUID.IsNull() {
		t.Errorf("uuid = %q, want null (the pre-existing snapshot must not be adopted)", got.UUID.ValueString())
	}
}

// TestServerSnapshotResource_Read_FinishedTaskNoStartedAtClockFallback verifies
// that a FINISHED task without startedAt and WITHOUT a persisted pre-create set
// still falls back to the create_requested_at bound (rather than name-only): a
// pre-existing same-name snapshot created before the dispatch is not adopted.
func TestServerSnapshotResource_Read_FinishedTaskNoStartedAtClockFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-ns":
			_, _ = w.Write([]byte(`{"uuid":"task-ns","state":"FINISHED"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			_, _ = w.Write([]byte("[" +
				snapshotListEntry(t, "snap-uuid-old", "pre-upgrade", "", "2026-01-02T02:00:00Z", "SHUTOFF", "system", false, false, nil) +
				"]"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":           tftypes.NewValue(tftypes.String, "123"),
		"name":                tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"wait":                tftypes.NewValue(tftypes.Bool, true),
		"uuid":                tftypes.NewValue(tftypes.String, nil),
		"task_id":             tftypes.NewValue(tftypes.String, "task-ns"),
		"create_requested_at": tftypes.NewValue(tftypes.String, "2026-01-02T02:59:00Z"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	resp.State.Raw = state.Raw
	r.Read(ctx, resource.ReadRequest{State: tfsdk.State{Schema: schemaResp.Schema, Raw: state.Raw}}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if resp.State.Raw.IsNull() {
		t.Fatal("state must be kept while the created snapshot is not listed")
	}
	var got serverSnapshotResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &got)...)
	if !got.UUID.IsNull() {
		t.Errorf("uuid = %q, want null (the pre-dispatch snapshot must not be adopted)", got.UUID.ValueString())
	}
}

// TestServerSnapshotResource_Read_TasklessPreCreateSetExcludesSkewedClock
// verifies the cross-clock case: the pre-existing same-name snapshot was
// created AFTER the host-clock dispatch time (so the create_requested_at bound
// would adopt it), but the persisted pre-create UUID set excludes it by
// identity.
func TestServerSnapshotResource_Read_TasklessPreCreateSetExcludesSkewedClock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			_, _ = w.Write([]byte("[" +
				snapshotListEntry(t, "snap-uuid-old", "pre-upgrade", "", "2026-01-02T02:59:30Z", "SHUTOFF", "system", false, false, nil) +
				"]"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":           tftypes.NewValue(tftypes.String, "123"),
		"name":                tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"wait":                tftypes.NewValue(tftypes.Bool, false),
		"uuid":                tftypes.NewValue(tftypes.String, nil),
		"create_requested_at": tftypes.NewValue(tftypes.String, "2026-01-02T02:59:00Z"),
		"pre_create_uuids":    snapshotStringListVal("snap-uuid-old"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	resp.State.Raw = state.Raw
	r.Read(ctx, resource.ReadRequest{State: tfsdk.State{Schema: schemaResp.Schema, Raw: state.Raw}}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if resp.State.Raw.IsNull() {
		t.Fatal("state must be kept while the created snapshot is not listed")
	}
	var got serverSnapshotResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &got)...)
	if !got.UUID.IsNull() {
		t.Errorf("uuid = %q, want null (the pre-existing snapshot is excluded by identity despite the clock skew)", got.UUID.ValueString())
	}
}

// TestServerSnapshotResource_Read_TasklessPreCreateSetAdoptsNew verifies the
// pre-create set does not over-exclude: a same-name snapshot that is NOT in the
// set is adopted once listed.
func TestServerSnapshotResource_Read_TasklessPreCreateSetAdoptsNew(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			_, _ = w.Write([]byte("[" +
				snapshotListEntry(t, "snap-uuid-old", "pre-upgrade", "", "2026-01-02T02:59:30Z", "SHUTOFF", "system", false, false, nil) + "," +
				snapshotListEntry(t, "snap-uuid-new", "pre-upgrade", "", "2026-01-02T03:05:00Z", "SHUTOFF", "system", false, false, nil) +
				"]"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":           tftypes.NewValue(tftypes.String, "123"),
		"name":                tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"wait":                tftypes.NewValue(tftypes.Bool, false),
		"uuid":                tftypes.NewValue(tftypes.String, nil),
		"create_requested_at": tftypes.NewValue(tftypes.String, "2026-01-02T02:59:00Z"),
		"pre_create_uuids":    snapshotStringListVal("snap-uuid-old"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	resp.State.Raw = state.Raw
	r.Read(ctx, resource.ReadRequest{State: tfsdk.State{Schema: schemaResp.Schema, Raw: state.Raw}}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if resp.State.Raw.IsNull() {
		t.Fatal("state must be kept")
	}
	var got serverSnapshotResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &got)...)
	if got.UUID.ValueString() != "snap-uuid-new" {
		t.Errorf("uuid = %q, want snap-uuid-new (the only same-name snapshot outside the pre-create set)", got.UUID.ValueString())
	}
}

// TestServerSnapshotResource_Delete_ServerGoneSuccess verifies that a 404
// preflight listing (the server was removed, e.g. between refresh and destroy)
// is a successful delete: the snapshot is necessarily gone.
func TestServerSnapshotResource_Delete_ServerGoneSuccess(t *testing.T) {
	deleted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/servers/123/snapshots/pre-upgrade":
			deleted = true
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-del","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"no such server"}`))
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
		t.Fatalf("Delete() must succeed when the server (and thus the snapshot) is gone; got: %v", resp.Diagnostics.Errors())
	}
	if deleted {
		t.Error("the name-based delete must not run when the server is already gone")
	}
}

// TestServerSnapshotResource_Delete_UnconfirmedPreCreateExcludedRefuses
// verifies the cross-clock case on the delete path: the only listed same-name
// snapshot was created AFTER the host-clock dispatch (so the clock bound would
// allow the delete), but the persisted pre-create set identifies it as
// pre-existing, so the delete is refused.
func TestServerSnapshotResource_Delete_UnconfirmedPreCreateExcludedRefuses(t *testing.T) {
	deleted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/servers/123/snapshots/pre-upgrade":
			deleted = true
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-del","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			_, _ = w.Write([]byte("[" +
				snapshotListEntry(t, "snap-uuid-old", "pre-upgrade", "", "2026-01-02T02:59:30Z", "SHUTOFF", "system", false, false, nil) +
				"]"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":           tftypes.NewValue(tftypes.String, "123"),
		"name":                tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"wait":                tftypes.NewValue(tftypes.Bool, false),
		"uuid":                tftypes.NewValue(tftypes.String, nil),
		"create_requested_at": tftypes.NewValue(tftypes.String, "2026-01-02T02:59:00Z"),
		"pre_create_uuids":    snapshotStringListVal("snap-uuid-old"),
	})

	var resp resource.DeleteResponse
	resp.State = state
	r.(resource.Resource).Delete(ctx, resource.DeleteRequest{State: state}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic when the only same-name snapshot is a pre-existing one")
	}
	if deleted {
		t.Error("the name-based delete must not run when the listed snapshot is excluded by the pre-create set")
	}
}

// TestServerSnapshotResource_Create_ConcurrentSnapshotInGapNotAdopted verifies
// the preflight-to-task race: a same-name snapshot created by a concurrent
// actor AFTER the pre-create listing (absent from preExistingUUIDs) but BEFORE
// the create task started is not adopted while the created snapshot is in
// listing lag.
func TestServerSnapshotResource_Create_ConcurrentSnapshotInGapNotAdopted(t *testing.T) {
	listCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/servers/123/snapshots":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-1","state":"PENDING"}`))
		case r.URL.Path == "/v1/tasks/task-1":
			_, _ = w.Write([]byte(`{"uuid":"task-1","state":"FINISHED","startedAt":"2026-01-02T03:00:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			listCalls++
			w.WriteHeader(http.StatusOK)
			if listCalls == 1 {
				// Pre-listing: no snapshot exists before the create.
				_, _ = w.Write([]byte("[]"))
			} else {
				// Post-finish: the created snapshot is not listed yet (lag),
				// but a concurrent same-name snapshot created in the gap
				// between the pre-listing and the task start is.
				_, _ = w.Write([]byte("[" +
					snapshotListEntry(t, "snap-uuid-other", "pre-upgrade", "", "2026-01-02T02:30:00Z", "SHUTOFF", "system", false, false, nil) +
					"]"))
			}
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
		t.Fatalf("Create() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	var state serverSnapshotResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if !state.UUID.IsNull() {
		t.Errorf("uuid = %q, want null (the concurrent snapshot predates the task start and must not be adopted)", state.UUID.ValueString())
	}
	if state.TaskID.ValueString() != "task-1" {
		t.Errorf("task_id = %q, want task-1", state.TaskID.ValueString())
	}
	if len(resp.Diagnostics.Warnings()) == 0 {
		t.Error("expected a warning when the created snapshot is not listed yet")
	}
}

// TestServerSnapshotResource_Create_MultipleNewSameNameNotAdopted verifies
// that when SEVERAL new same-name snapshots are listed (the created one plus a
// concurrent create, both after the task start), none is adopted: the created
// snapshot cannot be attributed, so the resource stays unconfirmed.
func TestServerSnapshotResource_Create_MultipleNewSameNameNotAdopted(t *testing.T) {
	listCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/servers/123/snapshots":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-1","state":"PENDING"}`))
		case r.URL.Path == "/v1/tasks/task-1":
			_, _ = w.Write([]byte(`{"uuid":"task-1","state":"FINISHED","startedAt":"2026-01-02T03:00:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			listCalls++
			w.WriteHeader(http.StatusOK)
			if listCalls == 1 {
				// Pre-listing: no snapshot exists before the create.
				_, _ = w.Write([]byte("[]"))
			} else {
				// Two NEW same-name snapshots, both created after the task
				// started: the created one and a concurrent create.
				_, _ = w.Write([]byte("[" +
					snapshotListEntry(t, "snap-uuid-a", "pre-upgrade", "", "2026-01-02T03:01:00Z", "SHUTOFF", "system", false, false, nil) + "," +
					snapshotListEntry(t, "snap-uuid-b", "pre-upgrade", "", "2026-01-02T03:02:00Z", "SHUTOFF", "system", false, false, nil) +
					"]"))
			}
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
		t.Fatalf("Create() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	var state serverSnapshotResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &state)...)
	if !state.UUID.IsNull() {
		t.Errorf("uuid = %q, want null (two new same-name snapshots cannot be attributed)", state.UUID.ValueString())
	}
	if state.TaskID.ValueString() != "task-1" {
		t.Errorf("task_id = %q, want task-1", state.TaskID.ValueString())
	}
	if len(resp.Diagnostics.Warnings()) == 0 {
		t.Error("expected an ambiguity warning")
	}
}

// TestServerSnapshotResource_Read_FinishedTaskConcurrentInGapNotAdopted
// verifies the Read-side preflight-to-task race: with a persisted pre-create
// set AND a task startedAt, a same-name snapshot that is absent from the set
// (created after the pre-create listing) but predates the task start is not
// adopted during the listing lag.
func TestServerSnapshotResource_Read_FinishedTaskConcurrentInGapNotAdopted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/tasks/task-1":
			_, _ = w.Write([]byte(`{"uuid":"task-1","state":"FINISHED","startedAt":"2026-01-02T03:00:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			// The concurrent snapshot is NOT in the pre-create set but was
			// created before the task started.
			_, _ = w.Write([]byte("[" +
				snapshotListEntry(t, "snap-uuid-old", "pre-upgrade", "", "2025-01-01T00:00:00Z", "SHUTOFF", "system", false, false, nil) + "," +
				snapshotListEntry(t, "snap-uuid-other", "pre-upgrade", "", "2026-01-02T02:30:00Z", "SHUTOFF", "system", false, false, nil) +
				"]"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":           tftypes.NewValue(tftypes.String, "123"),
		"name":                tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"wait":                tftypes.NewValue(tftypes.Bool, true),
		"uuid":                tftypes.NewValue(tftypes.String, nil),
		"task_id":             tftypes.NewValue(tftypes.String, "task-1"),
		"create_requested_at": tftypes.NewValue(tftypes.String, "2026-01-02T02:59:00Z"),
		"pre_create_uuids":    snapshotStringListVal("snap-uuid-old"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	resp.State.Raw = state.Raw
	r.Read(ctx, resource.ReadRequest{State: tfsdk.State{Schema: schemaResp.Schema, Raw: state.Raw}}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if resp.State.Raw.IsNull() {
		t.Fatal("state must be kept while the created snapshot is not listed")
	}
	var got serverSnapshotResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &got)...)
	if !got.UUID.IsNull() {
		t.Errorf("uuid = %q, want null (the gap snapshot predates the task start and must not be adopted)", got.UUID.ValueString())
	}
}

// TestServerSnapshotResource_Read_FinishedTaskAdoptsSoleNewCandidate verifies
// the combined proofs do not over-exclude: the sole same-name snapshot that
// is both outside the pre-create set AND created after the task start is
// adopted.
func TestServerSnapshotResource_Read_FinishedTaskAdoptsSoleNewCandidate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/tasks/task-1":
			_, _ = w.Write([]byte(`{"uuid":"task-1","state":"FINISHED","startedAt":"2026-01-02T03:00:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			_, _ = w.Write([]byte("[" +
				snapshotListEntry(t, "snap-uuid-old", "pre-upgrade", "", "2025-01-01T00:00:00Z", "SHUTOFF", "system", false, false, nil) + "," +
				snapshotListEntry(t, "snap-uuid-other", "pre-upgrade", "", "2026-01-02T02:30:00Z", "SHUTOFF", "system", false, false, nil) + "," +
				snapshotListEntry(t, "snap-uuid-new", "pre-upgrade", "", "2026-01-02T03:01:00Z", "SHUTOFF", "system", false, false, nil) +
				"]"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":           tftypes.NewValue(tftypes.String, "123"),
		"name":                tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"wait":                tftypes.NewValue(tftypes.Bool, true),
		"uuid":                tftypes.NewValue(tftypes.String, nil),
		"task_id":             tftypes.NewValue(tftypes.String, "task-1"),
		"create_requested_at": tftypes.NewValue(tftypes.String, "2026-01-02T02:59:00Z"),
		"pre_create_uuids":    snapshotStringListVal("snap-uuid-old"),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	resp.State.Raw = state.Raw
	r.Read(ctx, resource.ReadRequest{State: tfsdk.State{Schema: schemaResp.Schema, Raw: state.Raw}}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if resp.State.Raw.IsNull() {
		t.Fatal("state must be kept and the created snapshot adopted")
	}
	var got serverSnapshotResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &got)...)
	if got.UUID.ValueString() != "snap-uuid-new" {
		t.Errorf("uuid = %q, want snap-uuid-new (the sole candidate outside the set and after the task start)", got.UUID.ValueString())
	}
}

// TestServerSnapshotResource_Delete_ConcurrentSnapshotInGapRefuses verifies
// the Delete-side preflight-to-task race: the only listed same-name snapshot
// is absent from the pre-create set (created after the pre-create listing) but
// predates the create task's start, so the name-based delete is refused.
func TestServerSnapshotResource_Delete_ConcurrentSnapshotInGapRefuses(t *testing.T) {
	deleted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/servers/123/snapshots/pre-upgrade":
			deleted = true
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-del","state":"PENDING"}`))
		case r.URL.Path == "/v1/tasks/task-1":
			_, _ = w.Write([]byte(`{"uuid":"task-1","state":"FINISHED","startedAt":"2026-01-02T03:00:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			// Only the gap snapshot is listed: the pre-existing one is gone
			// and the created one is in listing lag.
			_, _ = w.Write([]byte("[" +
				snapshotListEntry(t, "snap-uuid-other", "pre-upgrade", "", "2026-01-02T02:30:00Z", "SHUTOFF", "system", false, false, nil) +
				"]"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":           tftypes.NewValue(tftypes.String, "123"),
		"name":                tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"wait":                tftypes.NewValue(tftypes.Bool, true),
		"uuid":                tftypes.NewValue(tftypes.String, nil),
		"task_id":             tftypes.NewValue(tftypes.String, "task-1"),
		"create_requested_at": tftypes.NewValue(tftypes.String, "2026-01-02T02:59:00Z"),
		"pre_create_uuids":    snapshotStringListVal("snap-uuid-old"),
	})

	var resp resource.DeleteResponse
	resp.State = state
	r.(resource.Resource).Delete(ctx, resource.DeleteRequest{State: state}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic when the only same-name snapshot predates the task start")
	}
	if deleted {
		t.Error("the name-based delete must not run when the listed snapshot predates the create task's start")
	}
}

// TestServerSnapshotResource_Read_FinishedTaskEmptySetMultipleNotAdopted
// verifies that a persisted EMPTY pre-create UUID set (nothing existed before
// the dispatch) is not collapsed to "no set": with a finished task that omits
// startedAt, multiple newly listed same-name snapshots must NOT be adopted
// (sole-candidate rule) instead of falling back to newest-wins.
func TestServerSnapshotResource_Read_FinishedTaskEmptySetMultipleNotAdopted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/tasks/task-1":
			_, _ = w.Write([]byte(`{"uuid":"task-1","state":"FINISHED"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			_, _ = w.Write([]byte("[" +
				snapshotListEntry(t, "snap-uuid-a", "pre-upgrade", "", "2026-01-02T03:01:00Z", "SHUTOFF", "system", false, false, nil) + "," +
				snapshotListEntry(t, "snap-uuid-b", "pre-upgrade", "", "2026-01-02T03:02:00Z", "SHUTOFF", "system", false, false, nil) +
				"]"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":           tftypes.NewValue(tftypes.String, "123"),
		"name":                tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"wait":                tftypes.NewValue(tftypes.Bool, true),
		"uuid":                tftypes.NewValue(tftypes.String, nil),
		"task_id":             tftypes.NewValue(tftypes.String, "task-1"),
		"create_requested_at": tftypes.NewValue(tftypes.String, "2026-01-02T02:59:00Z"),
		"pre_create_uuids":    snapshotStringListVal(),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	resp.State.Raw = state.Raw
	r.Read(ctx, resource.ReadRequest{State: tfsdk.State{Schema: schemaResp.Schema, Raw: state.Raw}}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if resp.State.Raw.IsNull() {
		t.Fatal("state must be kept while the created snapshot cannot be attributed")
	}
	var got serverSnapshotResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &got)...)
	if !got.UUID.IsNull() {
		t.Errorf("uuid = %q, want null (with an empty pre-create set, two new same-name snapshots cannot be attributed)", got.UUID.ValueString())
	}
}

// TestServerSnapshotResource_Read_FinishedTaskEmptySetSoleAdopted verifies the
// empty pre-create set does not over-exclude: with nothing pre-existing and no
// task startedAt, the sole newly listed same-name snapshot is adopted.
func TestServerSnapshotResource_Read_FinishedTaskEmptySetSoleAdopted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/tasks/task-1":
			_, _ = w.Write([]byte(`{"uuid":"task-1","state":"FINISHED"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			_, _ = w.Write([]byte("[" +
				snapshotListEntry(t, "snap-uuid-new", "pre-upgrade", "", "2026-01-02T03:01:00Z", "SHUTOFF", "system", false, false, nil) +
				"]"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":           tftypes.NewValue(tftypes.String, "123"),
		"name":                tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"wait":                tftypes.NewValue(tftypes.Bool, true),
		"uuid":                tftypes.NewValue(tftypes.String, nil),
		"task_id":             tftypes.NewValue(tftypes.String, "task-1"),
		"create_requested_at": tftypes.NewValue(tftypes.String, "2026-01-02T02:59:00Z"),
		"pre_create_uuids":    snapshotStringListVal(),
	})

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	resp.State.Raw = state.Raw
	r.Read(ctx, resource.ReadRequest{State: tfsdk.State{Schema: schemaResp.Schema, Raw: state.Raw}}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read() unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if resp.State.Raw.IsNull() {
		t.Fatal("state must be kept and the created snapshot adopted")
	}
	var got serverSnapshotResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &got)...)
	if got.UUID.ValueString() != "snap-uuid-new" {
		t.Errorf("uuid = %q, want snap-uuid-new (the sole new same-name snapshot)", got.UUID.ValueString())
	}
}

// TestServerSnapshotResource_Delete_EmptySetGapSnapshotRefuses verifies that
// with an EMPTY persisted pre-create set (nothing existed before the dispatch)
// and no task start time, a single same-name snapshot created before the
// recorded dispatch (a concurrent create in the gap) is still refused by the
// host-clock dispatch bound rather than deleted.
func TestServerSnapshotResource_Delete_EmptySetGapSnapshotRefuses(t *testing.T) {
	deleted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/servers/123/snapshots/pre-upgrade":
			deleted = true
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-del","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			_, _ = w.Write([]byte("[" +
				snapshotListEntry(t, "snap-uuid-other", "pre-upgrade", "", "2026-01-02T02:30:00Z", "SHUTOFF", "system", false, false, nil) +
				"]"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := netcup.New(netcup.WithAPIEndpoint(srv.URL), netcup.WithAccessToken("tok"))
	r, schemaResp := configureServerSnapshotResource(t, client)

	ctx := context.Background()
	state := resourceState(schemaResp, map[string]tftypes.Value{
		"server_id":           tftypes.NewValue(tftypes.String, "123"),
		"name":                tftypes.NewValue(tftypes.String, "pre-upgrade"),
		"wait":                tftypes.NewValue(tftypes.Bool, false),
		"uuid":                tftypes.NewValue(tftypes.String, nil),
		"create_requested_at": tftypes.NewValue(tftypes.String, "2026-01-02T02:59:00Z"),
		"pre_create_uuids":    snapshotStringListVal(),
	})

	var resp resource.DeleteResponse
	resp.State = state
	r.(resource.Resource).Delete(ctx, resource.DeleteRequest{State: state}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic when the only same-name snapshot predates the dispatch")
	}
	if deleted {
		t.Error("the name-based delete must not run when the listed snapshot predates the dispatch bound")
	}
}

// TestServerSnapshotResource_Delete_HostClockSkewServerProofsProceeds verifies
// that when the create task reported startedAt, an unconfirmed destroy trusts
// the server-side proofs: a host clock ahead of the API (create_requested_at
// later than the snapshot's server-side CreationTime) must not veto the sole
// listed snapshot that passes both the identity set and the startedAt bound.
func TestServerSnapshotResource_Delete_HostClockSkewServerProofsProceeds(t *testing.T) {
	deleted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/servers/123/snapshots/pre-upgrade":
			deleted = true
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"task-del","state":"PENDING"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task-1":
			_, _ = w.Write([]byte(`{"uuid":"task-1","name":"ServerSnapshot","state":"FINISHED","startedAt":"2026-01-02T03:00:00Z","finishedAt":"2026-01-02T03:02:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/servers/123/snapshots":
			_, _ = w.Write([]byte("[" +
				snapshotListEntry(t, "snap-uuid-new", "pre-upgrade", "", "2026-01-02T03:01:00Z", "SHUTOFF", "system", false, false, nil) +
				"]"))
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
		"uuid":      tftypes.NewValue(tftypes.String, nil),
		"task_id":   tftypes.NewValue(tftypes.String, "task-1"),
		// Host clock ahead of the API: the recorded dispatch time is later
		// than the snapshot's server-side creation time.
		"create_requested_at": tftypes.NewValue(tftypes.String, "2026-01-02T03:30:00Z"),
		"pre_create_uuids":    snapshotStringListVal(),
	})

	var resp resource.DeleteResponse
	resp.State = state
	r.(resource.Resource).Delete(ctx, resource.DeleteRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if !deleted {
		t.Error("the name-based delete must run when the sole snapshot passes both server-side proofs")
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
		"create_requested_at":  tftypes.NewValue(tftypes.String, "2026-01-02T03:00:00Z"),
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
