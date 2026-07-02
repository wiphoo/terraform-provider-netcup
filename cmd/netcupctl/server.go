package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
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
	case "get":
		return serverGet(args[1:], os.Stdout)
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
  netcupctl server get <id> [--json]
  netcupctl server help          show this help
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

	client, err := clientWithToken()
	if err != nil {
		return err
	}
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
		status := "Enabled"
		if s.Disabled {
			status = "Disabled"
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", s.ID, s.Name, hostname, product, status)
	}
	return tw.Flush()
}

func serverGet(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("server-get", flag.ContinueOnError)
	jsonFlag := fs.Bool("json", false, "output as JSON")
	positional, err := parsePositionalArgs(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if len(positional) == 0 {
		usageServer(os.Stderr)
		return fmt.Errorf("server get requires a server ID")
	}
	if len(positional) > 1 {
		return fmt.Errorf("server get takes a single server ID, got %d arguments", len(positional))
	}
	id, err := strconv.ParseInt(positional[0], 10, 32)
	if err != nil {
		return fmt.Errorf("invalid server ID %q: must be an integer", positional[0])
	}

	client, err := clientWithToken()
	if err != nil {
		return err
	}
	server, err := client.GetServer(context.Background(), int32(id))
	if err != nil {
		return err
	}

	if *jsonFlag {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(server)
	}

	hostname := ""
	if server.Hostname != nil {
		hostname = *server.Hostname
	}
	product := ""
	if server.Template != nil {
		product = server.Template.Name
	}
	status := "unknown"
	if server.ServerLiveInfo != nil && server.ServerLiveInfo.State != "" {
		status = server.ServerLiveInfo.State
	}
	admin := "Enabled"
	if server.Disabled {
		admin = "Disabled"
	}

	tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintf(tw, "ID:\t%d\n", server.ID)
	fmt.Fprintf(tw, "Name:\t%s\n", server.Name)
	fmt.Fprintf(tw, "Hostname:\t%s\n", hostname)
	fmt.Fprintf(tw, "Product:\t%s\n", product)
	fmt.Fprintf(tw, "Status:\t%s\n", status)
	fmt.Fprintf(tw, "Admin:\t%s\n", admin)
	fmt.Fprintf(tw, "IPv4:\t%s\n", formatIPv4(server.IPv4Addresses))
	fmt.Fprintf(tw, "IPv6:\t%s\n", formatIPv6(server.IPv6Addresses))
	site := "-"
	if server.Site != nil && server.Site.City != "" {
		site = server.Site.City
	}
	fmt.Fprintf(tw, "Site:\t%s\n", site)
	return tw.Flush()
}

// formatIPv4 joins the IPv4 addresses for display, or "-" when there are none.
func formatIPv4(addrs []netcup.IPv4AddressMinimal) string {
	if len(addrs) == 0 {
		return "-"
	}
	ips := make([]string, len(addrs))
	for i, a := range addrs {
		ips[i] = a.IP
	}
	return strings.Join(ips, ", ")
}

// formatIPv6 joins the IPv6 prefixes as prefix/length, or "-" when there are none.
func formatIPv6(addrs []netcup.IPv6AddressMinimal) string {
	if len(addrs) == 0 {
		return "-"
	}
	ips := make([]string, len(addrs))
	for i, a := range addrs {
		ips[i] = fmt.Sprintf("%s/%d", a.NetworkPrefix, a.NetworkPrefixLength)
	}
	return strings.Join(ips, ", ")
}

// clientWithToken builds a Client, preferring the NETCUP_ACCESS_TOKEN
// environment variable (consumed by netcup.New) and otherwise falling back to
// the access token persisted by `netcupctl auth login`. A failure to read the
// stored token file is surfaced rather than silently downgrading to an
// unauthenticated client.
func clientWithToken() (*netcup.Client, error) {
	if os.Getenv("NETCUP_ACCESS_TOKEN") != "" {
		return netcup.New(), nil
	}
	token, err := loadTokens()
	if err != nil {
		return nil, fmt.Errorf("loading tokens: %w", err)
	}
	if token != nil && token.AccessToken != "" {
		return netcup.New(netcup.WithAccessToken(token.AccessToken)), nil
	}
	return netcup.New(), nil
}
