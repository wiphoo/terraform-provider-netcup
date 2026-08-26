package main

import (
	"bufio"
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
	"time"

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
	case "images":
		return serverImages(args[1:], os.Stdout)
	case "snapshots":
		return serverSnapshots(args[1:], os.Stdout, os.Stderr, os.Stdin)
	case "power":
		return serverPower(args[1:], os.Stdout, os.Stderr, os.Stdin)
	case "rescue":
		return serverRescue(args[1:], os.Stdout, os.Stderr, os.Stdin)
	case "reinstall":
		return serverReinstall(args[1:], os.Stdout, os.Stderr, os.Stdin)
	case "help", "-h", "--help":
		usageServer(os.Stdout)
		return nil
	default:
		usageServer(os.Stderr)
		return fmt.Errorf("unknown server subcommand %q", args[0])
	}
}

func usageServer(w io.Writer) {
	fmt.Fprint(w, `netcupctl server - manage servers

Usage:
  netcupctl server list [--json]
  netcupctl server get <id> [--json]
  netcupctl server images <id> [--json]
  netcupctl server snapshots <subcommand> <id> [flags]   # list | create | delete | restore
  netcupctl server power <subcommand> <id> [flags]
  netcupctl server rescue <subcommand> <id> [flags]
  netcupctl server reinstall <id> --image <flavourId> [flags]
  netcupctl server help          show this help

WARNING: 'reinstall' WIPES THE SERVER (all data is permanently lost).
'server snapshots restore' reverts the server's disks and REBOOTS it — data
changed since the snapshot is lost.

Run 'netcupctl server snapshots help' for snapshot subcommands.
Run 'netcupctl server power help' for power subcommands.
Run 'netcupctl server rescue help' for rescue subcommands.
Run 'netcupctl server reinstall help' for reinstall flags.
`)
}

func serverList(args []string, out io.Writer) error {
	if helpRequested(args, out, usageServer) {
		return nil
	}
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
	if helpRequested(args, out, usageServer) {
		return nil
	}
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

func serverImages(args []string, out io.Writer) error {
	if helpRequested(args, out, usageServer) {
		return nil
	}
	fs := flag.NewFlagSet("server-images", flag.ContinueOnError)
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
		return fmt.Errorf("server images requires a server ID")
	}
	if len(positional) > 1 {
		return fmt.Errorf("server images takes a single server ID, got %d arguments", len(positional))
	}
	id, err := strconv.ParseInt(positional[0], 10, 32)
	if err != nil {
		return fmt.Errorf("invalid server ID %q: must be an integer", positional[0])
	}

	client, err := clientWithToken()
	if err != nil {
		return err
	}
	flavours, err := client.ListImageFlavours(context.Background(), int32(id))
	if err != nil {
		return err
	}

	if *jsonFlag {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(flavours)
	}

	if len(flavours) == 0 {
		fmt.Fprintln(out, "No image flavours found.")
		return nil
	}

	tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tALIAS\tIMAGE")
	for _, f := range flavours {
		image := "-"
		if f.Image != nil && f.Image.Name != "" {
			image = f.Image.Name
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", f.ID, f.Name, f.Alias, image)
	}
	return tw.Flush()
}

// serverSnapshots dispatches the `server snapshots` subcommands. out receives
// the machine-readable result (JSON/table); errW receives interactive and
// diagnostic text (warnings, the confirmation prompt, abort notices) so that
// --json output on out stays parseable; in supplies the confirmation answer.
//
// The pre-v0.7.0 form `server snapshots <id>` (list) is preserved: a first
// argument that is not a known subcommand is treated as the legacy server-ID
// listing, so existing scripts and tests keep working.
func serverSnapshots(args []string, out, errW io.Writer, in io.Reader) error {
	if len(args) == 0 {
		usageServerSnapshots(errW)
		return fmt.Errorf("server snapshots requires a subcommand or a server ID")
	}

	switch args[0] {
	case "list":
		return serverSnapshotsList(args[1:], out)
	case "create":
		return serverSnapshotsCreate(args[1:], out, errW, in)
	case "delete":
		return serverSnapshotsDelete(args[1:], out, errW, in)
	case "restore":
		return serverSnapshotsRestore(args[1:], out, errW, in)
	case "help", "-h", "--help":
		usageServerSnapshots(out)
		return nil
	default:
		// Legacy alias: `server snapshots <id> [--json]` lists.
		return serverSnapshotsList(args, out)
	}
}

func usageServerSnapshots(w io.Writer) {
	fmt.Fprint(w, `netcupctl server snapshots - manage server snapshots

Usage:
  netcupctl server snapshots list    <id> [--json]
  netcupctl server snapshots create  <id> --name <name> [--description <desc>] [--online|--disk <disk>] [--wait] [--json]
  netcupctl server snapshots delete  <id> <name> [--force|--yes] [--wait] [--json]
  netcupctl server snapshots restore <id> <name> [--force|--yes] [--wait] [--json]
  netcupctl server snapshots <id> [--json]     # legacy alias for 'list'

WARNING: 'delete' permanently removes the snapshot. 'restore' reverts the
server's disks to the snapshot and REBOOTS the server — data changed since the
snapshot is lost. Both prompt for confirmation unless --force (or --yes) is
given.

Flags:
  --name <string>          snapshot name (REQUIRED for create; max 255 chars)
  --description <string>   snapshot description (max 255 chars)
  --online                 take an online snapshot of the running server (no --disk needed)
  --disk <string>          disk name to snapshot (required for an offline snapshot)
  --wait                   poll the async task to a terminal state and print the result
  --force                  skip the confirmation prompt
  --yes                    alias for --force
  --json                   output as JSON
`)
}

// serverSnapshotsList lists a server's snapshots (the legacy leaf and the
// explicit `server snapshots list` form).
func serverSnapshotsList(args []string, out io.Writer) error {
	if helpRequested(args, out, usageServerSnapshots) {
		return nil
	}
	fs := flag.NewFlagSet("server-snapshots-list", flag.ContinueOnError)
	jsonFlag := fs.Bool("json", false, "output as JSON")
	positional, err := parsePositionalArgs(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if len(positional) == 0 {
		usageServerSnapshots(os.Stderr)
		return fmt.Errorf("server snapshots requires a server ID")
	}
	if len(positional) > 1 {
		return fmt.Errorf("server snapshots takes a single server ID, got %d arguments", len(positional))
	}
	id, err := strconv.ParseInt(positional[0], 10, 32)
	if err != nil {
		return fmt.Errorf("invalid server ID %q: must be an integer", positional[0])
	}

	client, err := clientWithToken()
	if err != nil {
		return err
	}
	snapshots, err := client.ListSnapshots(context.Background(), int32(id))
	if err != nil {
		return err
	}

	if *jsonFlag {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(snapshots)
	}

	if len(snapshots) == 0 {
		fmt.Fprintln(out, "No snapshots found.")
		return nil
	}

	tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "NAME\tCREATED\tSTATE\tONLINE\tEXPORTED")
	for _, s := range snapshots {
		created := "-"
		if !s.CreationTime.IsZero() {
			created = s.CreationTime.Format(time.RFC3339)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%t\t%t\n", s.Name, created, s.State, s.Online, s.Exported)
	}
	return tw.Flush()
}

// serverSnapshotsCreate starts a snapshot via the SDK CreateSnapshot. It is not
// destructive, so it never prompts; it still supports --wait/--json.
func serverSnapshotsCreate(args []string, out, errW io.Writer, in io.Reader) error {
	if helpRequested(args, out, usageServerSnapshots) {
		return nil
	}
	fs := flag.NewFlagSet("server-snapshots-create", flag.ContinueOnError)
	fs.SetOutput(errW)
	fs.Usage = func() { usageServerSnapshots(errW) }
	jsonFlag := fs.Bool("json", false, "output as JSON")
	waitFlag := fs.Bool("wait", false, "poll the task to a terminal state")
	nameFlag := fs.String("name", "", "snapshot name (REQUIRED, max 255 chars)")
	descriptionFlag := fs.String("description", "", "snapshot description (max 255 chars)")
	onlineFlag := fs.Bool("online", false, "take an online snapshot (no --disk needed)")
	diskFlag := fs.String("disk", "", "disk name to snapshot (required unless --online)")

	id, done, err := parseServerIDArg(fs, args, "server snapshots create", usageServerSnapshots)
	if err != nil || done {
		return err
	}

	if strings.TrimSpace(*nameFlag) == "" {
		usageServerSnapshots(errW)
		return fmt.Errorf("server snapshots create requires --name <name>")
	}
	if !*onlineFlag && strings.TrimSpace(*diskFlag) == "" {
		return fmt.Errorf("server snapshots create requires --disk <disk> for an offline snapshot, or --online")
	}

	opts := netcup.ServerSnapshotCreate{Name: *nameFlag}
	if *descriptionFlag != "" {
		desc := *descriptionFlag
		opts.Description = &desc
	}
	if *diskFlag != "" {
		disk := *diskFlag
		opts.DiskName = &disk
	}
	if *onlineFlag {
		opts.OnlineSnapshot = true
	}

	client, err := clientWithToken()
	if err != nil {
		return err
	}
	task, err := client.CreateSnapshot(context.Background(), id, opts)
	if err != nil {
		return err
	}

	waited := false
	if *waitFlag && task != nil {
		final, err := client.WaitForTask(context.Background(), task.UUID)
		if err != nil {
			return err
		}
		task = final
		waited = true
	}

	return printSnapshotResult(out, *jsonFlag, "create", id, opts.Name, task, waited)
}

// serverSnapshotsDelete removes a snapshot via the SDK DeleteSnapshot.
// DESTRUCTIVE (the snapshot is irrecoverably removed) — confirms unless
// --force/--yes.
func serverSnapshotsDelete(args []string, out, errW io.Writer, in io.Reader) error {
	if helpRequested(args, out, usageServerSnapshots) {
		return nil
	}
	fs := flag.NewFlagSet("server-snapshots-delete", flag.ContinueOnError)
	fs.SetOutput(errW)
	fs.Usage = func() { usageServerSnapshots(errW) }
	jsonFlag := fs.Bool("json", false, "output as JSON")
	waitFlag := fs.Bool("wait", false, "poll the task to a terminal state")
	forceFlag := fs.Bool("force", false, "skip the confirmation prompt")
	yesFlag := fs.Bool("yes", false, "alias for --force")

	id, name, err := parseSnapshotIDNameArg(fs, args, "server snapshots delete", usageServerSnapshots)
	if err != nil {
		return err
	}

	if !*forceFlag && !*yesFlag {
		confirmed, err := confirmAction(errW, in,
			fmt.Sprintf("Deleting snapshot %q from server %d permanently removes it and cannot be undone.", name, id),
			fmt.Sprintf("Continue with 'snapshots delete' on server %d?", id))
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Fprintln(errW, "Aborted; no changes made.")
			return fmt.Errorf("aborted by user")
		}
	}

	client, err := clientWithToken()
	if err != nil {
		return err
	}
	task, err := client.DeleteSnapshot(context.Background(), id, name)
	if err != nil {
		return err
	}

	waited := false
	if *waitFlag && task != nil {
		final, err := client.WaitForTask(context.Background(), task.UUID)
		if err != nil {
			return err
		}
		task = final
		waited = true
	}

	return printSnapshotResult(out, *jsonFlag, "delete", id, name, task, waited)
}

// serverSnapshotsRestore reverts a server from a snapshot via the SDK
// RestoreSnapshot. DESTRUCTIVE — the server's disks are reverted to the
// snapshot and the server is rebooted — so it warns prominently and confirms
// unless --force/--yes, mirroring `server reinstall`.
func serverSnapshotsRestore(args []string, out, errW io.Writer, in io.Reader) error {
	if helpRequested(args, out, usageServerSnapshots) {
		return nil
	}
	fs := flag.NewFlagSet("server-snapshots-restore", flag.ContinueOnError)
	fs.SetOutput(errW)
	fs.Usage = func() { usageServerSnapshots(errW) }
	jsonFlag := fs.Bool("json", false, "output as JSON")
	waitFlag := fs.Bool("wait", false, "poll the task to a terminal state")
	forceFlag := fs.Bool("force", false, "skip the confirmation prompt")
	yesFlag := fs.Bool("yes", false, "alias for --force")

	id, name, err := parseSnapshotIDNameArg(fs, args, "server snapshots restore", usageServerSnapshots)
	if err != nil {
		return err
	}

	if !*forceFlag && !*yesFlag {
		confirmed, err := confirmAction(errW, in,
			fmt.Sprintf("Restoring snapshot %q REVERTS server %d's disks to the snapshot and REBOOTS the server — data changed since the snapshot is lost.", name, id),
			fmt.Sprintf("Continue with 'snapshots restore' on server %d?", id))
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Fprintln(errW, "Aborted; no changes made.")
			return fmt.Errorf("aborted by user")
		}
	}

	client, err := clientWithToken()
	if err != nil {
		return err
	}
	task, err := client.RestoreSnapshot(context.Background(), id, name)
	if err != nil {
		return err
	}

	waited := false
	if *waitFlag && task != nil {
		final, err := client.WaitForTask(context.Background(), task.UUID)
		if err != nil {
			return err
		}
		task = final
		waited = true
	}

	return printSnapshotResult(out, *jsonFlag, "restore", id, name, task, waited)
}

// printSnapshotResult renders the outcome of a snapshot create/delete/restore.
// task is the accepted (or, with --wait, the final) TaskInfo.
func printSnapshotResult(out io.Writer, asJSON bool, action string, id int32, name string, task *netcup.TaskInfo, waited bool) error {
	if asJSON {
		return json.NewEncoder(out).Encode(map[string]interface{}{
			"serverId": id,
			"snapshot": name,
			"action":   action,
			"task":     task,
		})
	}

	tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintf(tw, "Server:\t%d\n", id)
	fmt.Fprintf(tw, "Snapshot:\t%s\n", name)
	fmt.Fprintf(tw, "Action:\t%s\n", action)
	if task != nil {
		fmt.Fprintf(tw, "Task:\t%s\n", task.UUID)
		if waited {
			fmt.Fprintf(tw, "Task State:\t%s\n", task.State)
		} else {
			fmt.Fprintf(tw, "Task State:\t%s (accepted; use --wait to poll)\n", task.State)
		}
	}
	return tw.Flush()
}

// parseSnapshotIDNameArg parses fs and requires exactly two positionals: a
// server ID and a snapshot name. context is used in error messages (e.g. "server
// snapshots delete"), and usage prints the relevant subcommand help on a
// missing-argument error. done is true when the caller should stop with a clean
// exit — a -h/--help request — in which case id/name/err are zero and the
// caller returns nil.
func parseSnapshotIDNameArg(fs *flag.FlagSet, args []string, context string, usage func(io.Writer)) (id int32, name string, err error) {
	positional, err := parsePositionalArgs(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, "", nil
		}
		return 0, "", err
	}
	if len(positional) == 0 {
		usage(os.Stderr)
		return 0, "", fmt.Errorf("%s requires a server ID and a snapshot name", context)
	}
	if len(positional) == 1 {
		return 0, "", fmt.Errorf("%s requires a snapshot name", context)
	}
	if len(positional) > 2 {
		return 0, "", fmt.Errorf("%s takes a server ID and a snapshot name, got %d arguments", context, len(positional))
	}
	parsed, err := strconv.ParseInt(positional[0], 10, 32)
	if err != nil {
		return 0, "", fmt.Errorf("invalid server ID %q: must be an integer", positional[0])
	}
	return int32(parsed), positional[1], nil
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

// powerAction describes one `server power` mutating verb: the desired state it
// sends, whether it causes downtime (and so needs confirmation), and its
// stateOption mapping. hardOption is the ?stateOption= value used with --hard;
// an empty hardOption means --hard is not supported for that verb. softOption is
// the value used without --hard (empty means the stateOption query is omitted).
type powerAction struct {
	verb       string
	state      netcup.PowerState
	downtime   bool
	softOption string
	hardOption string
	warning    string
}

// powerActions maps each mutating subcommand to its behavior. reboot is a native
// power-cycle (state ON with the POWERCYCLE stateOption, or RESET with --hard),
// not an OFF-then-ON sequence. off defaults to a soft/ACPI shutdown and uses the
// POWEROFF stateOption for a hard poweroff with --hard.
var powerActions = map[string]powerAction{
	"on": {
		verb:  "on",
		state: netcup.PowerOn,
	},
	"off": {
		verb:       "off",
		state:      netcup.PowerOff,
		downtime:   true,
		hardOption: "POWEROFF",
		warning:    "Powering off a server causes immediate downtime.",
	},
	"suspend": {
		verb:     "suspend",
		state:    netcup.PowerSuspended,
		downtime: true,
		warning:  "Suspending a server pauses it and causes downtime until it is resumed.",
	},
	"reboot": {
		verb:       "reboot",
		state:      netcup.PowerOn,
		downtime:   true,
		softOption: "POWERCYCLE",
		hardOption: "RESET",
		warning:    "Rebooting a server causes downtime while it restarts.",
	},
}

// serverPower dispatches the `server power` subcommands. out receives the
// machine-readable result (JSON/table); errW receives interactive and
// diagnostic text (the downtime warning, the confirmation prompt, abort
// notices) so that --json output on out stays parseable.
func serverPower(args []string, out, errW io.Writer, in io.Reader) error {
	if len(args) == 0 {
		usageServerPower(errW)
		return fmt.Errorf("server power requires a subcommand")
	}

	switch args[0] {
	case "status":
		return serverPowerStatus(args[1:], out)
	case "on", "off", "suspend", "reboot":
		return serverPowerSet(powerActions[args[0]], args[1:], out, errW, in)
	case "help", "-h", "--help":
		usageServerPower(out)
		return nil
	default:
		usageServerPower(errW)
		return fmt.Errorf("unknown server power subcommand %q", args[0])
	}
}

func usageServerPower(w io.Writer) {
	fmt.Fprint(w, `netcupctl server power - control server power state

Usage:
  netcupctl server power status  <id> [--json]
  netcupctl server power on      <id> [--wait] [--json]
  netcupctl server power off     <id> [--hard] [--wait] [--force|--yes] [--json]
  netcupctl server power suspend <id> [--wait] [--force|--yes] [--json]
  netcupctl server power reboot  <id> [--hard] [--wait] [--force|--yes] [--json]

WARNING: off, suspend, and reboot cause downtime. They prompt for confirmation
unless --force (or --yes) is given.

Flags:
  --hard    off: hard poweroff (POWEROFF); reboot: hard reset (RESET)
  --wait    poll the async task to a terminal state and print the result
  --force   skip the downtime confirmation prompt
  --yes     alias for --force
  --json    output as JSON
`)
}

// serverPowerStatus prints a server's current live power state
// (serverLiveInfo.state), reusing GetServer.
func serverPowerStatus(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("server-power-status", flag.ContinueOnError)
	jsonFlag := fs.Bool("json", false, "output as JSON")
	id, done, err := parseServerIDArg(fs, args, "server power status", usageServerPower)
	if err != nil || done {
		return err
	}

	client, err := clientWithToken()
	if err != nil {
		return err
	}
	server, err := client.GetServer(context.Background(), id)
	if err != nil {
		return err
	}

	state := "unknown"
	if server.ServerLiveInfo != nil && server.ServerLiveInfo.State != "" {
		state = server.ServerLiveInfo.State
	}

	if *jsonFlag {
		return json.NewEncoder(out).Encode(map[string]interface{}{
			"serverId": server.ID,
			"state":    state,
		})
	}

	tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintf(tw, "ID:\t%d\n", server.ID)
	fmt.Fprintf(tw, "State:\t%s\n", state)
	return tw.Flush()
}

// serverPowerSet performs a power state change (on/off/suspend/reboot). It
// resolves the stateOption from --hard, confirms downtime actions unless
// --force/--yes is set, calls SetPowerState, and optionally waits for the async
// task to reach a terminal state.
func serverPowerSet(action powerAction, args []string, out, errW io.Writer, in io.Reader) error {
	fs := flag.NewFlagSet("server-power-"+action.verb, flag.ContinueOnError)
	jsonFlag := fs.Bool("json", false, "output as JSON")
	waitFlag := fs.Bool("wait", false, "poll the task to a terminal state")
	hardFlag := fs.Bool("hard", false, "use the hard stateOption")
	forceFlag := fs.Bool("force", false, "skip the confirmation prompt")
	yesFlag := fs.Bool("yes", false, "alias for --force")
	id, done, err := parseServerIDArg(fs, args, "server power "+action.verb, usageServerPower)
	if err != nil || done {
		return err
	}

	stateOption := action.softOption
	if *hardFlag {
		if action.hardOption == "" {
			return fmt.Errorf("--hard is not supported for 'server power %s'", action.verb)
		}
		stateOption = action.hardOption
	}

	if action.downtime && !*forceFlag && !*yesFlag {
		confirmed, err := confirmDowntime(errW, in, action, id)
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Fprintln(errW, "Aborted; no changes made.")
			return fmt.Errorf("aborted by user")
		}
	}

	client, err := clientWithToken()
	if err != nil {
		return err
	}

	task, err := client.SetPowerState(context.Background(), id, action.state, stateOption)
	if err != nil {
		return err
	}

	// --wait polls the returned task to a terminal state. A synchronous 200
	// response has no task to wait for.
	waited := false
	if *waitFlag && task != nil {
		final, err := client.WaitForTask(context.Background(), task.UUID)
		if err != nil {
			return err
		}
		task = final
		waited = true
	}

	return printPowerResult(out, *jsonFlag, id, action.state, stateOption, task, waited)
}

// confirmAction writes a warning and a yes/no prompt to errW (stderr, not the
// result stream, so --json output on stdout stays parseable) and reads the
// answer from in. warning is shown as "WARNING: <warning>"; prompt is the
// question shown before "[y/N]: ". Only "y"/"yes" (case-insensitive) confirms;
// anything else (including EOF) declines. It is shared by the power and rescue
// confirmation prompts.
func confirmAction(errW io.Writer, in io.Reader, warning, prompt string) (bool, error) {
	fmt.Fprintf(errW, "WARNING: %s\n", warning)
	fmt.Fprintf(errW, "%s [y/N]: ", prompt)

	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// confirmDowntime prompts before a power change that causes downtime, delegating
// to confirmAction with power-specific wording.
func confirmDowntime(errW io.Writer, in io.Reader, action powerAction, id int32) (bool, error) {
	return confirmAction(errW, in, action.warning,
		fmt.Sprintf("Continue with 'power %s' on server %d?", action.verb, id))
}

// printPowerResult renders the outcome of a power change. task is nil for a
// synchronous 200; when non-nil it is the accepted (or, with --wait, the final)
// TaskInfo.
func printPowerResult(out io.Writer, asJSON bool, id int32, state netcup.PowerState, stateOption string, task *netcup.TaskInfo, waited bool) error {
	if asJSON {
		payload := map[string]interface{}{
			"serverId":  id,
			"requested": state,
		}
		if stateOption != "" {
			payload["stateOption"] = stateOption
		}
		payload["task"] = task
		return json.NewEncoder(out).Encode(payload)
	}

	tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintf(tw, "Server:\t%d\n", id)
	fmt.Fprintf(tw, "Requested:\t%s\n", state)
	if stateOption != "" {
		fmt.Fprintf(tw, "Option:\t%s\n", stateOption)
	}
	switch {
	case task == nil:
		fmt.Fprintf(tw, "Result:\t%s\n", "applied synchronously (no task)")
	case waited:
		fmt.Fprintf(tw, "Task:\t%s\n", task.UUID)
		fmt.Fprintf(tw, "Task State:\t%s\n", task.State)
	default:
		fmt.Fprintf(tw, "Task:\t%s\n", task.UUID)
		fmt.Fprintf(tw, "Task State:\t%s (accepted; use --wait to poll)\n", task.State)
	}
	return tw.Flush()
}

// parseServerIDArg parses fs and requires exactly one positional server ID,
// returning it as an int32. context is used in error messages (e.g. "server
// power off"), and usage prints the relevant subcommand help on a missing-ID
// error (e.g. usageServerPower vs usageServerRescue). done is true when the
// caller should stop with a clean exit — a -h/--help request — in which case
// id/err are zero and the caller returns nil.
func parseServerIDArg(fs *flag.FlagSet, args []string, context string, usage func(io.Writer)) (id int32, done bool, err error) {
	positional, err := parsePositionalArgs(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, true, nil
		}
		return 0, false, err
	}
	if len(positional) == 0 {
		usage(os.Stderr)
		return 0, false, fmt.Errorf("%s requires a server ID", context)
	}
	if len(positional) > 1 {
		return 0, false, fmt.Errorf("%s takes a single server ID, got %d arguments", context, len(positional))
	}
	parsed, err := strconv.ParseInt(positional[0], 10, 32)
	if err != nil {
		return 0, false, fmt.Errorf("invalid server ID %q: must be an integer", positional[0])
	}
	return int32(parsed), false, nil
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
