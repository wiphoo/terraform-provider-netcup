package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

func TestTokenFilePath_UsesXDG(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	p, err := tokenFilePath()
	if err != nil {
		t.Fatalf("tokenFilePath error = %v", err)
	}

	want := filepath.Join(dir, "netcupctl", "tokens.json")
	if p != want {
		t.Errorf("tokenFilePath = %s, want %s", p, want)
	}
}

func TestTokenFilePath_FallsBackToHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")

	p, err := tokenFilePath()
	if err != nil {
		t.Fatalf("tokenFilePath error = %v", err)
	}

	if p == "" {
		t.Fatal("tokenFilePath returned empty path")
	}
	if !filepath.IsAbs(p) {
		t.Errorf("tokenFilePath = %s, want absolute path", p)
	}
}

func TestSaveAndLoadTokens(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	tr := &netcup.TokenResponse{
		AccessToken:  "test_access",
		RefreshToken: "test_refresh",
		ExpiresIn:    300,
		TokenType:    "Bearer",
	}

	if err := saveTokens(tr); err != nil {
		t.Fatalf("saveTokens error = %v", err)
	}

	loaded, err := loadTokens()
	if err != nil {
		t.Fatalf("loadTokens error = %v", err)
	}
	if loaded == nil {
		t.Fatal("loadTokens returned nil")
	}
	if loaded.AccessToken != "test_access" {
		t.Errorf("AccessToken = %s", loaded.AccessToken)
	}
	if loaded.RefreshToken != "test_refresh" {
		t.Errorf("RefreshToken = %s", loaded.RefreshToken)
	}
}

func TestSaveTokens_FilePerms(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	tr := &netcup.TokenResponse{AccessToken: "tok", TokenType: "Bearer"}
	if err := saveTokens(tr); err != nil {
		t.Fatalf("saveTokens error = %v", err)
	}

	p, _ := tokenFilePath()
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("Stat error = %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Errorf("token file permissions = %o, want 0600", mode)
	}
}

func TestTokenFileExists(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if tokenFileExists() {
		t.Error("tokenFileExists = true before saving")
	}

	tr := &netcup.TokenResponse{AccessToken: "tok", TokenType: "Bearer"}
	if err := saveTokens(tr); err != nil {
		t.Fatalf("saveTokens error = %v", err)
	}

	if !tokenFileExists() {
		t.Error("tokenFileExists = false after saving")
	}
}

func TestLoadTokens_FileNotExist(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	token, err := loadTokens()
	if err != nil {
		t.Fatalf("loadTokens error = %v", err)
	}
	if token != nil {
		t.Fatal("loadTokens should return nil when file does not exist")
	}
}

func TestSaveTokens_DirPerms(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	tr := &netcup.TokenResponse{AccessToken: "tok", TokenType: "Bearer"}
	if err := saveTokens(tr); err != nil {
		t.Fatalf("saveTokens error = %v", err)
	}

	// Verify the parent directory exists with correct perms.
	p, _ := tokenFilePath()
	info, err := os.Stat(filepath.Dir(p))
	if err != nil {
		t.Fatalf("Stat dir error = %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0700 {
		t.Errorf("directory permissions = %o, want 0700", mode)
	}
}

func TestTokenFileDir_MkdirAll(t *testing.T) {
	// Ensure deep parent directories are created.
	dir := filepath.Join(t.TempDir(), "deep", "nested", "config")
	t.Setenv("XDG_CONFIG_HOME", dir)

	tr := &netcup.TokenResponse{AccessToken: "tok", TokenType: "Bearer"}
	if err := saveTokens(tr); err != nil {
		t.Fatalf("saveTokens error = %v", err)
	}

	p, _ := tokenFilePath()
	if _, err := os.Stat(p); err != nil {
		t.Errorf("token file not created: %v", err)
	}
}
