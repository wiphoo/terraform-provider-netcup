package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

func cmdServer(args []string) error {
	if len(args) == 0 {
		usageServer(os.Stderr)
		return fmt.Errorf("server requires a subcommand")
	}

	switch args[0] {
	case "list":
		return serverList(args[1:], os.Stdout)
	case "help", "-h", "--help":
		usageServer(os.Stdout)
		return nil
	default:
		usageServer(os.Stderr)
		return fmt.Errorf("unknown server subcommand %q", args[0])
	}
}

func usageServer(w *os.File) {
	fmt.Fprint(w, `netcupctl server - manage servers

Usage:
  netcupctl server list [--json]
  netcupctl server help    show this help
`)
}

func serverList(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("server-list", flag.ContinueOnError)
	jsonFlag := fs.Bool("json", false, "output as JSON")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	client := clientWithToken()
	servers, err := client.ListServers(context.Background())
	if err != nil {
		return err
	}

	if *jsonFlag {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(servers)
	}

	if len(servers) == 0 {
		fmt.Fprintln(out, "No servers found.")
		return nil
	}

	tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tHOSTNAME\tPRODUCT\tSTATUS")
	for _, s := range servers {
		hostname := ""
		if s.Hostname != nil {
			hostname = *s.Hostname
		}
		product := ""
		if s.Template != nil {
			product = s.Template.Name
		}
		status := "Active"
		if s.Disabled {
			status = "Disabled"
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", s.ID, s.Name, hostname, product, status)
	}
	return tw.Flush()
}

func clientWithToken() *netcup.Client {
	if os.Getenv("NETCUP_ACCESS_TOKEN") == "" {
		token, err := loadTokens()
		if err == nil && token != nil && token.AccessToken != "" {
			return netcup.New(netcup.WithAccessToken(token.AccessToken))
		}
	}
	return netcup.New()
}
