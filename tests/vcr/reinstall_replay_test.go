package vcr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/dnaeon/go-vcr/cassette"
	"gopkg.in/yaml.v2"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// TestReinstallServer202AndWait replays POST /v1/servers/{id}/image returning a
// 202 TaskInfo, then the GET /v1/tasks/{uuid} polls WaitForTask issues,
// transitioning RUNNING -> FINISHED. The UUID flows from the 202 body into
// WaitForTask, so no cassette-derived constant is needed; WithTaskPollInterval
// collapses the between-poll wait so the recorded transition replays without a
// real-time sleep. Replay-only: a live recording would perform a DESTRUCTIVE OS
// reinstall on the maintainer's server (see skipInRecordMode), exactly why the
// power/task cassettes are replay-only too.
func TestReinstallServer202AndWait(t *testing.T) {
	skipInRecordMode(t)

	const cassetteName = "TestReinstallServer202AndWait"
	client := NewClient(t, cassetteName, netcup.WithTaskPollInterval(time.Millisecond))
	id := ServerIDForTest(t, cassetteName)

	flavour := int32(42)
	task, err := client.ReinstallServer(context.Background(), id, netcup.ServerImageSetup{
		ImageFlavourID: &flavour,
	})
	if err != nil {
		t.Fatalf("ReinstallServer() error = %v", err)
	}
	if task == nil || task.UUID == "" {
		t.Fatalf("ReinstallServer() task = %+v, want a task with a UUID", task)
	}
	if task.State != netcup.TaskStatePending {
		t.Errorf("task.State = %q, want %q", task.State, netcup.TaskStatePending)
	}

	ctx, cancel := context.WithTimeout(context.Background(), replayWaitTimeout)
	defer cancel()
	final, err := client.WaitForTask(ctx, task.UUID)
	if err != nil {
		t.Fatalf("WaitForTask() error = %v", err)
	}
	if final.State != netcup.TaskStateFinished {
		t.Errorf("final.State = %q, want %q", final.State, netcup.TaskStateFinished)
	}
	if final.TaskProgress == nil || final.TaskProgress.ProgressInPercent != 100 {
		t.Errorf("final.TaskProgress = %+v, want ProgressInPercent 100", final.TaskProgress)
	}
	if len(final.Steps) == 0 {
		t.Error("final.Steps is empty, want the recorded wipe/install sub-steps")
	}
}

// TestReinstallServerCustomScript replays a reinstall carrying a full install
// body — image flavour, hostname, an additional user + password, and a
// customScript bootstrap — and asserts both the 202 TaskInfo decodes AND the
// on-the-wire request body. The latter matters because matchInteraction keys on
// method+URL only (tests/vcr/redact.go), so without an explicit body check a
// dropped/renamed json tag or a ReinstallServer wiring regression on any install
// field would still replay green. newCapturingClient records the outgoing body;
// it is normalized with the same save-time redaction (secrets -> fixed markers)
// and compared field-for-field against the committed cassette body. Replay-only
// (destructive).
func TestReinstallServerCustomScript(t *testing.T) {
	skipInRecordMode(t)

	const cassetteName = "TestReinstallServerCustomScript"
	var captured capturedBody
	client := newCapturingClient(t, cassetteName, &captured)
	id := ServerIDForTest(t, cassetteName)

	flavour := int32(42)
	// Already in redacted host-<hex>.example.com form so the save-time hostname
	// redaction is idempotent — the normalized body then equals the committed
	// cassette by value, not just by key set.
	hostname := "host-0a0b0c0d.example.com"
	user := "deploy"
	password := "s3cr3t-never-committed"
	script := "#!/bin/sh\necho provisioned > /etc/motd\n"
	task, err := client.ReinstallServer(context.Background(), id, netcup.ServerImageSetup{
		ImageFlavourID:         &flavour,
		Hostname:               &hostname,
		AdditionalUserUsername: &user,
		AdditionalUserPassword: &password,
		CustomScript:           &script,
	})
	if err != nil {
		t.Fatalf("ReinstallServer() error = %v", err)
	}
	if task == nil || task.State != netcup.TaskStatePending {
		t.Fatalf("task = %+v, want a PENDING task", task)
	}

	// Normalize the captured request body with the same redaction the recorder
	// applies at save time, then compare against the committed cassette body:
	// this is what actually exercises the "full install body" the test name
	// promises, since replay matching ignores the body entirely.
	gotBody := redactRequestBody("application/json", captured.body)
	wantBody := firstRequestBodyFromCassette(t, cassetteName)
	assertEqualJSON(t, gotBody, wantBody)
}

// TestReinstallServerAPIError replays a 422 rejection (an image flavour the
// server won't accept) and asserts ReinstallServer surfaces it as
// *netcup.APIError carrying the status code, with no task returned. Authored
// from the documented ValidationError shape, so replay-only.
func TestReinstallServerAPIError(t *testing.T) {
	skipInRecordMode(t)

	const cassetteName = "TestReinstallServerAPIError"
	client := NewClient(t, cassetteName)
	id := ServerIDForTest(t, cassetteName)

	flavour := int32(999999)
	task, err := client.ReinstallServer(context.Background(), id, netcup.ServerImageSetup{
		ImageFlavourID: &flavour,
	})
	if task != nil {
		t.Errorf("task = %+v, want nil on error", task)
	}
	var apiErr *netcup.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("ReinstallServer() error = %v, want *netcup.APIError", err)
	}
	if apiErr.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("StatusCode = %d, want 422", apiErr.StatusCode)
	}
}

// capturedBody holds the last non-empty request body a capturingTransport saw,
// so a test can assert what the SDK actually put on the wire.
type capturedBody struct {
	body string
}

// capturingTransport records the request body before delegating to the go-vcr
// recorder, then restores it so the recorder (and its matcher) still see an
// intact request. Used to observe the reinstall request body, which the
// method+URL-only matchInteraction never inspects.
type capturingTransport struct {
	inner    http.RoundTripper
	captured *capturedBody
}

func (c *capturingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		_ = req.Body.Close()
		if len(b) > 0 {
			c.captured.body = string(b)
		}
		req.Body = io.NopCloser(bytes.NewReader(b))
		req.ContentLength = int64(len(b))
	}
	return c.inner.RoundTrip(req)
}

// newCapturingClient is NewClient plus a transport shim that records the
// outgoing request body into captured. It reuses newRecorder so the cassette,
// matcher, and save-time scrub are wired identically to every other replay test.
func newCapturingClient(t *testing.T, cassetteName string, captured *capturedBody, extraOpts ...netcup.Option) *netcup.Client {
	t.Helper()
	rec, token := newRecorder(t, cassetteName)
	opts := []netcup.Option{
		netcup.WithAPIEndpoint(netcup.DefaultAPIEndpoint),
		netcup.WithHTTPClient(&http.Client{Transport: &capturingTransport{inner: rec, captured: captured}, Timeout: clientTimeout}),
		netcup.WithAccessToken(token),
	}
	opts = append(opts, extraOpts...)
	return netcup.New(opts...)
}

// firstRequestBodyFromCassette returns the request body of the first interaction
// in the named cassette that has one (the reinstall POST), so a body assertion
// compares against the committed fixture rather than a duplicated literal.
func firstRequestBodyFromCassette(t *testing.T, cassetteName string) string {
	t.Helper()
	path := filepath.Join("testdata", "cassettes", cassetteName+".yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cassette %q: %v", path, err)
	}
	var c cassette.Cassette
	if err := yaml.Unmarshal(data, &c); err != nil {
		t.Fatalf("parse cassette %q: %v", path, err)
	}
	for _, ia := range c.Interactions {
		if ia != nil && ia.Request.Body != "" {
			return ia.Request.Body
		}
	}
	t.Fatalf("cassette %q has no request with a body", cassetteName)
	return ""
}

// assertEqualJSON compares two JSON documents by value (order-independent): the
// redactor re-marshals from a Go map, which sorts keys alphabetically, so a byte
// comparison against the struct-ordered cassette body would spuriously fail.
func assertEqualJSON(t *testing.T, got, want string) {
	t.Helper()
	var g, w interface{}
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("parse normalized request body %q: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("parse cassette request body %q: %v", want, err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("redacted request body mismatch:\n got: %s\nwant: %s", got, want)
	}
}
