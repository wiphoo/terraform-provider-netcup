package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

func testAccCheckRDNSDestroy(s *terraform.State) error {
	client := netcup.New(
		netcup.WithAPIEndpoint(netcup.DefaultAPIEndpoint),
		netcup.WithAccessToken(os.Getenv("NETCUP_ACCESS_TOKEN")),
	)
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "netcup_rdns" {
			continue
		}
		ip := rs.Primary.Attributes["ip_address"]
		if ip == "" {
			continue
		}
		entry, err := client.GetRDNS(context.Background(), ip)
		if err != nil {
			// A 404 means the PTR is gone — the expected post-destroy state.
			// Any other error (5xx, auth, network) must not be swallowed, or a
			// failed read would let CheckDestroy pass without verifying removal.
			var apiErr *netcup.APIError
			if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
				continue
			}
			return fmt.Errorf("checking rDNS destroy for %s: %w", ip, err)
		}
		if entry.Hostname != "" {
			return fmt.Errorf("rDNS entry still exists for %s: hostname=%s", ip, entry.Hostname)
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
	restoreClient := netcup.New(
		netcup.WithAPIEndpoint(netcup.DefaultAPIEndpoint),
		netcup.WithAccessToken(os.Getenv("NETCUP_ACCESS_TOKEN")),
	)
	originalEntry, err := restoreClient.GetRDNS(context.Background(), testIP)
	if err != nil {
		var apiErr *netcup.APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
			t.Fatalf("failed to capture original PTR for %s: %v", testIP, err)
		}
	}
	if originalEntry != nil && originalEntry.Hostname != "" {
		originalHostname := originalEntry.Hostname
		t.Cleanup(func() {
			if _, err := restoreClient.SetRDNS(context.Background(), testIP, originalHostname); err != nil {
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
