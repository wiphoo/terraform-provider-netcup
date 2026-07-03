package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// rdnsConfirmAttempts and rdnsConfirmDelay bound the read-back confirmation
// after a set. netcup applies PTR changes asynchronously, so confirmRDNS
// re-reads across this window before giving up. They are package variables so
// tests can shrink the delay.
var (
	rdnsConfirmAttempts = 5
	rdnsConfirmDelay    = 1 * time.Second
)

// confirmRDNS re-reads the PTR for ip until it matches the value we set,
// absorbing netcup's asynchronous provisioning delay. It returns the confirmed
// entry on success, or an error if the requested PTR could not be confirmed
// within the retry window (so an unverified set does not exit 0).
func confirmRDNS(client *netcup.Client, ip string, set *netcup.RdnsEntry) (*netcup.RdnsEntry, error) {
	var lastErr error
	var lastHostname string
	for attempt := 0; attempt < rdnsConfirmAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(rdnsConfirmDelay)
		}
		readBack, err := client.GetRDNS(context.Background(), ip)
		if err != nil {
			lastErr = err
			continue
		}
		if rdnsHostnamesEqual(readBack.Hostname, set.Hostname) {
			return readBack, nil
		}
		lastErr = nil
		lastHostname = readBack.Hostname
	}
	if lastErr != nil {
		return nil, fmt.Errorf("rdns set succeeded but could not be confirmed after %d attempts: %w", rdnsConfirmAttempts, lastErr)
	}
	return nil, fmt.Errorf("rdns set succeeded but read-back did not match after %d attempts: set %q, got %q", rdnsConfirmAttempts, set.Hostname, lastHostname)
}

// rdnsHostnamesEqual reports whether two reverse-DNS hostnames are equivalent.
// PTR values are FQDNs: DNS names are case-insensitive and the API may return a
// canonicalized form with a trailing dot, so both are ignored (along with
// surrounding whitespace) when confirming a read-back.
func rdnsHostnamesEqual(a, b string) bool {
	return normalizeRDNSHostname(a) == normalizeRDNSHostname(b)
}

func normalizeRDNSHostname(h string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(h), "."))
}

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

	// Confirm the change actually landed before reporting success. netcup
	// provisions PTRs asynchronously, so an immediate read may still return the
	// previous value (or null); retry across a short window to absorb that.
	// The stored hostname may also be canonicalized (ASCII case, trailing FQDN
	// dot), so compare with rdnsHostnamesEqual. If confirmation never succeeds
	// the requested PTR is not verifiably in effect, so return an error rather
	// than a misleading exit code 0.
	entry, err := confirmRDNS(client, ip, set)
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
