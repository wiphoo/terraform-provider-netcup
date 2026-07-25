package provider

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccServerPowerResource(t *testing.T) {
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
				Config: fmt.Sprintf(`resource "netcup_server_power" "test" {
					server_id = %q
					state     = "ON"
				}`, serverID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("netcup_server_power.test", "server_id", serverID),
					resource.TestCheckResourceAttr("netcup_server_power.test", "state", "ON"),
					resource.TestCheckResourceAttrSet("netcup_server_power.test", "id"),
				),
			},
			{
				ResourceName:            "netcup_server_power.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"wait", "state_option", "pending_task_id"},
			},
		},
	})
}

func TestAccServerPowerResource_OffAndOn(t *testing.T) {
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
				Config: fmt.Sprintf(`resource "netcup_server_power" "test" {
					server_id = %q
					state     = "OFF"
				}`, serverID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("netcup_server_power.test", "server_id", serverID),
					resource.TestCheckResourceAttr("netcup_server_power.test", "state", "OFF"),
				),
			},
			{
				Config: fmt.Sprintf(`resource "netcup_server_power" "test" {
					server_id = %q
					state     = "ON"
				}`, serverID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("netcup_server_power.test", "state", "ON"),
				),
			},
		},
	})
}
