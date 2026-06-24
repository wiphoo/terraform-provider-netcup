package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// cmdPing implements `netcupctl ping`.
func cmdPing(args []string) error {
	fs := flag.NewFlagSet("ping", flag.ContinueOnError)
	endpoint := fs.String("endpoint", "", "override the REST API endpoint (defaults to NETCUP_API_ENDPOINT or the public SCP API)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var opts []netcup.Option
	if *endpoint != "" {
		opts = append(opts, netcup.WithAPIEndpoint(*endpoint))
	}

	client := netcup.New(opts...)
	if err := client.Ping(context.Background()); err != nil {
		return err
	}

	fmt.Println("ok: SCP REST API reachable")
	return nil
}
