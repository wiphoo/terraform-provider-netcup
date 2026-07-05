package vcr

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dnaeon/go-vcr/recorder"
)

// TestRedactInteractionAppliesAtSaveNotRoundTrip proves redactInteraction is
// wired as a save filter (AddSaveFilter), not a regular one (AddFilter): a
// live round trip in record mode must return the real response to the
// caller, and only the persisted cassette file should end up redacted.
//
// go-vcr's RoundTrip builds the *http.Response it hands back to the caller
// from the same *cassette.Interaction a regular AddFilter mutates, before
// that response is returned. Wiring redactInteraction via AddFilter instead
// of AddSaveFilter would make a live recording session observe the fake
// RFC 5737 IP instead of the real one — breaking, for example, a flow that
// reads a server's real IP from one call and uses it in a later live
// request. AddSaveFilter runs later, in Stop/Save, strictly after every live
// round trip has already returned its real response to the caller.
func TestRedactInteractionAppliesAtSaveNotRoundTrip(t *testing.T) {
	const realIP = "192.0.2.77"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ip":"` + realIP + `"}`))
	}))
	defer srv.Close()

	cassettePath := filepath.Join(t.TempDir(), "TestRedactInteractionAtSave")
	rec, err := recorder.NewAsMode(cassettePath, recorder.ModeRecording, nil)
	if err != nil {
		t.Fatalf("recorder.NewAsMode: %v", err)
	}
	rec.AddSaveFilter(redactInteraction)

	client := &http.Client{Transport: rec}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("live round trip: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}

	// The code under test must see the real response while recording.
	if !strings.Contains(string(body), realIP) {
		t.Fatalf("live caller got a redacted body instead of the real response: %s", body)
	}

	if err := rec.Stop(); err != nil {
		t.Fatalf("rec.Stop: %v", err)
	}

	saved, err := os.ReadFile(cassettePath + ".yaml")
	if err != nil {
		t.Fatalf("read saved cassette: %v", err)
	}
	if strings.Contains(string(saved), realIP) {
		t.Errorf("saved cassette still contains the real IP %q — the save filter did not redact it", realIP)
	}
}

// TestRDNSCassetteReplayMatchesRealIP is the full record-then-replay cycle
// for an rDNS-shaped URL: record a live interaction (whose URL embeds a real
// IP), let redactInteraction rewrite that IP at save time, then reopen the
// same cassette file in replay mode and issue a request built from the
// *same real IP* used during recording. Without matchInteraction (go-vcr's
// DefaultMatcher does exact method+URL string equality), this replay would
// fail with "interaction not found" because the committed cassette's URL now
// holds the redacted fake IP, not the real one — exactly the gap a future
// rDNS test (#43) would hit if its replay call is built from the real test
// IP (e.g. read from a maintainer's still-exported NETCUP_TEST_IP).
func TestRDNSCassetteReplayMatchesRealIP(t *testing.T) {
	const realIP = "192.0.2.88"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rdns":"host-example.com"}`))
	}))
	defer srv.Close()

	cassettePath := filepath.Join(t.TempDir(), "TestRDNSRoundTrip")
	realURL := srv.URL + "/v1/rdns/ipv4/" + realIP

	// Record.
	rec, err := recorder.NewAsMode(cassettePath, recorder.ModeRecording, nil)
	if err != nil {
		t.Fatalf("recorder.NewAsMode (record): %v", err)
	}
	rec.AddSaveFilter(redactInteraction)
	rec.SetMatcher(matchInteraction)

	if _, err := (&http.Client{Transport: rec}).Get(realURL); err != nil {
		t.Fatalf("live round trip: %v", err)
	}
	if err := rec.Stop(); err != nil {
		t.Fatalf("rec.Stop: %v", err)
	}

	saved, err := os.ReadFile(cassettePath + ".yaml")
	if err != nil {
		t.Fatalf("read saved cassette: %v", err)
	}
	if strings.Contains(string(saved), realIP) {
		t.Fatalf("saved cassette still contains the real IP %q", realIP)
	}

	// Replay, using the same real IP a naive future test would hardcode.
	replayRec, err := recorder.NewAsMode(cassettePath, recorder.ModeReplaying, nil)
	if err != nil {
		t.Fatalf("recorder.NewAsMode (replay): %v", err)
	}
	replayRec.SetMatcher(matchInteraction)
	defer func() { _ = replayRec.Stop() }()

	resp, err := (&http.Client{Transport: replayRec}).Get(realURL)
	if err != nil {
		t.Fatalf("replay with the real IP did not match the redacted cassette entry: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("replay status = %d, want 200", resp.StatusCode)
	}
}
