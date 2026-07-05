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
