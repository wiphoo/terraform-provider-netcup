package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// serverRescue dispatches the `server rescue` subcommands. out receives the
// machine-readable result (JSON/table); errW receives interactive and
// diagnostic text (the reboot warning, the confirmation prompt, abort notices)
// so that --json output on out stays parseable.
func serverRescue(args []string, out, errW io.Writer, in io.Reader) error {
	if len(args) == 0 {
		usageServerRescue(errW)
		return fmt.Errorf("server rescue requires a subcommand")
	}

	switch args[0] {
	case "status":
		return serverRescueStatus(args[1:], out)
	case "enable":
		return serverRescueEnable(args[1:], out, errW, in)
	case "disable":
		return serverRescueDisable(args[1:], out, errW, in)
	case "help", "-h", "--help":
		usageServerRescue(out)
		return nil
	default:
		usageServerRescue(errW)
		return fmt.Errorf("unknown server rescue subcommand %q", args[0])
	}
}

func usageServerRescue(w io.Writer) {
	fmt.Fprint(w, `netcupctl server rescue - control the rescue system

Usage:
  netcupctl server rescue status  <id> [--json]
  netcupctl server rescue enable  <id> [--wait] [--force|--yes] [--json]
  netcupctl server rescue disable <id> [--wait] [--force|--yes] [--json]

WARNING: enable and disable REBOOT the server (downtime). enable boots it into
the rescue environment; disable boots it back into the normal operating system.
Both prompt for confirmation unless --force (or --yes) is given.

The rescue password is only available while the rescue system is active. With
'enable --wait' it is read back and printed once activation finishes; otherwise
read it later with 'server rescue status'.

Flags:
  --wait    poll the async task to a terminal state and print the result
  --force   skip the reboot confirmation prompt
  --yes     alias for --force
  --json    output as JSON
`)
}

// serverRescueStatus prints a server's rescue-system status: whether it is
// active and, when active, the rescue password (only surfaced by the dedicated
// rescuesystem endpoint).
func serverRescueStatus(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("server-rescue-status", flag.ContinueOnError)
	jsonFlag := fs.Bool("json", false, "output as JSON")
	id, done, err := parseServerIDArg(fs, args, "server rescue status", usageServerRescue)
	if err != nil || done {
		return err
	}

	client, err := clientWithToken()
	if err != nil {
		return err
	}
	status, err := client.GetRescueSystem(context.Background(), id)
	if err != nil {
		return err
	}

	if *jsonFlag {
		return json.NewEncoder(out).Encode(map[string]interface{}{
			"serverId": id,
			"active":   status.Active,
			"password": status.Password,
		})
	}

	tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintf(tw, "Server:\t%d\n", id)
	if status.Active {
		fmt.Fprintf(tw, "Rescue:\t%s\n", "active")
		fmt.Fprintf(tw, "Password:\t%s\n", rescuePasswordText(status.Password))
	} else {
		fmt.Fprintf(tw, "Rescue:\t%s\n", "inactive")
	}
	return tw.Flush()
}

// serverRescueEnable activates the rescue system. It confirms the reboot (unless
// --force/--yes), calls EnableRescueSystem, and with --wait polls the async task
// to a terminal state and reads back the rescue password.
func serverRescueEnable(args []string, out, errW io.Writer, in io.Reader) error {
	fs := flag.NewFlagSet("server-rescue-enable", flag.ContinueOnError)
	jsonFlag := fs.Bool("json", false, "output as JSON")
	waitFlag := fs.Bool("wait", false, "poll the task to a terminal state")
	forceFlag := fs.Bool("force", false, "skip the confirmation prompt")
	yesFlag := fs.Bool("yes", false, "alias for --force")
	id, done, err := parseServerIDArg(fs, args, "server rescue enable", usageServerRescue)
	if err != nil || done {
		return err
	}

	if !*forceFlag && !*yesFlag {
		confirmed, err := confirmAction(errW, in,
			"Enabling the rescue system reboots the server into the rescue environment, causing downtime.",
			fmt.Sprintf("Continue with 'rescue enable' on server %d?", id))
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
	task, err := client.EnableRescueSystem(context.Background(), id)
	if err != nil {
		return err
	}

	// Without --wait there is no task to poll and the password is not yet
	// available; report the accepted task.
	if !*waitFlag {
		return printRescueResult(out, *jsonFlag, id, "enable", task, false, nil)
	}

	// --wait: poll the activation task to a terminal state, then read back the
	// rescue password. A wait failure (ERROR terminal or context deadline) is
	// returned as-is; the password GET is skipped because it would not exist.
	final, err := client.WaitForTask(context.Background(), task.UUID)
	if err != nil {
		return err
	}
	status, err := client.GetRescueSystem(context.Background(), id)
	if err != nil {
		return err
	}
	return printRescueResult(out, *jsonFlag, id, "enable", final, true, status)
}

// serverRescueDisable deactivates the rescue system. It confirms the reboot
// (unless --force/--yes), calls DisableRescueSystem, and with --wait polls the
// async task to a terminal state.
func serverRescueDisable(args []string, out, errW io.Writer, in io.Reader) error {
	fs := flag.NewFlagSet("server-rescue-disable", flag.ContinueOnError)
	jsonFlag := fs.Bool("json", false, "output as JSON")
	waitFlag := fs.Bool("wait", false, "poll the task to a terminal state")
	forceFlag := fs.Bool("force", false, "skip the confirmation prompt")
	yesFlag := fs.Bool("yes", false, "alias for --force")
	id, done, err := parseServerIDArg(fs, args, "server rescue disable", usageServerRescue)
	if err != nil || done {
		return err
	}

	if !*forceFlag && !*yesFlag {
		confirmed, err := confirmAction(errW, in,
			"Disabling the rescue system reboots the server back into its normal operating system, causing downtime.",
			fmt.Sprintf("Continue with 'rescue disable' on server %d?", id))
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
	task, err := client.DisableRescueSystem(context.Background(), id)
	if err != nil {
		return err
	}

	waited := false
	if *waitFlag {
		final, err := client.WaitForTask(context.Background(), task.UUID)
		if err != nil {
			return err
		}
		task = final
		waited = true
	}
	return printRescueResult(out, *jsonFlag, id, "disable", task, waited, nil)
}

// rescuePasswordText renders a rescue password for display, degrading to a hint
// when it is nil — the API may surface it a moment after activation finishes.
func rescuePasswordText(pw *string) string {
	if pw == nil {
		return "<unavailable — re-run 'server rescue status'>"
	}
	return *pw
}

// printRescueResult renders the outcome of an enable/disable. task is the
// accepted (or, with --wait, the final) TaskInfo. status is the read-back
// rescue status appended to a waited 'enable' (nil otherwise); when non-nil its
// password is printed.
func printRescueResult(out io.Writer, asJSON bool, id int32, action string, task *netcup.TaskInfo, waited bool, status *netcup.RescueSystemStatus) error {
	if asJSON {
		payload := map[string]interface{}{
			"serverId": id,
			"action":   action,
			"task":     task,
		}
		if status != nil {
			payload["active"] = status.Active
			payload["password"] = status.Password
		}
		return json.NewEncoder(out).Encode(payload)
	}

	tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintf(tw, "Server:\t%d\n", id)
	fmt.Fprintf(tw, "Action:\t%s\n", action)
	fmt.Fprintf(tw, "Task:\t%s\n", task.UUID)
	if waited {
		fmt.Fprintf(tw, "Task State:\t%s\n", task.State)
	} else {
		fmt.Fprintf(tw, "Task State:\t%s (accepted; use --wait to poll)\n", task.State)
	}
	if status != nil {
		fmt.Fprintf(tw, "Rescue:\t%s\n", rescueActiveText(status.Active))
		fmt.Fprintf(tw, "Password:\t%s\n", rescuePasswordText(status.Password))
	}
	return tw.Flush()
}

// rescueActiveText maps the active flag to the same label status uses.
func rescueActiveText(active bool) string {
	if active {
		return "active"
	}
	return "inactive"
}
