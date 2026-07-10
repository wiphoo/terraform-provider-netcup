package vcr

import (
	"os"
	"testing"
)

// TestMain restores NETCUP_TEST_IP's PTR after a record-mode run so
// `make acc-record` does not leave a maintainer's live reverse DNS cleared.
// In replay mode it is a passthrough. See RunWithRDNSRestore.
func TestMain(m *testing.M) {
	os.Exit(RunWithRDNSRestore(m))
}
