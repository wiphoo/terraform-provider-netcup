package vcr

import (
	"context"
	"os"
	"testing"
)

const testRDNSIP = "203.0.113.10"

const testNoPTRIP = "203.0.113.20"

const testRDNSHostname = "host-a1b2c3d4.example.com"

func rdnsIPForTest(t *testing.T) string {
	t.Helper()
	if os.Getenv("VCR_RECORD") == "1" {
		ip := os.Getenv("NETCUP_TEST_IP")
		if ip == "" {
			t.Fatal("VCR_RECORD=1 requires NETCUP_TEST_IP")
		}
		return ip
	}
	return testRDNSIP
}

func TestSetRDNS(t *testing.T) {
	client := NewClient(t, "TestSetRDNS")
	ip := rdnsIPForTest(t)
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

func TestGetRDNS(t *testing.T) {
	client := NewClient(t, "TestGetRDNS")
	entry, err := client.GetRDNS(context.Background(), testRDNSIP)
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

func TestGetRDNS_NoPTR(t *testing.T) {
	client := NewClient(t, "TestGetRDNS_NoPTR")
	entry, err := client.GetRDNS(context.Background(), testNoPTRIP)
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

func TestDeleteRDNS(t *testing.T) {
	client := NewClient(t, "TestDeleteRDNS")
	err := client.DeleteRDNS(context.Background(), testRDNSIP)
	if err != nil {
		t.Fatalf("DeleteRDNS() error = %v", err)
	}
}
