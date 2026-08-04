package provider

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestAccServerReinstallResource exercises netcup_server_reinstall against the
// live SCP API.
//
// ⚠️ DESTRUCTIVE: applying this resource WIPES the test server (native OS
// reinstall). It therefore requires BOTH TF_ACC=1 and an explicit opt-in
// (NETCUP_TEST_REINSTALL_ALLOWED=1) before it will run — so an accidental
// `make acc` can never destroy a server without deliberate intent. When TF_ACC
// is set but the opt-in is missing, the test FAILS (not skips) to make the
// missing guard loud. The image flavour is discovered at runtime via the
// netcup_server_images data source (no hardcoded flavour), and wait=false keeps
// the live apply from blocking on a multi-minute reinstall task.
func TestAccServerReinstallResource(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("TF_ACC not set")
	}
	if os.Getenv("NETCUP_TEST_REINSTALL_ALLOWED") != "1" {
		t.Fatal("NETCUP_TEST_REINSTALL_ALLOWED != 1: refusing to run a destructive reinstall acc test. " +
			"This test WIPES the server on apply; set NETCUP_TEST_REINSTALL_ALLOWED=1 to opt in deliberately.")
	}

	serverID := os.Getenv("NETCUP_TEST_SERVER_ID")
	if serverID == "" {
		t.Skip("NETCUP_TEST_SERVER_ID not set")
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProviderFactory(),
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`data "netcup_server_images" "test" {
					server_id = %q
				}

				resource "netcup_server_reinstall" "test" {
					server_id        = %q
					image_flavour_id = element(data.netcup_server_images.test.images[*].id, 0)
					wait             = false
				}`, serverID, serverID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("netcup_server_reinstall.test", "server_id", serverID),
					resource.TestCheckResourceAttrSet("netcup_server_reinstall.test", "image_flavour_id"),
					resource.TestCheckResourceAttrSet("netcup_server_reinstall.test", "id"),
					resource.TestCheckResourceAttrSet("netcup_server_reinstall.test", "task_id"),
				),
			},
		},
	})
}
