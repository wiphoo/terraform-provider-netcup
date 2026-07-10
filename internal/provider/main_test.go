package provider

import (
	"os"
	"testing"

	vcr "github.com/wiphoo/terraform-provider-netcup/tests/vcr"
)

// TestMain restores NETCUP_TEST_IP's PTR after a record-mode run of the
// provider-tier rDNS VCR tests (which set/delete it live under VCR_RECORD=1),
// so `make acc-record` does not leave a maintainer's live reverse DNS cleared.
// In replay mode it is a passthrough. See vcr.RunWithRDNSRestore.
func TestMain(m *testing.M) {
	os.Exit(vcr.RunWithRDNSRestore(m))
}
