package provider

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccSSHKeyResource(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("set TF_ACC to run acceptance tests")
	}
	pub := os.Getenv("NETCUP_ACC_SSH_PUBLIC_KEY")
	if pub == "" {
		t.Skip("set NETCUP_ACC_SSH_PUBLIC_KEY to run this test")
	}
	config := fmt.Sprintf(`
resource "netcup_ssh_key" "test" {
  name       = "tf-acc-netcup-ssh-key"
  public_key = %q
}
`, pub)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProviderFactory(),
		Steps: []resource.TestStep{{
			Config: config,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("netcup_ssh_key.test", "id"),
				resource.TestCheckResourceAttr("netcup_ssh_key.test", "name", "tf-acc-netcup-ssh-key"),
			),
		}},
	})
}
