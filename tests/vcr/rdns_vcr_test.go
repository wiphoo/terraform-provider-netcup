package vcr

import (
	"context"
	"os"
	"testing"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// liveRDNSClient returns a plain (unrecorded) *netcup.Client for rDNS prep
// operations in record mode. It uses the live NETCUP_ACCESS_TOKEN so that
// SetRDNS/ConfirmRDNS/DeleteRDNS calls do not go through go-vcr and cannot
// leak into the cassette under test.
func liveRDNSClient(t *testing.T) *netcup.Client {
	t.Helper()
	token := os.Getenv("NETCUP_ACCESS_TOKEN")
	if token == "" {
		t.Fatal("VCR_RECORD=1 requires NETCUP_ACCESS_TOKEN")
	}
	return netcup.New(
		netcup.WithAPIEndpoint(netcup.DefaultAPIEndpoint),
		netcup.WithAccessToken(token),
	)
}

const testRDNSHostname = "host-a1b2c3d4.example.com"

// TestSetRDNS records POST /v1/rdns/ipv4. In record mode it calls SetRDNS
// with NETCUP_TEST_IP so the cassette captures a real SCP response.
func TestSetRDNS(t *testing.T) {
	const cassetteName = "TestSetRDNS"
	client := NewClient(t, cassetteName)
	ip := RDNSIPForTest(t, cassetteName)
	entry, err := client.SetRDNS(context.Background(), ip, testRDNSHostname)
	if err != nil {
		t.Fatalf("SetRDNS() error = %v", err)
	}
	if entry == nil {
		t.Fatal("SetRDNS() returned nil entry")
	}
	if entry.IP == "" {
		t.Error("SetRDNS() entry.IP is empty")
	}
	if entry.Hostname == "" {
		t.Error("SetRDNS() entry.Hostname is empty")
	}
}

// TestGetRDNS records GET /v1/rdns/ipv4/{ip}. In record mode it first sets
// the PTR on NETCUP_TEST_IP so the read-back has a value to return. Prep
// operations (SetRDNS + ConfirmRDNS) use an unrecorded live client so the
// cassette contains only the intended GET interaction — polling GETs from
// ConfirmRDNS never leak into the recording.
func TestGetRDNS(t *testing.T) {
	const cassetteName = "TestGetRDNS"
	client := NewClient(t, cassetteName)
	ip := RDNSIPForTest(t, cassetteName)

	if os.Getenv("VCR_RECORD") == "1" {
		live := liveRDNSClient(t)
		_, err := live.SetRDNS(context.Background(), ip, testRDNSHostname)
		if err != nil {
			t.Fatalf("SetRDNS (record-mode prep) error = %v", err)
		}
		// rDNS updates are applied asynchronously; confirm before reading so
		// the recorded GetRDNS response is not the stale pre-set value. Use
		// the unrecorded live client so polling GETs don't leak into the
		// cassette.
		if _, err := live.ConfirmRDNS(context.Background(), ip, &netcup.RdnsEntry{Hostname: testRDNSHostname}); err != nil {
			t.Fatalf("ConfirmRDNS (record-mode prep) error = %v", err)
		}
	}

	entry, err := client.GetRDNS(context.Background(), ip)
	if err != nil {
		t.Fatalf("GetRDNS() error = %v", err)
	}
	if entry == nil {
		t.Fatal("GetRDNS() returned nil entry")
	}
	if entry.Hostname == "" {
		t.Error("GetRDNS() entry.Hostname is empty")
	}
	if entry.IP == "" {
		t.Error("GetRDNS() entry.IP is empty")
	}
}

// TestGetRDNS_NoPTR records GET /v1/rdns/ipv4/{ip} returning null. In record
// mode it first deletes any PTR on NETCUP_TEST_IP so the read-back is empty.
// Prep operations (DeleteRDNS + ConfirmRDNS) use an unrecorded live client so
// the cassette contains only the intended GET interaction.
func TestGetRDNS_NoPTR(t *testing.T) {
	const cassetteName = "TestGetRDNS_NoPTR"
	client := NewClient(t, cassetteName)
	ip := RDNSIPForTest(t, cassetteName)

	if os.Getenv("VCR_RECORD") == "1" {
		live := liveRDNSClient(t)
		if err := live.DeleteRDNS(context.Background(), ip); err != nil {
			t.Fatalf("DeleteRDNS (record-mode prep) error = %v", err)
		}
		// rDNS deletions are applied asynchronously; confirm the PTR is
		// empty before recording, so a stale value is never captured. Use
		// the unrecorded live client so polling GETs don't leak into the
		// cassette.
		if _, err := live.ConfirmRDNS(context.Background(), ip, &netcup.RdnsEntry{Hostname: ""}); err != nil {
			t.Fatalf("ConfirmRDNS (record-mode prep) error = %v", err)
		}
	}

	entry, err := client.GetRDNS(context.Background(), ip)
	if err != nil {
		t.Fatalf("GetRDNS() error = %v", err)
	}
	if entry == nil {
		t.Fatal("GetRDNS() returned nil entry")
	}
	if entry.Hostname != "" {
		t.Errorf("Hostname = %q, want empty string (no PTR)", entry.Hostname)
	}
}

// TestDeleteRDNS records DELETE /v1/rdns/ipv4/{ip}. In record mode it first
// sets a PTR on NETCUP_TEST_IP so there is something to delete. Prep operations
// (SetRDNS + ConfirmRDNS) use an unrecorded live client so the cassette
// contains only the intended DELETE interaction.
func TestDeleteRDNS(t *testing.T) {
	const cassetteName = "TestDeleteRDNS"
	client := NewClient(t, cassetteName)
	ip := RDNSIPForTest(t, cassetteName)

	if os.Getenv("VCR_RECORD") == "1" {
		live := liveRDNSClient(t)
		_, err := live.SetRDNS(context.Background(), ip, testRDNSHostname)
		if err != nil {
			t.Fatalf("SetRDNS (record-mode prep) error = %v", err)
		}
		// rDNS updates are asynchronous; confirm the set is readable before
		// issuing the recorded delete, otherwise the DELETE can hit the API
		// before the PTR exists and save a no-op/404 cassette.
		if _, err := live.ConfirmRDNS(context.Background(), ip, &netcup.RdnsEntry{Hostname: testRDNSHostname}); err != nil {
			t.Fatalf("ConfirmRDNS (record-mode prep) error = %v", err)
		}
	}

	err := client.DeleteRDNS(context.Background(), ip)
	if err != nil {
		t.Fatalf("DeleteRDNS() error = %v", err)
	}
}
