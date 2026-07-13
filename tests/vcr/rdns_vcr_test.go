package vcr

import (
	"context"
	"os"
	"testing"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// TestSetRDNS records POST /v1/rdns/ipv4. In record mode it calls SetRDNS
// with NETCUP_TEST_IP so the cassette captures a real SCP response.
func TestSetRDNS(t *testing.T) {
	const cassetteName = "TestSetRDNS"
	client := NewClient(t, cassetteName)
	ip := RDNSIPForTest(t, cassetteName)
	entry, err := client.SetRDNS(context.Background(), ip, TestRDNSHostname)
	if err != nil {
		t.Fatalf("SetRDNS() error = %v", err)
	}
	if entry == nil {
		t.Fatal("SetRDNS() returned nil entry")
	}
	if entry.IP != ip {
		t.Errorf("entry.IP = %q, want canonical %q", entry.IP, ip)
	}
	if !netcup.EqualRDNSHostnames(entry.Hostname, TestRDNSHostname) {
		t.Errorf("entry.Hostname = %q, want %q", entry.Hostname, TestRDNSHostname)
	}
}

// TestGetRDNS_WithPTR records GET /v1/rdns/ipv4/{ip} with a PTR set. In record
// mode it first seeds the PTR on NETCUP_TEST_IP (via an unrecorded live client,
// so ConfirmRDNS polling GETs don't leak into the cassette).
func TestGetRDNS_WithPTR(t *testing.T) {
	const cassetteName = "TestGetRDNS_WithPTR"
	client := NewClient(t, cassetteName)
	ip := RDNSIPForTest(t, cassetteName)

	if os.Getenv("VCR_RECORD") == "1" {
		SeedLivePTR(t, ip)
	}

	entry, err := client.GetRDNS(context.Background(), ip)
	if err != nil {
		t.Fatalf("GetRDNS() error = %v", err)
	}
	if entry == nil {
		t.Fatal("GetRDNS() returned nil entry")
	}
	if !netcup.EqualRDNSHostnames(entry.Hostname, TestRDNSHostname) {
		t.Errorf("Hostname = %q, want %q", entry.Hostname, TestRDNSHostname)
	}
}

// TestGetRDNS_NoPTR records GET /v1/rdns/ipv4/{ip} returning null. In record
// mode it first clears any PTR on NETCUP_TEST_IP (via an unrecorded live
// client) so the read-back is empty.
func TestGetRDNS_NoPTR(t *testing.T) {
	const cassetteName = "TestGetRDNS_NoPTR"
	client := NewClient(t, cassetteName)
	ip := RDNSIPForTest(t, cassetteName)

	if os.Getenv("VCR_RECORD") == "1" {
		ClearLivePTR(t, ip)
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
// seeds a PTR on NETCUP_TEST_IP (via an unrecorded live client) so there is
// something to delete.
func TestDeleteRDNS(t *testing.T) {
	const cassetteName = "TestDeleteRDNS"
	client := NewClient(t, cassetteName)
	ip := RDNSIPForTest(t, cassetteName)

	if os.Getenv("VCR_RECORD") == "1" {
		SeedLivePTR(t, ip)
	}

	err := client.DeleteRDNS(context.Background(), ip)
	if err != nil {
		t.Fatalf("DeleteRDNS() error = %v", err)
	}
}
