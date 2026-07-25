package provider

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

func TestAccServerPowerResource(t *testing.T) {
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

	// The serverPowerResource Delete is intentionally a no-op, so if this
	// test stalls or fails midway through the OFF step the shared
	// acceptance-test server stays powered off. Register a cleanup that
	// unconditionally powers it back ON before exiting.
	id, err := parseServerID(serverID)
	if err != nil {
		t.Fatalf("invalid NETCUP_TEST_SERVER_ID %q: %v", serverID, err)
	}
	cleanupAPIEndpoint := os.Getenv("NETCUP_API_ENDPOINT")
	if cleanupAPIEndpoint == "" {
		cleanupAPIEndpoint = netcup.DefaultAPIEndpoint
	}
	cleanupOIDCEndpoint := os.Getenv("NETCUP_OIDC_ENDPOINT")
	if cleanupOIDCEndpoint == "" {
		cleanupOIDCEndpoint = netcup.DefaultOIDCEndpoint
	}
	cleanupOpts := []netcup.Option{
		netcup.WithAPIEndpoint(cleanupAPIEndpoint),
		netcup.WithOIDCEndpoint(cleanupOIDCEndpoint),
	}
	cleanupAccessToken := os.Getenv("NETCUP_ACCESS_TOKEN")
	cleanupRefreshToken := os.Getenv("NETCUP_REFRESH_TOKEN")
	var cleanupExpiry time.Time
	if p, err := netcup.ParseAccessTokenExpiry(cleanupAccessToken); err == nil {
		cleanupExpiry = p
	}
	cleanupTokenSource := netcup.NewTokenSource(netcup.New(cleanupOpts...), cleanupAccessToken, cleanupRefreshToken, cleanupExpiry)
	cleanupClient := netcup.New(append(cleanupOpts, netcup.WithTokenSource(cleanupTokenSource))...)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), defaultTaskTimeout)
		defer cancel()
		task, cErr := cleanupClient.SetPowerState(cleanupCtx, id, netcup.PowerOn, "")
		if cErr != nil {
			t.Errorf("cleanup SetPowerState(ON) failed: %v", cErr)
			return
		}
		if task != nil {
			if _, wErr := cleanupClient.WaitForTask(cleanupCtx, task.UUID); wErr != nil {
				t.Errorf("cleanup WaitForTask after power-on failed: %v", wErr)
			}
		}
	})

	resource.Test(t, resource.TestCase{
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
