// Command netcupctl is a command-line client for the netcup Server Control
// Panel (SCP) REST API. It is the first consumer of the shared pkg/netcup SDK.
package main

import (
	"fmt"
	"os"

	"github.com/wiphoo/terraform-provider-netcup/internal/version"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "netcupctl: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage(os.Stderr)
		return nil
	}

	switch args[0] {
	case "ping":
		return cmdPing(args[1:])
	case "auth":
		return cmdAuth(args[1:])
	case "version":
		fmt.Println(version.String())
		return nil
	case "help", "-h", "--help":
		usage(os.Stdout)
		return nil
	default:
		usage(os.Stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage(w *os.File) {
	fmt.Fprint(w, `netcupctl - CLI for the netcup SCP REST API

Usage:
  netcupctl <command> [flags]

Commands:
  ping       Check that the SCP REST API is reachable
  auth       OAuth 2.0 device-authorization login and token refresh
  version    Print the netcupctl version
  help       Show this help
`)
}
