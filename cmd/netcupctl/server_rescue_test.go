package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// rescueRecorder captures what the fake SCP server saw and lets a test steer
// the responses of the rescuesystem endpoints.
type rescueRecorder struct {
	postCalls   int
	deleteCalls int

	// active/password drive GET /rescuesystem responses.
	active   bool
	password string // "" → null in the GET response

	// mutateStatus overrides the POST/DELETE status (0 → 202 Accepted).
	mutateStatus int
	// taskState drives GET /v1/tasks state ("" → FINISHED).
	taskState string
}

func newRescueServer(rec *rescueRecorder) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/tasks/"):
			ts := rec.taskState
			if ts == "" {
				ts = "FINISHED"
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"task-1","state":"` + ts + `"}`))

		case strings.HasSuffix(r.URL.Path, "/rescuesystem"):
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				if rec.active {
					pw := "null"
					if rec.password != "" {
						pw = `"` + rec.password + `"`
					}
					_, _ = w.Write([]byte(`{"active":true,"password":` + pw + `}`))
				} else {
					_, _ = w.Write([]byte(`{"active":false,"password":null}`))
				}
			case http.MethodPost, http.MethodDelete:
				if r.Method == http.MethodPost {
					rec.postCalls++
				} else {
					rec.deleteCalls++
				}
				status := rec.mutateStatus
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
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func setRescueEnv(t *testing.T, url string) {
	t.Helper()
	t.Setenv("NETCUP_API_ENDPOINT", url)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")
}

func TestServerRescueStatusActive(t *testing.T) {
	rec := &rescueRecorder{active: true, password: "rescue-pw"}
	srv := newRescueServer(rec)
	defer srv.Close()
	setRescueEnv(t, srv.URL)

	var out, errBuf bytes.Buffer
	if err := serverRescue([]string{"status", "5"}, &out, &errBuf, nil); err != nil {
		t.Fatalf("serverRescue status error = %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "active") || !strings.Contains(s, "rescue-pw") {
		t.Errorf("status output missing active/password: %q", s)
	}
}

func TestServerRescueStatusInactive(t *testing.T) {
	rec := &rescueRecorder{active: false}
	srv := newRescueServer(rec)
	defer srv.Close()
	setRescueEnv(t, srv.URL)

	var out, errBuf bytes.Buffer
	if err := serverRescue([]string{"status", "5"}, &out, &errBuf, nil); err != nil {
		t.Fatalf("serverRescue status error = %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "inactive") {
		t.Errorf("status output missing inactive: %q", s)
	}
	if strings.Contains(s, "Password") {
		t.Errorf("inactive status should not print a password line: %q", s)
	}
}

func TestServerRescueStatusJSON(t *testing.T) {
	rec := &rescueRecorder{active: true, password: "rescue-pw"}
	srv := newRescueServer(rec)
	defer srv.Close()
	setRescueEnv(t, srv.URL)

	var out, errBuf bytes.Buffer
	if err := serverRescue([]string{"status", "5", "--json"}, &out, &errBuf, nil); err != nil {
		t.Fatalf("serverRescue status --json error = %v", err)
	}
	s := strings.TrimSpace(out.String())
	if !strings.HasPrefix(s, "{") || !strings.Contains(s, `"active":true`) || !strings.Contains(s, `"password":"rescue-pw"`) {
		t.Errorf("JSON status output unexpected: %q", s)
	}
}

func TestServerRescueEnableConfirmYes(t *testing.T) {
	rec := &rescueRecorder{}
	srv := newRescueServer(rec)
	defer srv.Close()
	setRescueEnv(t, srv.URL)

	var out, errBuf bytes.Buffer
	if err := serverRescue([]string{"enable", "5"}, &out, &errBuf, strings.NewReader("y\n")); err != nil {
		t.Fatalf("serverRescue enable error = %v", err)
	}
	if rec.postCalls != 1 {
		t.Errorf("postCalls = %d, want 1", rec.postCalls)
	}
	e := errBuf.String()
	if !strings.Contains(e, "WARNING") || !strings.Contains(strings.ToLower(e), "reboot") {
		t.Errorf("stderr missing reboot warning: %q", e)
	}
	if strings.Contains(out.String(), "WARNING") {
		t.Errorf("warning leaked into stdout: %q", out.String())
	}
	// Without --wait the accepted task is reported with the poll hint.
	if !strings.Contains(out.String(), "use --wait") {
		t.Errorf("output missing accepted-task hint: %q", out.String())
	}
}

func TestServerRescueEnableAbort(t *testing.T) {
	rec := &rescueRecorder{}
	srv := newRescueServer(rec)
	defer srv.Close()
	setRescueEnv(t, srv.URL)

	var out, errBuf bytes.Buffer
	err := serverRescue([]string{"enable", "5"}, &out, &errBuf, strings.NewReader("n\n"))
	if err == nil {
		t.Fatal("serverRescue enable (declined) error = nil, want abort error")
	}
	if !strings.Contains(err.Error(), "aborted") {
		t.Errorf("error = %v, want abort", err)
	}
	if rec.postCalls != 0 {
		t.Errorf("declined confirmation still issued %d POST calls, want 0", rec.postCalls)
	}
	if !strings.Contains(errBuf.String(), "Aborted") {
		t.Errorf("stderr missing abort notice: %q", errBuf.String())
	}
}

func TestServerRescueEnableForceSkipsPrompt(t *testing.T) {
	rec := &rescueRecorder{}
	srv := newRescueServer(rec)
	defer srv.Close()
	setRescueEnv(t, srv.URL)

	var out, errBuf bytes.Buffer
	// nil stdin: with --force there must be no prompt to read.
	if err := serverRescue([]string{"enable", "5", "--force"}, &out, &errBuf, nil); err != nil {
		t.Fatalf("serverRescue enable --force error = %v", err)
	}
	if rec.postCalls != 1 {
		t.Errorf("postCalls = %d, want 1", rec.postCalls)
	}
	if strings.Contains(errBuf.String(), "WARNING") {
		t.Errorf("--force should not print a warning/prompt: %q", errBuf.String())
	}
}

func TestServerRescueEnableWaitReadsBackPassword(t *testing.T) {
	// After activation the fake server reports active with a password, which
	// the read-back GET should surface.
	rec := &rescueRecorder{active: true, password: "post-activation-pw", taskState: "FINISHED"}
	srv := newRescueServer(rec)
	defer srv.Close()
	setRescueEnv(t, srv.URL)

	var out, errBuf bytes.Buffer
	if err := serverRescue([]string{"enable", "5", "--yes", "--wait"}, &out, &errBuf, nil); err != nil {
		t.Fatalf("serverRescue enable --wait error = %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "FINISHED") {
		t.Errorf("output missing final task state: %q", s)
	}
	if !strings.Contains(s, "post-activation-pw") {
		t.Errorf("output missing read-back rescue password: %q", s)
	}
}

func TestServerRescueEnableWaitJSON(t *testing.T) {
	rec := &rescueRecorder{active: true, password: "pw-json", taskState: "FINISHED"}
	srv := newRescueServer(rec)
	defer srv.Close()
	setRescueEnv(t, srv.URL)

	var out, errBuf bytes.Buffer
	if err := serverRescue([]string{"enable", "5", "--force", "--wait", "--json"}, &out, &errBuf, nil); err != nil {
		t.Fatalf("serverRescue enable --wait --json error = %v", err)
	}
	s := strings.TrimSpace(out.String())
	if !strings.Contains(s, `"action":"enable"`) || !strings.Contains(s, `"password":"pw-json"`) {
		t.Errorf("JSON enable output unexpected: %q", s)
	}
}

func TestServerRescueDisableForceWait(t *testing.T) {
	rec := &rescueRecorder{taskState: "FINISHED"}
	srv := newRescueServer(rec)
	defer srv.Close()
	setRescueEnv(t, srv.URL)

	var out, errBuf bytes.Buffer
	if err := serverRescue([]string{"disable", "5", "--force", "--wait"}, &out, &errBuf, nil); err != nil {
		t.Fatalf("serverRescue disable --wait error = %v", err)
	}
	if rec.deleteCalls != 1 {
		t.Errorf("deleteCalls = %d, want 1", rec.deleteCalls)
	}
	if !strings.Contains(out.String(), "FINISHED") {
		t.Errorf("output missing final task state: %q", out.String())
	}
}

func TestServerRescueDisableConfirmYes(t *testing.T) {
	rec := &rescueRecorder{}
	srv := newRescueServer(rec)
	defer srv.Close()
	setRescueEnv(t, srv.URL)

	var out, errBuf bytes.Buffer
	if err := serverRescue([]string{"disable", "5"}, &out, &errBuf, strings.NewReader("yes\n")); err != nil {
		t.Fatalf("serverRescue disable error = %v", err)
	}
	if rec.deleteCalls != 1 {
		t.Errorf("deleteCalls = %d, want 1", rec.deleteCalls)
	}
	if !strings.Contains(strings.ToLower(errBuf.String()), "reboot") {
		t.Errorf("stderr missing reboot warning: %q", errBuf.String())
	}
}

func TestServerRescueEnableAPIError(t *testing.T) {
	// 400: rescue system currently active.
	rec := &rescueRecorder{mutateStatus: http.StatusBadRequest}
	srv := newRescueServer(rec)
	defer srv.Close()
	setRescueEnv(t, srv.URL)

	var out, errBuf bytes.Buffer
	err := serverRescue([]string{"enable", "5", "--force"}, &out, &errBuf, nil)
	if err == nil {
		t.Fatal("serverRescue enable error = nil, want API error")
	}
}

func TestServerRescueJSONWithPromptKeepsStdoutClean(t *testing.T) {
	rec := &rescueRecorder{}
	srv := newRescueServer(rec)
	defer srv.Close()
	setRescueEnv(t, srv.URL)

	// --json without --force still prompts; the warning/prompt must go to
	// stderr so stdout stays parseable JSON.
	var out, errBuf bytes.Buffer
	if err := serverRescue([]string{"enable", "5", "--json"}, &out, &errBuf, strings.NewReader("y\n")); err != nil {
		t.Fatalf("serverRescue enable --json error = %v", err)
	}
	s := strings.TrimSpace(out.String())
	if !strings.HasPrefix(s, "{") {
		t.Errorf("stdout is not clean JSON: %q", s)
	}
	if !strings.Contains(errBuf.String(), "WARNING") {
		t.Errorf("stderr missing warning: %q", errBuf.String())
	}
}

func TestServerRescueMissingID(t *testing.T) {
	var out, errBuf bytes.Buffer
	err := serverRescue([]string{"status"}, &out, &errBuf, nil)
	if err == nil {
		t.Fatal("serverRescue status (no id) error = nil, want error")
	}
	// The error carries the rescue context (usage itself is printed to the
	// process stderr by parseServerIDArg, not the injected buffer).
	if !strings.Contains(err.Error(), "server rescue status requires a server ID") {
		t.Errorf("error = %v, want rescue-scoped missing-ID message", err)
	}
}

func TestServerRescueInvalidID(t *testing.T) {
	var out, errBuf bytes.Buffer
	err := serverRescue([]string{"status", "notanumber"}, &out, &errBuf, nil)
	if err == nil {
		t.Fatal("serverRescue status (bad id) error = nil, want error")
	}
}

func TestServerRescueUnknownSubcommand(t *testing.T) {
	var out, errBuf bytes.Buffer
	err := serverRescue([]string{"frobnicate", "5"}, &out, &errBuf, nil)
	if err == nil {
		t.Fatal("serverRescue unknown subcommand error = nil, want error")
	}
}
