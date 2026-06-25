package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

const tokensDir = "netcupctl"
const tokensFile = "tokens.json"

// tokenFilePath returns the path to the token file. It uses $XDG_CONFIG_HOME
// when set, falling back to ~/.config on Unix and %APPDATA% on Windows.
func tokenFilePath() (string, error) {
	var dir string
	switch runtime.GOOS {
	case "windows":
		dir = os.Getenv("APPDATA")
		if dir == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			dir = filepath.Join(home, "AppData", "Roaming")
		}
	default:
		dir = os.Getenv("XDG_CONFIG_HOME")
		if dir == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			dir = filepath.Join(home, ".config")
		}
	}

	p := filepath.Join(dir, tokensDir, tokensFile)
	return p, nil
}

// tokenFileExists reports whether the token file exists on disk.
func tokenFileExists() bool {
	p, err := tokenFilePath()
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// loadTokens reads the persisted token file. It returns a nil pointer without
// error when the file does not exist.
func loadTokens() (*netcup.TokenResponse, error) {
	p, err := tokenFilePath()
	if err != nil {
		return nil, err
	}

	f, err := os.Open(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var tr netcup.TokenResponse
	if err := json.NewDecoder(f).Decode(&tr); err != nil {
		return nil, err
	}
	return &tr, nil
}

// saveTokens persists the token response to the token file with 0600
// permissions. The parent directory is created if necessary.
func saveTokens(tr *netcup.TokenResponse) error {
	p, err := tokenFilePath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}

	f, err := os.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(tr)
}
