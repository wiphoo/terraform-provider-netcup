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
)

func cmdRDNS(args []string) error {
	if len(args) == 0 {
		usageRDNS(os.Stderr)
		return fmt.Errorf("rdns requires a subcommand")
	}

	switch args[0] {
	case "get":
		return rdnsGet(args[1:], os.Stdout)
	case "set":
		return rdnsSet(args[1:], os.Stdout)
	case "help", "-h", "--help":
		usageRDNS(os.Stdout)
		return nil
	default:
		usageRDNS(os.Stderr)
		return fmt.Errorf("unknown rdns subcommand %q", args[0])
	}
}

func usageRDNS(w *os.File) {
	fmt.Fprint(w, `netcupctl rdns - manage reverse DNS entries

Usage:
  netcupctl rdns get <ip> [--json]
  netcupctl rdns set <ip> <hostname> [--json]
  netcupctl rdns help                show this help
`)
}

func rdnsGet(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("rdns-get", flag.ContinueOnError)
	jsonFlag := fs.Bool("json", false, "output as JSON")
	positional, err := parsePositionalArgs(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if len(positional) == 0 {
		usageRDNS(os.Stderr)
		return fmt.Errorf("rdns get requires an IP address")
	}
	if len(positional) > 1 {
		return fmt.Errorf("rdns get takes a single IP address, got %d arguments", len(positional))
	}

	client, err := clientWithToken()
	if err != nil {
		return err
	}

	entry, err := client.GetRDNS(context.Background(), positional[0])
	if err != nil {
		return err
	}

	if *jsonFlag {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(entry)
	}

	hostname := entry.Hostname
	if hostname == "" {
		hostname = "<none>"
	}

	tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintf(tw, "IP:\t%s\n", entry.IP)
	fmt.Fprintf(tw, "Hostname:\t%s\n", hostname)
	return tw.Flush()
}

func rdnsSet(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("rdns-set", flag.ContinueOnError)
	jsonFlag := fs.Bool("json", false, "output as JSON")
	positional, err := parsePositionalArgs(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if len(positional) < 2 {
		usageRDNS(os.Stderr)
		return fmt.Errorf("rdns set requires an IP address and a hostname")
	}
	if len(positional) > 2 {
		return fmt.Errorf("rdns set takes an IP address and a hostname, got %d arguments", len(positional))
	}

	client, err := clientWithToken()
	if err != nil {
		return err
	}

	ip, hostname := positional[0], positional[1]
	set, err := client.SetRDNS(context.Background(), ip, hostname)
	if err != nil {
		return err
	}

	// Read-back confirmation: fetch the entry and verify the hostname matches.
	entry, err := client.GetRDNS(context.Background(), ip)
	if err != nil {
		return fmt.Errorf("rdns set succeeded but read-back failed: %w", err)
	}
	if entry.Hostname != set.Hostname {
		return fmt.Errorf("rdns set succeeded but read-back mismatch: set %q, got %q", set.Hostname, entry.Hostname)
	}

	if *jsonFlag {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(entry)
	}

	tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintf(tw, "IP:\t%s\n", entry.IP)
	fmt.Fprintf(tw, "Hostname:\t%s\n", entry.Hostname)
	return tw.Flush()
}
