package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// powerRecorder captures what the fake SCP server saw on the PATCH call and
// lets a test steer the PATCH status and the polled task state.
type powerRecorder struct {
	patchCalls  int
	state       string
	option      string
	patchStatus int    // PATCH response status; 0 → 202 Accepted
	taskState   string // GET /v1/tasks state; "" → FINISHED
}

func newPowerServer(rec *powerRecorder) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/v1/servers/"):
			rec.patchCalls++
			rec.option = r.URL.Query().Get("stateOption")
			var body struct {
				State string `json:"state"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			rec.state = body.State

			status := rec.patchStatus
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
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/tasks/"):
			ts := rec.taskState
			if ts == "" {
				ts = "FINISHED"
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-1","state":"` + ts + `"}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/servers/"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":5,"name":"web-01","serverLiveInfo":{"state":"RUNNING"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func setPowerEnv(t *testing.T, url string) {
	t.Helper()
	t.Setenv("NETCUP_API_ENDPOINT", url)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")
}

func TestServerPowerStatus(t *testing.T) {
	rec := &powerRecorder{}
	srv := newPowerServer(rec)
	defer srv.Close()
	setPowerEnv(t, srv.URL)

	var buf bytes.Buffer
	if err := serverPower([]string{"status", "5"}, &buf, nil); err != nil {
		t.Fatalf("serverPower status error = %v", err)
	}
	if !strings.Contains(buf.String(), "RUNNING") {
		t.Errorf("output missing live state: %q", buf.String())
	}
	if rec.patchCalls != 0 {
		t.Errorf("status issued %d PATCH calls, want 0", rec.patchCalls)
	}
}

func TestServerPowerOn(t *testing.T) {
	rec := &powerRecorder{}
	srv := newPowerServer(rec)
	defer srv.Close()
	setPowerEnv(t, srv.URL)

	var buf bytes.Buffer
	// on causes no downtime, so no confirmation and no --force needed.
	if err := serverPower([]string{"on", "5"}, &buf, nil); err != nil {
		t.Fatalf("serverPower on error = %v", err)
	}
	if rec.state != "ON" {
		t.Errorf("state = %q, want ON", rec.state)
	}
	if rec.option != "" {
		t.Errorf("stateOption = %q, want empty", rec.option)
	}
	if !strings.Contains(buf.String(), "ON") {
		t.Errorf("output missing requested state: %q", buf.String())
	}
}

func TestServerPowerOffConfirmYes(t *testing.T) {
	rec := &powerRecorder{}
	srv := newPowerServer(rec)
	defer srv.Close()
	setPowerEnv(t, srv.URL)

	var buf bytes.Buffer
	if err := serverPower([]string{"off", "5"}, &buf, strings.NewReader("y\n")); err != nil {
		t.Fatalf("serverPower off error = %v", err)
	}
	if rec.state != "OFF" || rec.option != "" {
		t.Errorf("state=%q option=%q, want OFF and no option", rec.state, rec.option)
	}
	out := buf.String()
	if !strings.Contains(out, "WARNING") || !strings.Contains(strings.ToLower(out), "downtime") {
		t.Errorf("output missing downtime warning: %q", out)
	}
}

func TestServerPowerOffAbort(t *testing.T) {
	rec := &powerRecorder{}
	srv := newPowerServer(rec)
	defer srv.Close()
	setPowerEnv(t, srv.URL)

	var buf bytes.Buffer
	err := serverPower([]string{"off", "5"}, &buf, strings.NewReader("n\n"))
	if err == nil {
		t.Fatal("serverPower off (declined) error = nil, want abort error")
	}
	if !strings.Contains(err.Error(), "aborted") {
		t.Errorf("error = %v, want abort", err)
	}
	if rec.patchCalls != 0 {
		t.Errorf("declined confirmation still issued %d PATCH calls, want 0", rec.patchCalls)
	}
	if !strings.Contains(buf.String(), "Aborted") {
		t.Errorf("output missing abort notice: %q", buf.String())
	}
}

func TestServerPowerOffForceSkipsPrompt(t *testing.T) {
	rec := &powerRecorder{}
	srv := newPowerServer(rec)
	defer srv.Close()
	setPowerEnv(t, srv.URL)

	var buf bytes.Buffer
	// No reader provided; --force must bypass the prompt entirely.
	if err := serverPower([]string{"off", "5", "--force"}, &buf, nil); err != nil {
		t.Fatalf("serverPower off --force error = %v", err)
	}
	if rec.patchCalls != 1 || rec.state != "OFF" {
		t.Errorf("patchCalls=%d state=%q, want 1 and OFF", rec.patchCalls, rec.state)
	}
}

func TestServerPowerOffHard(t *testing.T) {
	rec := &powerRecorder{}
	srv := newPowerServer(rec)
	defer srv.Close()
	setPowerEnv(t, srv.URL)

	var buf bytes.Buffer
	if err := serverPower([]string{"off", "5", "--hard", "--yes"}, &buf, nil); err != nil {
		t.Fatalf("serverPower off --hard error = %v", err)
	}
	if rec.state != "OFF" || rec.option != "POWEROFF" {
		t.Errorf("state=%q option=%q, want OFF and POWEROFF", rec.state, rec.option)
	}
}

func TestServerPowerRebootSoftAndHard(t *testing.T) {
	cases := []struct {
		args   []string
		option string
	}{
		{[]string{"reboot", "5", "--force"}, "POWERCYCLE"},
		{[]string{"reboot", "5", "--hard", "--force"}, "RESET"},
	}
	for _, tc := range cases {
		rec := &powerRecorder{}
		srv := newPowerServer(rec)
		setPowerEnv(t, srv.URL)

		var buf bytes.Buffer
		if err := serverPower(tc.args, &buf, nil); err != nil {
			srv.Close()
			t.Fatalf("serverPower %v error = %v", tc.args, err)
		}
		if rec.state != "ON" || rec.option != tc.option {
			t.Errorf("args %v: state=%q option=%q, want ON and %s", tc.args, rec.state, rec.option, tc.option)
		}
		srv.Close()
	}
}

func TestServerPowerSuspendHardUnsupported(t *testing.T) {
	rec := &powerRecorder{}
	srv := newPowerServer(rec)
	defer srv.Close()
	setPowerEnv(t, srv.URL)

	var buf bytes.Buffer
	err := serverPower([]string{"suspend", "5", "--hard", "--force"}, &buf, nil)
	if err == nil {
		t.Fatal("serverPower suspend --hard error = nil, want unsupported error")
	}
	if !strings.Contains(err.Error(), "--hard") {
		t.Errorf("error = %v, want mention of --hard", err)
	}
	if rec.patchCalls != 0 {
		t.Errorf("unsupported --hard still issued %d PATCH calls, want 0", rec.patchCalls)
	}
}

func TestServerPowerWaitPollsToTerminal(t *testing.T) {
	rec := &powerRecorder{taskState: "FINISHED"}
	srv := newPowerServer(rec)
	defer srv.Close()
	setPowerEnv(t, srv.URL)

	var buf bytes.Buffer
	if err := serverPower([]string{"off", "5", "--force", "--wait"}, &buf, nil); err != nil {
		t.Fatalf("serverPower off --wait error = %v", err)
	}
	if !strings.Contains(buf.String(), "FINISHED") {
		t.Errorf("output missing final task state: %q", buf.String())
	}
}

func TestServerPowerJSON(t *testing.T) {
	rec := &powerRecorder{}
	srv := newPowerServer(rec)
	defer srv.Close()
	setPowerEnv(t, srv.URL)

	var buf bytes.Buffer
	if err := serverPower([]string{"off", "5", "--force", "--json"}, &buf, nil); err != nil {
		t.Fatalf("serverPower off --json error = %v", err)
	}
	out := strings.TrimSpace(buf.String())
	if !strings.HasPrefix(out, "{") || !strings.Contains(out, `"requested":"OFF"`) {
		t.Errorf("JSON output unexpected: %q", out)
	}
}

func TestServerPowerInvalidID(t *testing.T) {
	var buf bytes.Buffer
	err := serverPower([]string{"off", "not-a-number", "--force"}, &buf, nil)
	if err == nil {
		t.Fatal("serverPower off <bad id> error = nil, want error")
	}
	if !strings.Contains(err.Error(), "invalid server ID") {
		t.Errorf("error = %v, want invalid server ID", err)
	}
}

func TestServerPowerMissingID(t *testing.T) {
	var buf bytes.Buffer
	err := serverPower([]string{"off", "--force"}, &buf, nil)
	if err == nil {
		t.Fatal("serverPower off (no id) error = nil, want error")
	}
	if !strings.Contains(err.Error(), "server ID") {
		t.Errorf("error = %v, want missing server ID", err)
	}
}

func TestServerPowerAPIError(t *testing.T) {
	rec := &powerRecorder{patchStatus: http.StatusServiceUnavailable}
	srv := newPowerServer(rec)
	defer srv.Close()
	setPowerEnv(t, srv.URL)

	var buf bytes.Buffer
	err := serverPower([]string{"off", "5", "--force"}, &buf, nil)
	if err == nil {
		t.Fatal("serverPower off (503) error = nil, want API error")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error = %v, want mention of 503", err)
	}
}

func TestServerPowerUnknownSubcommand(t *testing.T) {
	var buf bytes.Buffer
	err := serverPower([]string{"frobnicate", "5"}, &buf, nil)
	if err == nil {
		t.Fatal("serverPower <unknown> error = nil, want error")
	}
	if !strings.Contains(err.Error(), "unknown server power subcommand") {
		t.Errorf("error = %v, want unknown subcommand", err)
	}
}
