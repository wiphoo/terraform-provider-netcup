package provider

import (
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func testAccProviderFactory() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"netcup": providerserver.NewProtocol6WithError(New("test")()),
	}
}

// testCheckResourceAttrGreaterThanZero asserts that a Terraform count
// attribute (e.g. "servers.#", "ipv4_addresses.#") is set to a value greater
// than zero. This is needed because TestCheckResourceAttrSet only verifies the
// attribute exists and is non-empty, so it would pass on "0" — the value
// Terraform stores for an empty list.
func testCheckResourceAttrGreaterThanZero(name, key string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return fmt.Errorf("resource %q not found in state", name)
		}
		v, ok := rs.Primary.Attributes[key]
		if !ok {
			return fmt.Errorf("%s.%s not found in state", name, key)
		}
		count, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("%s.%s = %q, expected an integer: %w", name, key, v, err)
		}
		if count <= 0 {
			return fmt.Errorf("%s.%s = %d, expected > 0", name, key, count)
		}
		return nil
	}
}

// testCheckResourceAttrAnyGreaterThanZero asserts that at least one of the
// given Terraform count attributes is greater than zero. It is used where a
// resource legitimately populates one of several lists — e.g. a server may be
// IPv6-only, so requiring ipv4_addresses.# > 0 would wrongly fail; passing both
// "ipv4_addresses.#" and "ipv6_addresses.#" requires only that the server has
// at least one address of either family.
func testCheckResourceAttrAnyGreaterThanZero(name string, keys ...string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return fmt.Errorf("resource %q not found in state", name)
		}
		var seen []string
		for _, key := range keys {
			v, ok := rs.Primary.Attributes[key]
			if !ok {
				continue
			}
			count, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("%s.%s = %q, expected an integer: %w", name, key, v, err)
			}
			seen = append(seen, fmt.Sprintf("%s=%d", key, count))
			if count > 0 {
				return nil
			}
		}
		return fmt.Errorf("%s: expected at least one of %v to be > 0, got %v", name, keys, seen)
	}
}

func TestAccServersDataSource(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("TF_ACC not set")
	}

	resource.ParallelTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProviderFactory(),
		Steps: []resource.TestStep{
			{
				Config: `data "netcup_servers" "all" {}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					testCheckResourceAttrGreaterThanZero("data.netcup_servers.all", "servers.#"),
				),
			},
		},
	})
}
