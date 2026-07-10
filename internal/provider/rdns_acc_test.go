package provider

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
	vcr "github.com/wiphoo/terraform-provider-netcup/tests/vcr"
)

func testAccCheckRDNSDestroy(s *terraform.State) error {
	client := vcr.NewLiveClient(os.Getenv("NETCUP_ACCESS_TOKEN"))
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "netcup_rdns" {
			continue
		}
		ip := rs.Primary.Attributes["ip_address"]
		if ip == "" {
			continue
		}
		// CapturePTR reads the current PTR, treating a 404 (already gone) as an
		// empty result and surfacing any other error (5xx/auth/network) so a
		// failed read can't let CheckDestroy pass without verifying removal.
		hostname, err := vcr.CapturePTR(client, ip)
		if err != nil {
			return fmt.Errorf("checking rDNS destroy for %s: %w", ip, err)
		}
		// rDNS deletions are asynchronous, so an immediate read can still see
		// the old hostname; ConfirmRDNS polls for the empty value.
		if hostname != "" {
			if _, err := client.ConfirmRDNS(context.Background(), ip, &netcup.RdnsEntry{Hostname: ""}); err != nil {
				return fmt.Errorf("rDNS entry for %s was not cleared after destroy: %w", ip, err)
			}
		}
	}
	return nil
}

func TestAccRDNSResource(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("TF_ACC not set")
	}

	testIP := os.Getenv("NETCUP_TEST_IP")
	if testIP == "" {
		t.Skip("NETCUP_TEST_IP not set")
	}

	testHostname := "test-acc-rdns.example.com"
	updatedHostname := "test-acc-rdns-updated.example.com"

	// The test overwrites testIP's PTR and the framework's destroy step (via
	// CheckDestroy) clears it, so if the caller points NETCUP_TEST_IP at an IP
	// that already has a PTR, that value would be lost. Capture it up front and
	// restore it via t.Cleanup, which runs after resource.Test finishes
	// (including CheckDestroy), so the restore never races the empty-hostname
	// assertion. If the IP had no PTR, there is nothing to restore.
	restoreClient := vcr.NewLiveClient(os.Getenv("NETCUP_ACCESS_TOKEN"))
	originalHostname, err := vcr.CapturePTR(restoreClient, testIP)
	if err != nil {
		t.Fatalf("failed to capture original PTR for %s: %v", testIP, err)
	}
	if originalHostname != "" {
		t.Cleanup(func() {
			// EnsurePTR restores the value and confirms the async read-back, so
			// the test fails rather than passing with the caller's PTR cleared.
			if err := vcr.EnsurePTR(restoreClient, testIP, originalHostname); err != nil {
				t.Errorf("failed to restore original PTR %q for %s: %v", originalHostname, testIP, err)
			}
		})
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProviderFactory(),
		CheckDestroy:             testAccCheckRDNSDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`resource "netcup_rdns" "test" {
					ip_address = %q
					hostname   = %q
				}`, testIP, testHostname),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("netcup_rdns.test", "ip_address", testIP),
					resource.TestCheckResourceAttr("netcup_rdns.test", "hostname", testHostname),
					resource.TestCheckResourceAttrSet("netcup_rdns.test", "id"),
				),
			},
			{
				ResourceName:      "netcup_rdns.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: fmt.Sprintf(`resource "netcup_rdns" "test" {
					ip_address = %q
					hostname   = %q
				}`, testIP, updatedHostname),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("netcup_rdns.test", "ip_address", testIP),
					resource.TestCheckResourceAttr("netcup_rdns.test", "hostname", updatedHostname),
				),
			},
		},
	})
}
