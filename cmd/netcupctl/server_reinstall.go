package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// int32SliceFlag collects a repeatable integer flag (e.g. --ssh-key 1 --ssh-key
// 2) into an []int32, used for ServerImageSetup.SSHKeyIDs.
type int32SliceFlag []int32

func (f *int32SliceFlag) String() string {
	parts := make([]string, len(*f))
	for i, v := range *f {
		parts[i] = strconv.FormatInt(int64(v), 10)
	}
	return strings.Join(parts, ",")
}

func (f *int32SliceFlag) Set(value string) error {
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return fmt.Errorf("invalid ssh key id %q: must be an integer", value)
	}
	*f = append(*f, int32(parsed))
	return nil
}

// serverReinstall performs a native OS reinstall on a server via the SDK
// ReinstallServer (POST /v1/servers/{id}/image). This is DESTRUCTIVE — it wipes
// the server and all data on it is permanently lost — so it confirms before
// proceeding (unless --force/--yes). out receives the machine-readable result
// (JSON/table); errW receives interactive and diagnostic text (the data-loss
// warning, the confirmation prompt, abort notices) so that --json output on out
// stays parseable.
func serverReinstall(args []string, out, errW io.Writer, in io.Reader) error {
	fs := flag.NewFlagSet("server-reinstall", flag.ContinueOnError)
	jsonFlag := fs.Bool("json", false, "output as JSON")
	waitFlag := fs.Bool("wait", false, "poll the task to a terminal state")
	forceFlag := fs.Bool("force", false, "skip the confirmation prompt")
	yesFlag := fs.Bool("yes", false, "alias for --force")
	var imageVal int32
	var imageSet bool
	fs.Func("image", "imageFlavourId to install (REQUIRED, must fit int32)", func(s string) error {
		parsed, err := strconv.ParseInt(s, 10, 32)
		if err != nil {
			return fmt.Errorf("invalid --image value %q: must be an int32", s)
		}
		imageVal = int32(parsed)
		imageSet = true
		return nil
	})
	hostnameFlag := fs.String("hostname", "", "hostname to set on the reinstalled server")
	customScriptFlag := fs.String("custom-script", "", "post-install bootstrap script (inline)")
	customScriptFileFlag := fs.String("custom-script-file", "", "post-install bootstrap script from a file ('-' for stdin)")
	sshPasswordAuthFlag := fs.Bool("ssh-password-auth", false, "enable SSH password authentication")
	sshPasswordAuthSet := false
	var sshKeys int32SliceFlag
	fs.Var(&sshKeys, "ssh-key", "SSH key id to authorize (repeatable)")

	id, done, err := parseServerIDArg(fs, args, "server reinstall", usageServerReinstall)
	if err != nil || done {
		return err
	}

	// Detect which optional flags the user actually set so nil pointers are
	// preserved for everything they left out.
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "image":
			imageSet = true
		case "ssh-password-auth":
			sshPasswordAuthSet = true
		}
	})

	if !imageSet {
		usageServerReinstall(errW)
		return fmt.Errorf("server reinstall requires --image <flavourId> (list valid ids with 'netcupctl server images %d')", id)
	}
	if *customScriptFlag != "" && *customScriptFileFlag != "" {
		return fmt.Errorf("--custom-script and --custom-script-file are mutually exclusive")
	}

	setup := netcup.ServerImageSetup{}
	image := imageVal
	setup.ImageFlavourID = &image
	if *hostnameFlag != "" {
		setup.Hostname = hostnameFlag
	}
	if len(sshKeys) > 0 {
		setup.SSHKeyIDs = sshKeys
	}
	if sshPasswordAuthSet {
		setup.SSHPasswordAuthentication = sshPasswordAuthFlag
	}

	if !*forceFlag && !*yesFlag {
		confirmed, err := confirmAction(errW, in,
			fmt.Sprintf("Reinstalling server %d WIPES THE SERVER — all data on it is permanently lost and cannot be recovered.", id),
			fmt.Sprintf("Continue with 'reinstall' on server %d (imageFlavourId %d)?", id, image))
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Fprintln(errW, "Aborted; no changes made.")
			return fmt.Errorf("aborted by user")
		}
	}

	switch {
	case *customScriptFlag != "":
		script := *customScriptFlag
		setup.CustomScript = &script
	case *customScriptFileFlag != "":
		script, err := readCustomScript(*customScriptFileFlag, in)
		if err != nil {
			return err
		}
		setup.CustomScript = &script
	}

	client, err := clientWithToken()
	if err != nil {
		return err
	}
	task, err := client.ReinstallServer(context.Background(), id, setup)
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

	return printReinstallResult(out, *jsonFlag, id, image, task, waited)
}

// readCustomScript reads the post-install bootstrap script from path, or from in
// (stdin) when path is "-".
func readCustomScript(path string, in io.Reader) (string, error) {
	if path == "-" {
		data, err := io.ReadAll(in)
		if err != nil {
			return "", fmt.Errorf("reading custom script from stdin: %w", err)
		}
		return string(data), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading custom script file %q: %w", path, err)
	}
	return string(data), nil
}

// printReinstallResult renders the outcome of a reinstall. task is the accepted
// (or, with --wait, the final) TaskInfo.
func printReinstallResult(out io.Writer, asJSON bool, id, image int32, task *netcup.TaskInfo, waited bool) error {
	if asJSON {
		return json.NewEncoder(out).Encode(map[string]interface{}{
			"serverId":       id,
			"imageFlavourId": image,
			"action":         "reinstall",
			"task":           task,
		})
	}

	tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintf(tw, "Server:\t%d\n", id)
	fmt.Fprintf(tw, "Action:\t%s\n", "reinstall")
	fmt.Fprintf(tw, "Image:\t%d\n", image)
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

func usageServerReinstall(w io.Writer) {
	fmt.Fprint(w, `netcupctl server reinstall - reinstall a server's operating system

Usage:
  netcupctl server reinstall <id> --image <flavourId> [flags]

WARNING: reinstall WIPES THE SERVER. All data on it is permanently lost and
cannot be recovered. This is far more destructive than a power/rescue reboot.
It prompts for confirmation unless --force (or --yes) is given.

Discover valid image flavour ids with 'netcupctl server images <id>'.

Flags:
  --image <int>              imageFlavourId to install (REQUIRED)
  --hostname <string>        hostname to set on the reinstalled server
  --ssh-key <int>            SSH key id to authorize (repeatable)
  --ssh-password-auth        enable SSH password authentication
  --custom-script <string>   post-install bootstrap script (inline)
  --custom-script-file <path>  post-install bootstrap script from a file ('-' for stdin)
  --wait                     poll the async task to a terminal state and print the result
  --force                    skip the data-loss confirmation prompt
  --yes                      alias for --force
  --json                     output as JSON

Note: diskName, locale, timezone, and additional-user setup fields of the API
are not yet exposed by this command.
`)
}
