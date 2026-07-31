package main

import (
	"flag"
	"io"
)

// helpRequested reports whether args is a bare `help` word — the subcommand-style
// help request (e.g. `netcupctl server reinstall help`, `netcupctl rdns get help`),
// the sibling of the `-h`/`--help` flags that flag.Parse already recognizes. When
// it is, usage is written to out and the caller returns nil.
//
// Positional leaf commands (which parse an <ip>/<id>/flags rather than dispatching
// a sub-subcommand) call this first, so `<cmd> help` prints usage instead of `help`
// being consumed as a (non-numeric) argument and failing with "invalid server ID" /
// an IP parse error. Group dispatchers (server, rdns, auth, power, rescue) instead
// match "help"/"-h"/"--help" in their own subcommand switch; this is the equivalent
// entry point for the leaves that have no such switch.
func helpRequested(args []string, out io.Writer, usage func(io.Writer)) bool {
	if len(args) > 0 && args[0] == "help" {
		usage(out)
		return true
	}
	return false
}

// parsePositionalArgs parses args into fs, accepting flags positioned before or
// after positional arguments. Go's flag.Parse stops at the first non-flag
// argument, so a single call would leave `<positional> --flag` unparsed; this
// peels off one positional at a time and re-parses the remainder. Callers
// register flags on fs beforehand and validate the returned positional count.
// A -h/--help request surfaces as flag.ErrHelp, which the caller should treat
// as a clean exit.
func parsePositionalArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	pending := args
	for {
		if err := fs.Parse(pending); err != nil {
			return nil, err
		}
		pending = fs.Args()
		if len(pending) == 0 {
			return positional, nil
		}
		positional = append(positional, pending[0])
		pending = pending[1:]
	}
}
