package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

func cmdRDNS(args []string) error {
	if len(args) == 0 {
		usageRDNS(os.Stderr)
		return fmt.Errorf("rdns requires a subcommand")
	}

	switch args[0] {
	case "get":
		return rdnsGet(args[1:], os.Stdout)
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
  netcupctl rdns help                show this help
`)
}

func rdnsGet(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("rdns-get", flag.ContinueOnError)
	jsonFlag := fs.Bool("json", false, "output as JSON")

	// flag.Parse stops at the first non-flag argument, so parse iteratively to
	// accept flags before or after the positional IP.
	var positional []string
	pending := args
	for {
		if err := fs.Parse(pending); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		pending = fs.Args()
		if len(pending) == 0 {
			break
		}
		positional = append(positional, pending[0])
		pending = pending[1:]
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

	fmt.Fprintf(out, "IP:\t%s\n", entry.IP)
	fmt.Fprintf(out, "Hostname:\t%s\n", hostname)
	return nil
}
