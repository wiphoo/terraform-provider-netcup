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

	// original == "" means the IP had no PTR. The record-mode rDNS tests may
	// have left a test hostname behind (e.g. TestDeleteRDNS sets then deletes
	// the PTR, but deletion is asynchronous), so clear any residual PTR and
	// confirm it is gone — don't just assume the pre-test empty state survived.
	// When original != "", restore it and confirm the read-back converges.
	if original != "" {
		if _, err := client.SetRDNS(context.Background(), ip, original); err != nil {
			fmt.Fprintf(os.Stderr, "vcr: failed to restore original PTR %q for %s after recording: %v\n", original, ip, err)
			// Surface the restore failure as a non-zero exit so a maintainer
			// running `make acc-record` cannot miss that the live PTR was left
			// cleared. Don't clobber an existing test-failure code.
			if code == 0 {
				code = 1
			}
		} else if _, err := client.ConfirmRDNS(context.Background(), ip, &netcup.RdnsEntry{Hostname: original}); err != nil {
			fmt.Fprintf(os.Stderr, "vcr: failed to confirm restored PTR %q for %s: %v\n", original, ip, err)
			if code == 0 {
				code = 1
			}
		}
	} else {
		if err := client.DeleteRDNS(context.Background(), ip); err != nil {
			// A 404 means there is no PTR to clear — already the desired state.
			var apiErr *netcup.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
				fmt.Fprintf(os.Stderr, "vcr: failed to clear residual PTR for %s after recording: %v\n", ip, err)
				if code == 0 {
					code = 1
				}
			}
		}
		// Confirm the PTR is empty: deletion is asynchronous, so even after a
		// successful DeleteRDNS (or a 404 from a prior async delete) the
		// read-back may still reflect the old value until it propagates.
		if _, err := client.ConfirmRDNS(context.Background(), ip, &netcup.RdnsEntry{Hostname: ""}); err != nil {
			fmt.Fprintf(os.Stderr, "vcr: failed to confirm cleared PTR for %s after recording: %v\n", ip, err)
			if code == 0 {
				code = 1
			}
		}
	}
	return code
}
