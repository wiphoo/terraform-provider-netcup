package vcr

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// RunWithRDNSRestore runs a package's tests via m.Run(), wrapping them with
// PTR capture/restore in record mode.
//
// In replay mode (VCR_RECORD unset — the default, credential-free path) it is
// a pure passthrough that makes no network calls. In record mode
// (VCR_RECORD=1, i.e. `make acc-record`) the rDNS VCR tests set and delete the
// live NETCUP_TEST_IP's PTR to regenerate cassettes; without a restore, a
// maintainer refreshing cassettes against a normal server IP would exit with
// their live reverse DNS cleared. This captures the IP's existing PTR before
// the tests run and restores it afterward.
//
// Call it from a package-level TestMain:
//
//	func TestMain(m *testing.M) { os.Exit(vcr.RunWithRDNSRestore(m)) }
//
// The restore uses a plain live client (no recorder transport) and runs after
// m.Run() returns — i.e. after all cassettes are saved — so it never appears
// in a recording. With `make acc-record`'s `-p 1`, packages run serially, so
// each package captures the value the previous one restored and the original
// PTR survives the whole pass.
func RunWithRDNSRestore(m *testing.M) int {
	if os.Getenv("VCR_RECORD") != "1" {
		return m.Run()
	}

	ip := os.Getenv("NETCUP_TEST_IP")
	token := os.Getenv("NETCUP_ACCESS_TOKEN")
	if ip == "" || token == "" {
		// The recording tests' own guards report the missing input; just run.
		return m.Run()
	}

	client := netcup.New(
		netcup.WithAPIEndpoint(netcup.DefaultAPIEndpoint),
		netcup.WithAccessToken(token),
	)

	var original string
	entry, err := client.GetRDNS(context.Background(), ip)
	if err != nil {
		var apiErr *netcup.APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
			fmt.Fprintf(os.Stderr, "vcr: failed to capture original PTR for %s: %v; aborting recording to avoid leaving PTR state unknown\n", ip, err)
			return 1
		}
	}
	if entry != nil {
		original = entry.Hostname
	}

	code := m.Run()

	// original == "" means the IP had no PTR; leaving it cleared matches the
	// pre-test state, so there is nothing to restore.
	if original != "" {
		if _, err := client.SetRDNS(context.Background(), ip, original); err != nil {
			fmt.Fprintf(os.Stderr, "vcr: failed to restore original PTR %q for %s after recording: %v\n", original, ip, err)
			// Surface the restore failure as a non-zero exit so a maintainer
			// running `make acc-record` cannot miss that the live PTR was left
			// cleared. Don't clobber an existing test-failure code.
			if code == 0 {
				code = 1
			}
		}
	}
	return code
}
