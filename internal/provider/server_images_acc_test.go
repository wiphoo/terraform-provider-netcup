package provider

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccServerImagesDataSource(t *testing.T) {
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
				Config: fmt.Sprintf(`data "netcup_server_images" "test" {
					server_id = %q
				}`, serverID),
				Check: resource.ComposeAggregateTestCheckFunc(
					testCheckResourceAttrGreaterThanZero("data.netcup_server_images.test", "images.#"),
				),
			},
		},
	})
}
