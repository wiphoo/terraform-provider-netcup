package provider

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
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
// netcup_server_images data source, and wait=true blocks the apply until the
// reinstall task reaches a terminal state so a reinstall that ends in ERROR
// fails this release-gate test instead of leaving `make acc` green. The
// post-apply task check independently requires FINISHED, so an indeterminate
// wait that persisted state with a warning cannot pass this test (a wait=false
// apply would return on the 202 before the task could fail, and the resource's
// Delete is a no-op, so nothing downstream would catch it).
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
	taskClient := testAccNetcupClient()

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
					wait             = true
				}`, serverID, serverID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("netcup_server_reinstall.test", "server_id", serverID),
					resource.TestCheckResourceAttrSet("netcup_server_reinstall.test", "image_flavour_id"),
					resource.TestCheckResourceAttrSet("netcup_server_reinstall.test", "id"),
					resource.TestCheckResourceAttrSet("netcup_server_reinstall.test", "task_id"),
					testAccCheckReinstallTaskFinished(taskClient),
				),
			},
		},
	})
}

// testAccCheckReinstallTaskFinished independently verifies the remote task
// after Terraform apply. The resource preserves state and emits a warning for
// an indeterminate wait because retrying could issue a second destructive
// reinstall; this check makes that outcome fail this release-gate test instead
// of allowing a later ERROR or still-running task to go unnoticed.
func testAccCheckReinstallTaskFinished(client *netcup.Client) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources["netcup_server_reinstall.test"]
		if !ok {
			return fmt.Errorf("resource %q not found in state", "netcup_server_reinstall.test")
		}
		taskID := rs.Primary.Attributes["task_id"]
		if taskID == "" {
			return fmt.Errorf("netcup_server_reinstall.test.task_id is empty")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		task, err := client.GetTask(ctx, taskID)
		if err != nil {
			return fmt.Errorf("get reinstall task %s: %w", taskID, err)
		}
		if task.State != netcup.TaskStateFinished {
			return fmt.Errorf("reinstall task %s is %s, want FINISHED", taskID, task.State)
		}
		return nil
	}
}

// testAccNetcupClient mirrors the provider's environment-based client setup so
// the post-apply task check supports the same access-token refresh behavior as
// the Terraform provider itself.
func testAccNetcupClient() *netcup.Client {
	apiEndpoint := os.Getenv("NETCUP_API_ENDPOINT")
	if apiEndpoint == "" {
		apiEndpoint = netcup.DefaultAPIEndpoint
	}
	oidcEndpoint := os.Getenv("NETCUP_OIDC_ENDPOINT")
	if oidcEndpoint == "" {
		oidcEndpoint = netcup.DefaultOIDCEndpoint
	}
	accessToken := os.Getenv("NETCUP_ACCESS_TOKEN")
	refreshToken := os.Getenv("NETCUP_REFRESH_TOKEN")
	opts := []netcup.Option{
		netcup.WithAPIEndpoint(apiEndpoint),
		netcup.WithOIDCEndpoint(oidcEndpoint),
	}
	var expiry time.Time
	if parsed, err := netcup.ParseAccessTokenExpiry(accessToken); err == nil {
		expiry = parsed
	}
	tokenSource := netcup.NewTokenSource(netcup.New(opts...), accessToken, refreshToken, expiry)
	return netcup.New(append(opts, netcup.WithTokenSource(tokenSource))...)
}
