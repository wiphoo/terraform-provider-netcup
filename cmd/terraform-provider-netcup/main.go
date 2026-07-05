// Command terraform-provider-netcup is the Terraform provider entrypoint. It
// is invoked by Terraform itself (via the plugin protocol), not run directly
// by users.
package main

import (
	"context"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/wiphoo/terraform-provider-netcup/internal/provider"
	"github.com/wiphoo/terraform-provider-netcup/internal/version"
)

// providerAddress is the provider's Terraform Registry source address (see
// README.md's required_providers block: source = "wiphoo/netcup").
const providerAddress = "registry.terraform.io/wiphoo/netcup"

func main() {
	err := providerserver.Serve(context.Background(), provider.New(version.String()), providerserver.ServeOpts{
		Address: providerAddress,
	})
	if err != nil {
		log.Fatal(err)
	}
}
