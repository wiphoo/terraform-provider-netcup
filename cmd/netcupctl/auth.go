package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// errNotLoggedIn is returned by the token-output commands when no usable access
// token is stored.
var errNotLoggedIn = errors.New("not logged in; run 'netcupctl auth login'")

func cmdAuth(args []string) error {
	if len(args) == 0 {
		usageAuth(os.Stderr)
		return fmt.Errorf("auth requires a subcommand")
	}

	switch args[0] {
	case "login":
		return authLogin(args[1:])
	case "refresh":
		return authRefresh(args[1:])
	case "token":
		return authToken(args[1:], os.Stdout)
	case "export":
		return authExport(args[1:], os.Stdout)
	case "status":
		return authStatus(args[1:], os.Stdout)
	case "help", "-h", "--help":
		usageAuth(os.Stdout)
		return nil
	default:
		usageAuth(os.Stderr)
		return fmt.Errorf("unknown auth subcommand %q", args[0])
	}
}

func usageAuth(w *os.File) {
	fmt.Fprint(w, `netcupctl auth - authenticate with the SCP API

Usage:
  netcupctl auth login    OAuth 2.0 device-authorization login
  netcupctl auth refresh  refresh the stored access token
  netcupctl auth token    print the stored access token (secret)
  netcupctl auth export   print eval-able NETCUP_* export lines (secret)
  netcupctl auth status   show login status (no secrets)
  netcupctl auth help     show this help

Note: 'token' and 'export' print credentials to stdout; redirect with care.
`)
}

// authLogin runs the OAuth 2.0 device-authorization flow and persists the
// resulting tokens to disk with 0600 permissions.
func authLogin(args []string) error {
	fs := flag.NewFlagSet("auth-login", flag.ContinueOnError)
	endpoint := fs.String("oidc-endpoint", "", "override the OIDC endpoint")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if tokenFileExists() {
		fmt.Fprintln(os.Stderr, "Warning: tokens already exist; re-authenticating will overwrite them.")
	}

	var opts []netcup.Option
	if *endpoint != "" {
		opts = append(opts, netcup.WithOIDCEndpoint(*endpoint))
	}

	client := netcup.New(opts...)
	token, err := client.DeviceLogin(context.Background(), os.Stdout)
	if err != nil {
		return err
	}

	if err := saveTokens(token); err != nil {
		return fmt.Errorf("saving tokens: %w", err)
	}

	fmt.Println("Tokens saved.")
	return nil
}

// authRefresh loads the persisted tokens, refreshes the access token, and
// saves the updated tokens back to disk.
func authRefresh(args []string) error {
	fs := flag.NewFlagSet("auth-refresh", flag.ContinueOnError)
	endpoint := fs.String("oidc-endpoint", "", "override the OIDC endpoint")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	token, err := loadTokens()
	if err != nil {
		return fmt.Errorf("loading tokens: %w", err)
	}
	if token == nil || token.RefreshToken == "" {
		return fmt.Errorf("no refresh token found; run 'netcupctl auth login' first")
	}

	var opts []netcup.Option
	if *endpoint != "" {
		opts = append(opts, netcup.WithOIDCEndpoint(*endpoint))
	}

	client := netcup.New(opts...)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	refreshed, err := client.RefreshAccessToken(ctx, token.RefreshToken)
	if err != nil {
		return fmt.Errorf("refreshing token: %w", err)
	}

	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = token.RefreshToken
	}

	if err := saveTokens(refreshed); err != nil {
		return fmt.Errorf("saving refreshed tokens: %w", err)
	}

	fmt.Println("Token refreshed successfully.")
	return nil
}

// parseNoFlags parses a flag set that takes no flags, treating -h/--help as a
// clean (nil) exit to match the other auth subcommands.
func parseNoFlags(name string, args []string) error {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	return nil
}

// authToken prints the stored access token to out, for use in shell
// substitution such as: export NETCUP_ACCESS_TOKEN=$(netcupctl auth token).
func authToken(args []string, out io.Writer) error {
	if err := parseNoFlags("auth-token", args); err != nil {
		return err
	}

	token, err := loadTokens()
	if err != nil {
		return fmt.Errorf("loading tokens: %w", err)
	}
	if token == nil || token.AccessToken == "" {
		return errNotLoggedIn
	}

	fmt.Fprintln(out, token.AccessToken)
	return nil
}

// authExport prints eval-able shell export statements for the stored tokens,
// for use as: eval "$(netcupctl auth export)".
func authExport(args []string, out io.Writer) error {
	if err := parseNoFlags("auth-export", args); err != nil {
		return err
	}

	token, err := loadTokens()
	if err != nil {
		return fmt.Errorf("loading tokens: %w", err)
	}
	if token == nil || token.AccessToken == "" {
		return errNotLoggedIn
	}

	fmt.Fprintf(out, "export NETCUP_ACCESS_TOKEN=%s\n", shellSingleQuote(token.AccessToken))
	if token.RefreshToken != "" {
		fmt.Fprintf(out, "export NETCUP_REFRESH_TOKEN=%s\n", shellSingleQuote(token.RefreshToken))
	}
	return nil
}

// authStatus reports whether the user is logged in, without printing any token
// values.
func authStatus(args []string, out io.Writer) error {
	if err := parseNoFlags("auth-status", args); err != nil {
		return err
	}

	path, _ := tokenFilePath()

	token, err := loadTokens()
	if err != nil {
		return fmt.Errorf("loading tokens: %w", err)
	}
	if token == nil || token.AccessToken == "" {
		fmt.Fprintln(out, "Not logged in. Run 'netcupctl auth login'.")
		return nil
	}

	tokenType := token.TokenType
	if tokenType == "" {
		tokenType = "unknown"
	}
	refresh := "absent"
	if token.RefreshToken != "" {
		refresh = "present"
	}

	fmt.Fprintf(out, "Logged in. tokens: %s (token_type: %s, refresh token: %s)\n", path, tokenType, refresh)
	return nil
}

// shellSingleQuote wraps s in single quotes, safe for POSIX shell eval.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
