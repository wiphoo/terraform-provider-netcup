package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
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
	case "delete":
		return rdnsDelete(args[1:], os.Stdout)
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
  netcupctl rdns delete <ip> [--json]
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

	// Confirm the change actually landed before reporting success. netcup
	// provisions PTRs asynchronously, so an immediate read may still return the
	// previous value (or null); ConfirmRDNS retries across a short window to
	// absorb that. If confirmation never succeeds the requested PTR is not
	// verifiably in effect, so return an error rather than a misleading exit
	// code 0.
	entry, err := client.ConfirmRDNS(context.Background(), ip, set)
	if err != nil {
		return err
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

func rdnsDelete(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("rdns-delete", flag.ContinueOnError)
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
		return fmt.Errorf("rdns delete requires an IP address")
	}
	if len(positional) > 1 {
		return fmt.Errorf("rdns delete takes a single IP address, got %d arguments", len(positional))
	}

	client, err := clientWithToken()
	if err != nil {
		return err
	}

	if err := client.DeleteRDNS(context.Background(), positional[0]); err != nil {
		return err
	}

	// Canonicalize the IP for display so the output always shows a consistent
	// form (RFC 5952 for IPv6), matching what rdnsGet and rdnsSet display.
	canon, err := canonicalDisplayIP(positional[0])
	if err != nil {
		return err
	}

	if *jsonFlag {
		return json.NewEncoder(out).Encode(map[string]interface{}{
			"ip":      canon,
			"deleted": true,
		})
	}

	fmt.Fprintf(out, "Deleted reverse DNS for %s\n", canon)
	return nil
}

// canonicalDisplayIP parses ip and returns its canonical string form (RFC 5952
// for IPv6; IPv4-in-IPv6 addresses are unmapped to dotted-quad). This is the
// same logic as netcup.canonicalizeIP but replicated in the CLI package to
// avoid depending on an unexported SDK function.
func canonicalDisplayIP(ip string) (string, error) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return "", fmt.Errorf("invalid IP address %q: %w", ip, err)
	}
	addr = addr.Unmap()
	return addr.String(), nil
}
