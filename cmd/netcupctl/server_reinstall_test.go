package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reinstallRecorder captures what the fake SCP server saw on the reinstall POST
// and lets a test steer the POST status and the polled task state.
type reinstallRecorder struct {
	postCalls  int
	body       serverImageSetupCapture
	postStatus int    // POST /v1/servers/{id}/image status; 0 → 202 Accepted
	taskState  string // GET /v1/tasks state; "" → FINISHED
}

// serverImageSetupCapture is a minimal decode target for the request body,
// declared locally to keep the test independent of the SDK's json tags shape.
type serverImageSetupCapture struct {
	ImageFlavourID            *int32  `json:"imageFlavourId"`
	Hostname                  *string `json:"hostname"`
	SSHKeyIDs                 []int32 `json:"sshKeyIds"`
	SSHPasswordAuthentication *bool   `json:"sshPasswordAuthentication"`
	CustomScript              *string `json:"customScript"`
}

func newReinstallServer(rec *reinstallRecorder) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/image"):
			rec.postCalls++
			_ = json.NewDecoder(r.Body).Decode(&rec.body)

			status := rec.postStatus
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
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func setReinstallEnv(t *testing.T, url string) {
	t.Helper()
	t.Setenv("NETCUP_API_ENDPOINT", url)
	t.Setenv("NETCUP_ACCESS_TOKEN", "test-token")
}

func TestServerReinstallImageSelectionForce(t *testing.T) {
	rec := &reinstallRecorder{}
	srv := newReinstallServer(rec)
	defer srv.Close()
	setReinstallEnv(t, srv.URL)

	var out, errBuf bytes.Buffer
	if err := serverReinstall([]string{"5", "--image", "42", "--force"}, &out, &errBuf, nil); err != nil {
		t.Fatalf("serverReinstall error = %v", err)
	}
	if rec.postCalls != 1 {
		t.Fatalf("postCalls = %d, want 1", rec.postCalls)
	}
	if rec.body.ImageFlavourID == nil || *rec.body.ImageFlavourID != 42 {
		t.Errorf("imageFlavourId = %v, want 42", rec.body.ImageFlavourID)
	}
	if !strings.Contains(out.String(), "42") || !strings.Contains(out.String(), "task-1") {
		t.Errorf("output missing image/task: %q", out.String())
	}
}

func TestServerReinstallPassthroughFlags(t *testing.T) {
	rec := &reinstallRecorder{}
	srv := newReinstallServer(rec)
	defer srv.Close()
	setReinstallEnv(t, srv.URL)

	var out, errBuf bytes.Buffer
	args := []string{"5", "--image", "42", "--force",
		"--hostname", "web-01", "--ssh-key", "1", "--ssh-key", "2", "--ssh-password-auth"}
	if err := serverReinstall(args, &out, &errBuf, nil); err != nil {
		t.Fatalf("serverReinstall error = %v", err)
	}
	if rec.body.Hostname == nil || *rec.body.Hostname != "web-01" {
		t.Errorf("hostname = %v, want web-01", rec.body.Hostname)
	}
	if len(rec.body.SSHKeyIDs) != 2 || rec.body.SSHKeyIDs[0] != 1 || rec.body.SSHKeyIDs[1] != 2 {
		t.Errorf("sshKeyIds = %v, want [1 2]", rec.body.SSHKeyIDs)
	}
	if rec.body.SSHPasswordAuthentication == nil || !*rec.body.SSHPasswordAuthentication {
		t.Errorf("sshPasswordAuthentication = %v, want true", rec.body.SSHPasswordAuthentication)
	}
}

func TestServerReinstallOmitsUnsetFields(t *testing.T) {
	rec := &reinstallRecorder{}
	srv := newReinstallServer(rec)
	defer srv.Close()
	setReinstallEnv(t, srv.URL)

	var out, errBuf bytes.Buffer
	if err := serverReinstall([]string{"5", "--image", "42", "--force"}, &out, &errBuf, nil); err != nil {
		t.Fatalf("serverReinstall error = %v", err)
	}
	if rec.body.Hostname != nil {
		t.Errorf("hostname should be nil, got %v", *rec.body.Hostname)
	}
	if rec.body.SSHPasswordAuthentication != nil {
		t.Errorf("sshPasswordAuthentication should be nil, got %v", *rec.body.SSHPasswordAuthentication)
	}
	if rec.body.CustomScript != nil {
		t.Errorf("customScript should be nil, got %v", *rec.body.CustomScript)
	}
}

func TestServerReinstallCustomScriptFromFile(t *testing.T) {
	rec := &reinstallRecorder{}
	srv := newReinstallServer(rec)
	defer srv.Close()
	setReinstallEnv(t, srv.URL)

	dir := t.TempDir()
	path := filepath.Join(dir, "bootstrap.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho hi\n"), 0o600); err != nil {
		t.Fatalf("write script: %v", err)
	}

	var out, errBuf bytes.Buffer
	if err := serverReinstall([]string{"5", "--image", "42", "--force", "--custom-script-file", path}, &out, &errBuf, nil); err != nil {
		t.Fatalf("serverReinstall error = %v", err)
	}
	if rec.body.CustomScript == nil || !strings.Contains(*rec.body.CustomScript, "echo hi") {
		t.Errorf("customScript = %v, want file contents", rec.body.CustomScript)
	}
}

func TestServerReinstallCustomScriptFromStdin(t *testing.T) {
	rec := &reinstallRecorder{}
	srv := newReinstallServer(rec)
	defer srv.Close()
	setReinstallEnv(t, srv.URL)

	var out, errBuf bytes.Buffer
	in := strings.NewReader("#!/bin/sh\necho from-stdin\n")
	if err := serverReinstall([]string{"5", "--image", "42", "--force", "--custom-script-file", "-"}, &out, &errBuf, in); err != nil {
		t.Fatalf("serverReinstall error = %v", err)
	}
	if rec.body.CustomScript == nil || !strings.Contains(*rec.body.CustomScript, "from-stdin") {
		t.Errorf("customScript = %v, want stdin contents", rec.body.CustomScript)
	}
}

// TestServerReinstallStdinScriptRequiresForce guards the fix for PR #107's P2
// review thread: with confirmation enabled, confirmAction reads stdin through a
// bufio.Reader that can read ahead past "y\n" and discard buffered bytes, which
// would truncate a script also piped through stdin. The command rejects that
// combination up front rather than silently submitting a mangled customScript.
func TestServerReinstallStdinScriptRequiresForce(t *testing.T) {
	rec := &reinstallRecorder{}
	srv := newReinstallServer(rec)
	defer srv.Close()
	setReinstallEnv(t, srv.URL)

	var out, errBuf bytes.Buffer
	// Both the confirmation answer and the script are piped through stdin — the
	// exact ambiguous case the guard rejects.
	in := strings.NewReader("y\n#!/bin/sh\necho hi\n")
	err := serverReinstall([]string{"5", "--image", "42", "--custom-script-file", "-"}, &out, &errBuf, in)
	if err == nil {
		t.Fatal("expected an error rejecting stdin script without --force, got nil")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error = %v, want it to mention --force", err)
	}
	if rec.postCalls != 0 {
		t.Errorf("postCalls = %d, want 0 (must reject before any reinstall)", rec.postCalls)
	}
}

func TestServerReinstallMutuallyExclusiveScriptFlags(t *testing.T) {
	rec := &reinstallRecorder{}
	srv := newReinstallServer(rec)
	defer srv.Close()
	setReinstallEnv(t, srv.URL)

	var out, errBuf bytes.Buffer
	err := serverReinstall([]string{"5", "--image", "42", "--force",
		"--custom-script", "echo hi", "--custom-script-file", "x"}, &out, &errBuf, nil)
	if err == nil {
		t.Fatal("expected mutually-exclusive error, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error = %v, want mutually exclusive", err)
	}
	if rec.postCalls != 0 {
		t.Errorf("postCalls = %d, want 0", rec.postCalls)
	}
}

func TestServerReinstallMissingImage(t *testing.T) {
	rec := &reinstallRecorder{}
	srv := newReinstallServer(rec)
	defer srv.Close()
	setReinstallEnv(t, srv.URL)

	var out, errBuf bytes.Buffer
	err := serverReinstall([]string{"5", "--force"}, &out, &errBuf, nil)
	if err == nil {
		t.Fatal("expected missing --image error, got nil")
	}
	if !strings.Contains(err.Error(), "--image") {
		t.Errorf("error = %v, want mention of --image", err)
	}
	if rec.postCalls != 0 {
		t.Errorf("postCalls = %d, want 0", rec.postCalls)
	}
}

func TestServerReinstallConfirmYesWipesWarning(t *testing.T) {
	rec := &reinstallRecorder{}
	srv := newReinstallServer(rec)
	defer srv.Close()
	setReinstallEnv(t, srv.URL)

	var out, errBuf bytes.Buffer
	if err := serverReinstall([]string{"5", "--image", "42"}, &out, &errBuf, strings.NewReader("y\n")); err != nil {
		t.Fatalf("serverReinstall error = %v", err)
	}
	if rec.postCalls != 1 {
		t.Errorf("postCalls = %d, want 1", rec.postCalls)
	}
	e := errBuf.String()
	if !strings.Contains(e, "WARNING") || !strings.Contains(strings.ToUpper(e), "WIPES") {
		t.Errorf("stderr missing data-loss warning: %q", e)
	}
	if strings.Contains(out.String(), "WARNING") {
		t.Errorf("warning leaked into stdout: %q", out.String())
	}
}

func TestServerReinstallAbort(t *testing.T) {
	rec := &reinstallRecorder{}
	srv := newReinstallServer(rec)
	defer srv.Close()
	setReinstallEnv(t, srv.URL)

	var out, errBuf bytes.Buffer
	err := serverReinstall([]string{"5", "--image", "42"}, &out, &errBuf, strings.NewReader("n\n"))
	if err == nil {
		t.Fatal("declined confirmation error = nil, want abort error")
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

func TestServerReinstallForceSkipsPrompt(t *testing.T) {
	rec := &reinstallRecorder{}
	srv := newReinstallServer(rec)
	defer srv.Close()
	setReinstallEnv(t, srv.URL)

	var out, errBuf bytes.Buffer
	// No reader provided; --force must bypass the prompt entirely.
	if err := serverReinstall([]string{"5", "--image", "42", "--force"}, &out, &errBuf, nil); err != nil {
		t.Fatalf("serverReinstall --force error = %v", err)
	}
	if rec.postCalls != 1 {
		t.Errorf("postCalls = %d, want 1", rec.postCalls)
	}
	if strings.Contains(errBuf.String(), "WARNING") {
		t.Errorf("--force still emitted a warning/prompt: %q", errBuf.String())
	}
}

func TestServerReinstallWaitPollsToTerminal(t *testing.T) {
	rec := &reinstallRecorder{taskState: "FINISHED"}
	srv := newReinstallServer(rec)
	defer srv.Close()
	setReinstallEnv(t, srv.URL)

	var out, errBuf bytes.Buffer
	if err := serverReinstall([]string{"5", "--image", "42", "--force", "--wait"}, &out, &errBuf, nil); err != nil {
		t.Fatalf("serverReinstall --wait error = %v", err)
	}
	if !strings.Contains(out.String(), "FINISHED") {
		t.Errorf("output missing final task state: %q", out.String())
	}
}

func TestServerReinstallJSON(t *testing.T) {
	rec := &reinstallRecorder{}
	srv := newReinstallServer(rec)
	defer srv.Close()
	setReinstallEnv(t, srv.URL)

	var out, errBuf bytes.Buffer
	if err := serverReinstall([]string{"5", "--image", "42", "--force", "--json"}, &out, &errBuf, nil); err != nil {
		t.Fatalf("serverReinstall --json error = %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout=%q", err, out.String())
	}
	if decoded["action"] != "reinstall" {
		t.Errorf("decoded action = %v, want reinstall", decoded["action"])
	}
}

func TestServerReinstallJSONWithPromptKeepsStdoutClean(t *testing.T) {
	rec := &reinstallRecorder{}
	srv := newReinstallServer(rec)
	defer srv.Close()
	setReinstallEnv(t, srv.URL)

	var out, errBuf bytes.Buffer
	if err := serverReinstall([]string{"5", "--image", "42", "--json"}, &out, &errBuf, strings.NewReader("y\n")); err != nil {
		t.Fatalf("serverReinstall --json (prompted) error = %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout=%q", err, out.String())
	}
	if !strings.Contains(errBuf.String(), "WARNING") {
		t.Errorf("stderr missing warning: %q", errBuf.String())
	}
}

func TestServerReinstallInvalidID(t *testing.T) {
	var out, errBuf bytes.Buffer
	err := serverReinstall([]string{"not-a-number", "--image", "42", "--force"}, &out, &errBuf, nil)
	if err == nil {
		t.Fatal("invalid id error = nil, want error")
	}
	if !strings.Contains(err.Error(), "invalid server ID") {
		t.Errorf("error = %v, want invalid server ID", err)
	}
}

func TestServerReinstallMissingID(t *testing.T) {
	var out, errBuf bytes.Buffer
	err := serverReinstall([]string{"--image", "42", "--force"}, &out, &errBuf, nil)
	if err == nil {
		t.Fatal("missing id error = nil, want error")
	}
	if !strings.Contains(err.Error(), "server ID") {
		t.Errorf("error = %v, want missing server ID", err)
	}
}

func TestServerReinstallAPIError(t *testing.T) {
	rec := &reinstallRecorder{postStatus: http.StatusUnprocessableEntity}
	srv := newReinstallServer(rec)
	defer srv.Close()
	setReinstallEnv(t, srv.URL)

	var out, errBuf bytes.Buffer
	err := serverReinstall([]string{"5", "--image", "42", "--force"}, &out, &errBuf, nil)
	if err == nil {
		t.Fatal("422 error = nil, want API error")
	}
	if !strings.Contains(err.Error(), "422") {
		t.Errorf("error = %v, want mention of 422", err)
	}
}
