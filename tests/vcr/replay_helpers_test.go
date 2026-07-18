package vcr

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/dnaeon/go-vcr/cassette"
	"gopkg.in/yaml.v2"
)

// skipInRecordMode skips a test when VCR_RECORD=1. Some v0.3.0 cassettes are
// authored from the documented SCP OpenAPI schema rather than a live capture,
// because the interaction they model can't be reproduced idempotently against a
// live server, or a live recording would leak an identifier the save filter has
// no rule for: a task that ends in ERROR (can't be forced on demand), an
// *active* rescue system or rescue enable/disable (each reboots the server, and
// enabling twice is a 400), a power change (reboots the server), an empty
// snapshot list (depends on the server having no snapshots), and the snapshot
// list itself (SnapshotMinimal.uuid is an unredacted live resource identifier).
// These stay replay-only so `make acc-record` neither reboots the maintainer's
// server, commits a live UUID, nor overwrites a hand-authored fixture with a
// non-matching live one. The remaining read-only cassettes (imageflavours and
// rescue status inactive — neither carries a UUID) are omitted here and remain
// live-refreshable. See CONTRIBUTING.md.
func skipInRecordMode(t *testing.T) {
	t.Helper()
	if os.Getenv("VCR_RECORD") == "1" {
		t.Skip("cassette authored from the documented SCP schema; not reproducible against a live server — see CONTRIBUTING.md (\"Redaction\" / v0.3.0 cassettes)")
	}
}

// taskURLPattern extracts the UUID from a GET /v1/tasks/{uuid} request URL.
var taskURLPattern = regexp.MustCompile(`/v1/tasks/([^/?]+)`)

// taskUUIDFromCassette returns the task UUID embedded in the first
// GET /v1/tasks/{uuid} interaction of the named cassette. It lets a replay-only
// task test (e.g. the ERROR terminal) drive WaitForTask with the exact UUID the
// committed cassette was authored with, mirroring how ServerIDForTest derives a
// server ID — so the value never has to be duplicated as a Go constant.
func taskUUIDFromCassette(t *testing.T, cassetteName string) string {
	t.Helper()
	path := filepath.Join("testdata", "cassettes", cassetteName+".yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read task cassette %q: %v", path, err)
	}
	var c cassette.Cassette
	if err := yaml.Unmarshal(data, &c); err != nil {
		t.Fatalf("parse task cassette %q: %v", path, err)
	}
	for _, ia := range c.Interactions {
		if ia == nil {
			continue
		}
		if m := taskURLPattern.FindStringSubmatch(ia.URL); m != nil {
			return m[1]
		}
	}
	t.Fatalf("task cassette %q does not contain a /v1/tasks/{uuid} request URL", path)
	return ""
}
