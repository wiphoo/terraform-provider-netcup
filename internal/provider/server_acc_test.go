package provider

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccServerDataSource(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("TF_ACC not set")
	}

	serverID := os.Getenv("NETCUP_TEST_SERVER_ID")
	if serverID == "" {
		t.Skip("NETCUP_TEST_SERVER_ID not set")
	}

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProviderFactory(),
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`data "netcup_server" "test" {
					id = %q
				}`, serverID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.netcup_server.test", "id", serverID),
					resource.TestCheckResourceAttrSet("data.netcup_server.test", "hostname"),
					resource.TestCheckResourceAttrSet("data.netcup_server.test", "status"),
					resource.TestCheckResourceAttrSet("data.netcup_server.test", "product_name"),
					resource.TestCheckResourceAttrSet("data.netcup_server.test", "ipv4_addresses.#"),
				),
			},
		},
	})
}
