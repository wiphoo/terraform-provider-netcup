package provider

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

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

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProviderFactory(),
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
