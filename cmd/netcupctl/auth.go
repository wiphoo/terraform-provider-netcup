package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

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
  netcupctl auth login   OAuth 2.0 device-authorization login
  netcupctl auth refresh refresh the stored access token
  netcupctl auth help    show this help
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
