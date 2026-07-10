package vcr

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// TestRDNSHostname is the redacted PTR value the rDNS record-mode prep and the
// committed replay cassettes share. Recording sets this exact value; the
// save-time hostname redactor is idempotent for host-<hash>.example.com names,
// so it survives redaction and a replay plan built from it still matches.
const TestRDNSHostname = "host-a1b2c3d4.example.com"

// NewLiveClient builds a plain (unrecorded) live *netcup.Client for token.
// Record-mode prep/restore and acceptance tests use it to reach the real SCP
// API directly, bypassing any go-vcr recorder transport.
func NewLiveClient(token string) *netcup.Client {
	return netcup.New(
		netcup.WithAPIEndpoint(netcup.DefaultAPIEndpoint),
		netcup.WithAccessToken(token),
	)
}

// LiveRDNSClient returns a NewLiveClient seeded from NETCUP_ACCESS_TOKEN, for
// VCR_RECORD=1 rDNS prep. It fatals if the token is unset so the unrecorded
// prep calls can never silently no-op.
func LiveRDNSClient(t *testing.T) *netcup.Client {
	t.Helper()
	token := os.Getenv("NETCUP_ACCESS_TOKEN")
	if token == "" {
		t.Fatal("VCR_RECORD=1 requires NETCUP_ACCESS_TOKEN")
	}
	return NewLiveClient(token)
}

// CapturePTR reads ip's current PTR hostname, treating a 404 (no PTR set) as an
// empty result. Any other error is returned.
func CapturePTR(client *netcup.Client, ip string) (string, error) {
	entry, err := client.GetRDNS(context.Background(), ip)
	if err != nil {
		if isNotFound(err) {
			return "", nil
		}
		return "", err
	}
	return entry.Hostname, nil
}

// EnsurePTR sets ip's PTR to hostname and waits (ConfirmRDNS) for the change to
// read back, absorbing the SCP API's asynchronous rDNS propagation.
func EnsurePTR(client *netcup.Client, ip, hostname string) error {
	if _, err := client.SetRDNS(context.Background(), ip, hostname); err != nil {
		return err
	}
	_, err := client.ConfirmRDNS(context.Background(), ip, &netcup.RdnsEntry{Hostname: hostname})
	return err
}

// EnsureNoPTR deletes ip's PTR (tolerating a 404 as already-absent) and waits
// for the empty read-back to propagate.
func EnsureNoPTR(client *netcup.Client, ip string) error {
	if err := client.DeleteRDNS(context.Background(), ip); err != nil && !isNotFound(err) {
		return err
	}
	_, err := client.ConfirmRDNS(context.Background(), ip, &netcup.RdnsEntry{Hostname: ""})
	return err
}

// SeedLivePTR sets ip's PTR to TestRDNSHostname via an unrecorded live client
// and waits for it to read back — VCR_RECORD=1 prep for a test whose recorded
// interaction needs an existing PTR. Fatals on error.
func SeedLivePTR(t *testing.T, ip string) {
	t.Helper()
	if err := EnsurePTR(LiveRDNSClient(t), ip, TestRDNSHostname); err != nil {
		t.Fatalf("rDNS record-mode seed prep for %s: %v", ip, err)
	}
}

// ClearLivePTR deletes ip's PTR via an unrecorded live client and waits for the
// empty read-back — VCR_RECORD=1 prep for a test whose recorded interaction
// needs no PTR. Fatals on error.
func ClearLivePTR(t *testing.T, ip string) {
	t.Helper()
	if err := EnsureNoPTR(LiveRDNSClient(t), ip); err != nil {
		t.Fatalf("rDNS record-mode clear prep for %s: %v", ip, err)
	}
}

// isNotFound reports whether err is a *netcup.APIError with a 404 status.
func isNotFound(err error) bool {
	var apiErr *netcup.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}
