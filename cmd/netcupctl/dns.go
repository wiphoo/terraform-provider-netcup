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

func cmdDNS(args []string) error {
	if len(args) == 0 {
		usageDNS(os.Stderr)
		return errors.New("dns requires a subcommand")
	}
	switch args[0] {
	case "zone":
		return cmdDNSZone(args[1:], os.Stdout)
	case "records":
		return cmdDNSRecords(args[1:], os.Stdout, os.Stderr, os.Stdin)
	case "help", "-h", "--help":
		usageDNS(os.Stdout)
		return nil
	default:
		usageDNS(os.Stderr)
		return fmt.Errorf("unknown dns subcommand %q", args[0])
	}
}

func usageDNS(w io.Writer) {
	fmt.Fprint(w, `netcupctl dns - manage DNS zones and records (CCP DNS API)

Usage:
  netcupctl dns zone get    <domain> [--json]
  netcupctl dns zone update <domain> [--ttl N] [--refresh N] [--retry N] [--expire N] [--json]
  netcupctl dns records list   <domain> [--json]
  netcupctl dns records add    <domain> <hostname> <type> <destination> [--priority N] [--json]
  netcupctl dns records update <domain> <id> [--hostname H] [--type T] [--destination D] [--priority N] [--json]
  netcupctl dns records delete <domain> <id> [--force|--yes] [--json]
  netcupctl dns help

DNS uses the CCP API, not the SCP device login. Set:
  NETCUP_CUSTOMER_NUMBER      your customer number
  NETCUP_DNS_API_KEY          API key from the CCP (Master data > API)
  NETCUP_DNS_API_PASSWORD     API password from the CCP

Use @ for the zone apex and * for a wildcard. Record types: A AAAA MX CNAME CAA
SRV TXT TLSA NS DS. Deleting a record is permanent and prompts for confirmation
unless --force (or --yes) is given.
`)
}

// withDNS builds a DNSClient from the environment, runs fn, and always logs out.
func withDNS(fn func(context.Context, *netcup.DNSClient) error) error {
	creds, err := netcup.DNSCredentialsFromEnv()
	if err != nil {
		return err
	}
	c, err := netcup.NewDNSClient(creds)
	if err != nil {
		return err
	}
	ctx := context.Background()
	defer func() { _ = c.Logout(ctx) }()
	return fn(ctx, c)
}

// parseDNSArgs parses args into fs and requires exactly want positionals.
// done is true when -h/--help was requested (the caller returns nil).
func parseDNSArgs(fs *flag.FlagSet, args []string, want int, problem string) (positional []string, done bool, err error) {
	positional, err = parsePositionalArgs(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, true, nil
		}
		return nil, false, err
	}
	if len(positional) != want {
		usageDNS(os.Stderr)
		return nil, false, errors.New(problem)
	}
	return positional, false, nil
}

func writeDNSJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printDNSZone(out io.Writer, z *netcup.DNSZone, asJSON bool) error {
	if asJSON {
		return writeDNSJSON(out, z)
	}
	tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintf(tw, "Domain:\t%s\n", z.Name)
	fmt.Fprintf(tw, "TTL:\t%d\n", z.TTL)
	fmt.Fprintf(tw, "Serial:\t%d\n", z.Serial)
	fmt.Fprintf(tw, "Refresh:\t%d\n", z.Refresh)
	fmt.Fprintf(tw, "Retry:\t%d\n", z.Retry)
	fmt.Fprintf(tw, "Expire:\t%d\n", z.Expire)
	fmt.Fprintf(tw, "DNSSEC:\t%t\n", z.DNSSEC)
	return tw.Flush()
}

func printDNSRecords(out io.Writer, recs []netcup.DNSRecord) error {
	tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "ID\tHOSTNAME\tTYPE\tPRIORITY\tDESTINATION")
	for _, r := range recs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n", r.ID, r.Hostname, r.Type, r.Priority, r.Destination)
	}
	return tw.Flush()
}

func cmdDNSZone(args []string, out io.Writer) error {
	if len(args) == 0 {
		usageDNS(os.Stderr)
		return errors.New("dns zone requires a subcommand")
	}
	switch args[0] {
	case "get":
		return dnsZoneGet(args[1:], out)
	case "update":
		return dnsZoneUpdate(args[1:], out)
	case "help", "-h", "--help":
		usageDNS(out)
		return nil
	default:
		usageDNS(os.Stderr)
		return fmt.Errorf("unknown dns zone subcommand %q", args[0])
	}
}

func dnsZoneGet(args []string, out io.Writer) error {
	if helpRequested(args, out, usageDNS) {
		return nil
	}
	fs := flag.NewFlagSet("dns-zone-get", flag.ContinueOnError)
	jsonFlag := fs.Bool("json", false, "output as JSON")
	pos, done, err := parseDNSArgs(fs, args, 1, "dns zone get takes a single <domain>")
	if err != nil || done {
		return err
	}
	return withDNS(func(ctx context.Context, c *netcup.DNSClient) error {
		z, err := c.GetZone(ctx, pos[0])
		if err != nil {
			return err
		}
		return printDNSZone(out, z, *jsonFlag)
	})
}

func dnsZoneUpdate(args []string, out io.Writer) error {
	if helpRequested(args, out, usageDNS) {
		return nil
	}
	fs := flag.NewFlagSet("dns-zone-update", flag.ContinueOnError)
	jsonFlag := fs.Bool("json", false, "output as JSON")
	ttl := fs.Int("ttl", 0, "zone TTL in seconds")
	refresh := fs.Int("refresh", 0, "SOA refresh in seconds")
	retry := fs.Int("retry", 0, "SOA retry in seconds")
	expire := fs.Int("expire", 0, "SOA expire in seconds")
	pos, done, err := parseDNSArgs(fs, args, 1, "dns zone update takes a single <domain>")
	if err != nil || done {
		return err
	}
	if *ttl == 0 && *refresh == 0 && *retry == 0 && *expire == 0 {
		return errors.New("dns zone update needs at least one of --ttl, --refresh, --retry, --expire")
	}
	return withDNS(func(ctx context.Context, c *netcup.DNSClient) error {
		z, err := c.GetZone(ctx, pos[0])
		if err != nil {
			return err
		}
		if *ttl != 0 {
			z.TTL = *ttl
		}
		if *refresh != 0 {
			z.Refresh = *refresh
		}
		if *retry != 0 {
			z.Retry = *retry
		}
		if *expire != 0 {
			z.Expire = *expire
		}
		updated, err := c.UpdateZone(ctx, *z)
		if err != nil {
			return err
		}
		return printDNSZone(out, updated, *jsonFlag)
	})
}

func cmdDNSRecords(args []string, out, errW io.Writer, in io.Reader) error {
	if len(args) == 0 {
		usageDNS(os.Stderr)
		return errors.New("dns records requires a subcommand")
	}
	switch args[0] {
	case "list":
		return dnsRecordsList(args[1:], out)
	case "add":
		return dnsRecordsAdd(args[1:], out)
	case "update":
		return dnsRecordsUpdate(args[1:], out)
	case "delete":
		return dnsRecordsDelete(args[1:], out, errW, in)
	case "help", "-h", "--help":
		usageDNS(out)
		return nil
	default:
		usageDNS(os.Stderr)
		return fmt.Errorf("unknown dns records subcommand %q", args[0])
	}
}

func dnsRecordsList(args []string, out io.Writer) error {
	if helpRequested(args, out, usageDNS) {
		return nil
	}
	fs := flag.NewFlagSet("dns-records-list", flag.ContinueOnError)
	jsonFlag := fs.Bool("json", false, "output as JSON")
	pos, done, err := parseDNSArgs(fs, args, 1, "dns records list takes a single <domain>")
	if err != nil || done {
		return err
	}
	return withDNS(func(ctx context.Context, c *netcup.DNSClient) error {
		recs, err := c.ListRecords(ctx, pos[0])
		if err != nil {
			return err
		}
		if *jsonFlag {
			return writeDNSJSON(out, recs)
		}
		return printDNSRecords(out, recs)
	})
}

func dnsRecordsAdd(args []string, out io.Writer) error {
	if helpRequested(args, out, usageDNS) {
		return nil
	}
	fs := flag.NewFlagSet("dns-records-add", flag.ContinueOnError)
	jsonFlag := fs.Bool("json", false, "output as JSON")
	priority := fs.Int("priority", 0, "priority (MX, SRV)")
	pos, done, err := parseDNSArgs(fs, args, 4, "dns records add takes <domain> <hostname> <type> <destination>")
	if err != nil || done {
		return err
	}
	return withDNS(func(ctx context.Context, c *netcup.DNSClient) error {
		created, err := c.CreateRecord(ctx, pos[0], netcup.DNSRecord{
			Hostname: pos[1], Type: pos[2], Destination: pos[3], Priority: *priority,
		})
		if err != nil {
			return err
		}
		if *jsonFlag {
			return writeDNSJSON(out, created)
		}
		return printDNSRecords(out, []netcup.DNSRecord{*created})
	})
}

func dnsRecordsUpdate(args []string, out io.Writer) error {
	if helpRequested(args, out, usageDNS) {
		return nil
	}
	fs := flag.NewFlagSet("dns-records-update", flag.ContinueOnError)
	jsonFlag := fs.Bool("json", false, "output as JSON")
	hostname := fs.String("hostname", "", "new hostname")
	typ := fs.String("type", "", "new record type")
	destination := fs.String("destination", "", "new destination")
	priority := fs.Int("priority", -1, "new priority")
	pos, done, err := parseDNSArgs(fs, args, 2, "dns records update takes <domain> <id>")
	if err != nil || done {
		return err
	}
	if *hostname == "" && *typ == "" && *destination == "" && *priority < 0 {
		return errors.New("dns records update needs at least one of --hostname, --type, --destination, --priority")
	}
	return withDNS(func(ctx context.Context, c *netcup.DNSClient) error {
		recs, err := c.ListRecords(ctx, pos[0])
		if err != nil {
			return err
		}
		var rec *netcup.DNSRecord
		for i := range recs {
			if recs[i].ID == pos[1] {
				rec = &recs[i]
			}
		}
		if rec == nil {
			return fmt.Errorf("record %s not found in %s", pos[1], pos[0])
		}
		if *hostname != "" {
			rec.Hostname = *hostname
		}
		if *typ != "" {
			rec.Type = *typ
		}
		if *destination != "" {
			rec.Destination = *destination
		}
		if *priority >= 0 {
			rec.Priority = *priority
		}
		updated, err := c.UpdateRecord(ctx, pos[0], *rec)
		if err != nil {
			return err
		}
		if *jsonFlag {
			return writeDNSJSON(out, updated)
		}
		return printDNSRecords(out, []netcup.DNSRecord{*updated})
	})
}

// dnsRecordsDelete removes a record. DESTRUCTIVE — confirms unless --force/--yes.
func dnsRecordsDelete(args []string, out, errW io.Writer, in io.Reader) error {
	if helpRequested(args, out, usageDNS) {
		return nil
	}
	fs := flag.NewFlagSet("dns-records-delete", flag.ContinueOnError)
	jsonFlag := fs.Bool("json", false, "output as JSON")
	forceFlag := fs.Bool("force", false, "skip the confirmation prompt")
	yesFlag := fs.Bool("yes", false, "alias for --force")
	pos, done, err := parseDNSArgs(fs, args, 2, "dns records delete takes <domain> <id>")
	if err != nil || done {
		return err
	}
	if !*forceFlag && !*yesFlag {
		confirmed, err := confirmAction(errW, in,
			fmt.Sprintf("Deleting DNS record %s from %s is permanent and affects live DNS resolution.", pos[1], pos[0]),
			fmt.Sprintf("Continue with 'dns records delete' on %s?", pos[0]))
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Fprintln(errW, "Aborted; no changes made.")
			return errors.New("aborted by user")
		}
	}
	return withDNS(func(ctx context.Context, c *netcup.DNSClient) error {
		if err := c.DeleteRecord(ctx, pos[0], pos[1]); err != nil {
			return err
		}
		if *jsonFlag {
			return writeDNSJSON(out, map[string]any{"domain": pos[0], "id": pos[1], "deleted": true})
		}
		fmt.Fprintf(out, "Deleted DNS record %s from %s\n", pos[1], pos[0])
		return nil
	})
}
