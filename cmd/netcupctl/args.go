package main

import "flag"

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
