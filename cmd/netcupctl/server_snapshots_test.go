package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServerSnapshots_TableOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/servers/123/snapshots" {
			t.Errorf("path = %q, want /v1/servers/123/snapshots", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[
			{"uuid":"a1","name":"nightly","description":null,"disks":["sda"],
			 "creationTime":"2026-07-16T13:34:04Z","state":"RUNNING","online":true,"exported":false},
			{"uuid":"b2","name":"exported-snap","description":null,"disks":[],
			 "creationTime":"2026-07-10T09:00:00Z","state":"STOPPED","online":false,"exported":true,"exportedSizeInKiB":204800}
		]`))
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	if err := serverSnapshots([]string{"123"}, &buf, &buf, nil); err != nil {
		t.Fatalf("serverSnapshots() error = %v", err)
	}
	out := buf.String()
	for _, want := range []string{"NAME", "CREATED", "STATE", "ONLINE", "EXPORTED", "nightly", "exported-snap", "2026-07-16T13:34:04Z", "RUNNING", "true", "false"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %q", want, out)
		}
	}
}

func TestServerSnapshots_NoSnapshots(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	if err := serverSnapshots([]string{"123"}, &buf, &buf, nil); err != nil {
		t.Fatalf("serverSnapshots() error = %v", err)
	}
	if !strings.Contains(buf.String(), "No snapshots found.") {
		t.Errorf("output = %q, want 'No snapshots found.'", buf.String())
	}
}

func TestServerSnapshots_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"uuid":"a1","name":"nightly","disks":[],"creationTime":"2026-07-16T13:34:04Z","state":"RUNNING","online":true,"exported":false}]`))
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	// Flag placed both before and after the positional ID must work, since the
	// documented form is `server snapshots <id> [--json]`.
	for _, args := range [][]string{{"--json", "123"}, {"123", "--json"}} {
		var buf bytes.Buffer
		if err := serverSnapshots(args, &buf, &buf, nil); err != nil {
			t.Fatalf("serverSnapshots(%v) error = %v", args, err)
		}
		out := strings.TrimSpace(buf.String())
		if !strings.HasPrefix(out, "[") || !strings.HasSuffix(out, "]") {
			t.Errorf("serverSnapshots(%v) JSON output should be an array, got: %q", args, out)
		}
		if !strings.Contains(out, `"name": "nightly"`) {
			t.Errorf("serverSnapshots(%v) JSON output missing snapshot data: %q", args, out)
		}
	}
}

func TestServerSnapshots_MissingID(t *testing.T) {
	var buf bytes.Buffer
	err := serverSnapshots(nil, &buf, &buf, nil)
	if err == nil {
		t.Fatal("serverSnapshots() error = nil, want error for missing ID")
	}
	if !strings.Contains(err.Error(), "server ID") {
		t.Errorf("error should mention missing server ID, got: %v", err)
	}
}

func TestServerSnapshots_InvalidID(t *testing.T) {
	var buf bytes.Buffer
	err := serverSnapshots([]string{"not-a-number"}, &buf, &buf, nil)
	if err == nil {
		t.Fatal("serverSnapshots() error = nil, want error for non-integer ID")
	}
	if !strings.Contains(err.Error(), "invalid server ID") {
		t.Errorf("error should mention invalid server ID, got: %v", err)
	}
}

func TestServerSnapshots_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"server not found"}`))
	}))
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var buf bytes.Buffer
	err := serverSnapshots([]string{"999"}, &buf, &buf, nil)
	if err == nil {
		t.Fatal("serverSnapshots() error = nil, want not-found error")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should mention 404, got: %v", err)
	}
}

// snapshotActionRecorder captures what the fake SCP server saw on a snapshot
// mutation and lets a test steer the response status and the polled task state.
type snapshotActionRecorder struct {
	requests  int
	method    string
	path      string
	body      map[string]interface{}
	status    int    // 0 → 202 Accepted
	taskState string // GET /v1/tasks state; "" → FINISHED
}

func newSnapshotActionServer(rec *snapshotActionRecorder) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.requests++
		rec.method = r.Method
		rec.path = r.URL.Path
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/snapshots") {
			_ = json.NewDecoder(r.Body).Decode(&rec.body)
		}

		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/tasks/"):
			ts := rec.taskState
			if ts == "" {
				ts = "FINISHED"
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-1","state":"` + ts + `"}`))
		default:
			status := rec.status
			if status == 0 {
				status = http.StatusAccepted
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			switch {
			case status == http.StatusAccepted:
				_, _ = w.Write([]byte(`{"uuid":"task-1","state":"PENDING"}`))
			case status >= 400:
				_, _ = w.Write([]byte(`{"message":"boom"}`))
			}
		}
	}))
}

func TestServerSnapshotsCreate(t *testing.T) {
	rec := &snapshotActionRecorder{}
	srv := newSnapshotActionServer(rec)
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var out, errBuf bytes.Buffer
	if err := serverSnapshotsCreate([]string{"5", "--name", "nightly", "--disk", "sda"}, &out, &errBuf, nil); err != nil {
		t.Fatalf("serverSnapshotsCreate error = %v", err)
	}
	if rec.method != http.MethodPost || rec.path != "/v1/servers/5/snapshots" {
		t.Errorf("request = %s %s, want POST /v1/servers/5/snapshots", rec.method, rec.path)
	}
	if rec.body["name"] != "nightly" {
		t.Errorf("body name = %v, want nightly", rec.body["name"])
	}
	if rec.body["diskName"] != "sda" {
		t.Errorf("body diskName = %v, want sda", rec.body["diskName"])
	}
	if _, ok := rec.body["onlineSnapshot"]; ok {
		t.Errorf("body onlineSnapshot present (%v), want absent for an offline snapshot", rec.body["onlineSnapshot"])
	}
	if !strings.Contains(out.String(), "nightly") || !strings.Contains(out.String(), "task-1") {
		t.Errorf("output missing snapshot/task: %q", out.String())
	}
}

func TestServerSnapshotsCreateOnline(t *testing.T) {
	rec := &snapshotActionRecorder{}
	srv := newSnapshotActionServer(rec)
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var out, errBuf bytes.Buffer
	if err := serverSnapshotsCreate([]string{"5", "--name", "live", "--online"}, &out, &errBuf, nil); err != nil {
		t.Fatalf("serverSnapshotsCreate error = %v", err)
	}
	if rec.body["onlineSnapshot"] != true {
		t.Errorf("body onlineSnapshot = %v, want true", rec.body["onlineSnapshot"])
	}
	if _, ok := rec.body["diskName"]; ok {
		t.Errorf("body diskName present (%v), want absent for an online snapshot", rec.body["diskName"])
	}
}

func TestServerSnapshotsCreateRequiresName(t *testing.T) {
	var out, errBuf bytes.Buffer
	err := serverSnapshotsCreate([]string{"5", "--disk", "sda"}, &out, &errBuf, nil)
	if err == nil {
		t.Fatal("missing --name error = nil, want error")
	}
	if !strings.Contains(err.Error(), "--name") {
		t.Errorf("error = %v, want mention of --name", err)
	}
}

func TestServerSnapshotsCreateRequiresDiskOrOnline(t *testing.T) {
	var out, errBuf bytes.Buffer
	err := serverSnapshotsCreate([]string{"5", "--name", "x"}, &out, &errBuf, nil)
	if err == nil {
		t.Fatal("missing --disk/--online error = nil, want error")
	}
	if !strings.Contains(err.Error(), "--disk") {
		t.Errorf("error = %v, want mention of --disk/--online", err)
	}
}

func TestServerSnapshotsCreateJSON(t *testing.T) {
	rec := &snapshotActionRecorder{}
	srv := newSnapshotActionServer(rec)
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var out, errBuf bytes.Buffer
	if err := serverSnapshotsCreate([]string{"5", "--name", "nightly", "--disk", "sda", "--json"}, &out, &errBuf, nil); err != nil {
		t.Fatalf("serverSnapshotsCreate --json error = %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout=%q", err, out.String())
	}
	if decoded["action"] != "create" || decoded["snapshot"] != "nightly" {
		t.Errorf("decoded = %v, want action=create snapshot=nightly", decoded)
	}
}

func TestServerSnapshotsCreateWaitPollsToTerminal(t *testing.T) {
	rec := &snapshotActionRecorder{taskState: "FINISHED"}
	srv := newSnapshotActionServer(rec)
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var out, errBuf bytes.Buffer
	if err := serverSnapshotsCreate([]string{"5", "--name", "nightly", "--disk", "sda", "--wait"}, &out, &errBuf, nil); err != nil {
		t.Fatalf("serverSnapshotsCreate --wait error = %v", err)
	}
	if !strings.Contains(out.String(), "FINISHED") {
		t.Errorf("output missing final task state: %q", out.String())
	}
}

func TestServerSnapshotsDeleteForce(t *testing.T) {
	rec := &snapshotActionRecorder{}
	srv := newSnapshotActionServer(rec)
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var out, errBuf bytes.Buffer
	if err := serverSnapshotsDelete([]string{"5", "nightly", "--force"}, &out, &errBuf, nil); err != nil {
		t.Fatalf("serverSnapshotsDelete --force error = %v", err)
	}
	if rec.method != http.MethodDelete || rec.path != "/v1/servers/5/snapshots/nightly" {
		t.Errorf("request = %s %s, want DELETE /v1/servers/5/snapshots/nightly", rec.method, rec.path)
	}
	if !strings.Contains(out.String(), "delete") || !strings.Contains(out.String(), "task-1") {
		t.Errorf("output missing action/task: %q", out.String())
	}
	if strings.Contains(errBuf.String(), "WARNING") {
		t.Errorf("--force still emitted a warning/prompt: %q", errBuf.String())
	}
}

func TestServerSnapshotsDeleteConfirmsYes(t *testing.T) {
	rec := &snapshotActionRecorder{}
	srv := newSnapshotActionServer(rec)
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var out, errBuf bytes.Buffer
	if err := serverSnapshotsDelete([]string{"5", "nightly"}, &out, &errBuf, strings.NewReader("y\n")); err != nil {
		t.Fatalf("serverSnapshotsDelete error = %v", err)
	}
	if rec.requests != 1 {
		t.Errorf("requests = %d, want 1 (confirmed delete)", rec.requests)
	}
	if !strings.Contains(errBuf.String(), "WARNING") {
		t.Errorf("stderr missing warning: %q", errBuf.String())
	}
	if strings.Contains(out.String(), "WARNING") {
		t.Errorf("warning leaked into stdout: %q", out.String())
	}
}

func TestServerSnapshotsDeleteAborts(t *testing.T) {
	rec := &snapshotActionRecorder{}
	srv := newSnapshotActionServer(rec)
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var out, errBuf bytes.Buffer
	err := serverSnapshotsDelete([]string{"5", "nightly"}, &out, &errBuf, strings.NewReader("n\n"))
	if err == nil {
		t.Fatal("declined confirmation error = nil, want abort error")
	}
	if !strings.Contains(err.Error(), "aborted") {
		t.Errorf("error = %v, want abort", err)
	}
	if rec.requests != 0 {
		t.Errorf("declined confirmation still made %d requests, want 0", rec.requests)
	}
}

func TestServerSnapshotsDeleteMissingName(t *testing.T) {
	var out, errBuf bytes.Buffer
	err := serverSnapshotsDelete([]string{"5"}, &out, &errBuf, nil)
	if err == nil {
		t.Fatal("missing name error = nil, want error")
	}
	if !strings.Contains(err.Error(), "snapshot name") {
		t.Errorf("error = %v, want mention of snapshot name", err)
	}
}

func TestServerSnapshotsRestoreForce(t *testing.T) {
	rec := &snapshotActionRecorder{}
	srv := newSnapshotActionServer(rec)
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var out, errBuf bytes.Buffer
	if err := serverSnapshotsRestore([]string{"5", "nightly", "--force"}, &out, &errBuf, nil); err != nil {
		t.Fatalf("serverSnapshotsRestore --force error = %v", err)
	}
	if rec.method != http.MethodPost || rec.path != "/v1/servers/5/snapshots/nightly/revert" {
		t.Errorf("request = %s %s, want POST /v1/servers/5/snapshots/nightly/revert", rec.method, rec.path)
	}
	if !strings.Contains(out.String(), "restore") || !strings.Contains(out.String(), "task-1") {
		t.Errorf("output missing action/task: %q", out.String())
	}
}

func TestServerSnapshotsRestoreConfirmsWithWarning(t *testing.T) {
	rec := &snapshotActionRecorder{}
	srv := newSnapshotActionServer(rec)
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var out, errBuf bytes.Buffer
	if err := serverSnapshotsRestore([]string{"5", "nightly"}, &out, &errBuf, strings.NewReader("y\n")); err != nil {
		t.Fatalf("serverSnapshotsRestore error = %v", err)
	}
	if rec.requests != 1 {
		t.Errorf("requests = %d, want 1 (confirmed restore)", rec.requests)
	}
	e := errBuf.String()
	if !strings.Contains(e, "WARNING") || !strings.Contains(strings.ToUpper(e), "REBOOTS") {
		t.Errorf("stderr missing prominent restore warning: %q", e)
	}
}

func TestServerSnapshotsRestoreAPIError(t *testing.T) {
	rec := &snapshotActionRecorder{status: http.StatusNotFound}
	srv := newSnapshotActionServer(rec)
	defer srv.Close()
	t.Setenv("NETCUP_API_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")

	var out, errBuf bytes.Buffer
	err := serverSnapshotsRestore([]string{"5", "nightly", "--force"}, &out, &errBuf, nil)
	if err == nil {
		t.Fatal("404 error = nil, want API error")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %v, want mention of 404", err)
	}
}

func TestServerSnapshotsCreateHelpSubcommandShowsUsage(t *testing.T) {
	var out, errBuf bytes.Buffer
	if err := serverSnapshotsCreate([]string{"help"}, &out, &errBuf, nil); err != nil {
		t.Fatalf("serverSnapshotsCreate help error = %v", err)
	}
	if !strings.Contains(out.String(), "netcupctl server snapshots create") {
		t.Errorf("help output missing usage:\n%s", out.String())
	}
}

func TestServerSnapshotsHelpShowsUsage(t *testing.T) {
	var out, errBuf bytes.Buffer
	if err := serverSnapshots([]string{"help"}, &out, &errBuf, nil); err != nil {
		t.Fatalf("serverSnapshots help error = %v", err)
	}
	o := out.String()
	if !strings.Contains(o, "create") || !strings.Contains(o, "restore") || !strings.Contains(o, "WARNING") {
		t.Errorf("help output missing create/restore/warning:\n%s", o)
	}
}
