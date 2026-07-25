package provider

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccRescueResource(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("TF_ACC not set")
	}

	serverID := os.Getenv("NETCUP_TEST_SERVER_ID")
	if serverID == "" {
		t.Skip("NETCUP_TEST_SERVER_ID not set")
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProviderFactory(),
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`resource "netcup_server_rescue" "test" {
					server_id = %q
				}`, serverID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("netcup_server_rescue.test", "server_id", serverID),
					resource.TestCheckResourceAttr("netcup_server_rescue.test", "active", "true"),
					resource.TestCheckResourceAttrSet("netcup_server_rescue.test", "password"),
					resource.TestCheckResourceAttrSet("netcup_server_rescue.test", "id"),
				),
			},
			{
				ResourceName:            "netcup_server_rescue.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"wait", "password", "pending_task_id"},
			},
		},
	})
}
