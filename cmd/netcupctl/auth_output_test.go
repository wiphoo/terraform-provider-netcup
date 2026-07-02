package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// mustSave persists tokens into an isolated XDG config dir for the test.
func mustSave(t *testing.T, tr *netcup.TokenResponse) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := saveTokens(tr); err != nil {
		t.Fatalf("saveTokens error = %v", err)
	}
}

func TestAuthToken_PrintsAccessToken(t *testing.T) {
	mustSave(t, &netcup.TokenResponse{AccessToken: "acc123", RefreshToken: "ref456", TokenType: "Bearer"})

	var buf bytes.Buffer
	if err := authToken(nil, &buf); err != nil {
		t.Fatalf("authToken error = %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "acc123" {
		t.Fatalf("authToken output = %q, want %q", got, "acc123")
	}
}

func TestAuthToken_NotLoggedIn(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var buf bytes.Buffer
	if err := authToken(nil, &buf); err == nil {
		t.Fatal("authToken error = nil, want error when not logged in")
	}
	if buf.Len() != 0 {
		t.Errorf("authToken wrote output on error: %q", buf.String())
	}
}

func TestAuthExport_PrintsBoth(t *testing.T) {
	mustSave(t, &netcup.TokenResponse{AccessToken: "acc", RefreshToken: "ref", TokenType: "Bearer"})

	var buf bytes.Buffer
	if err := authExport(nil, &buf); err != nil {
		t.Fatalf("authExport error = %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "export NETCUP_ACCESS_TOKEN='acc'") {
		t.Errorf("missing access export in %q", out)
	}
	if !strings.Contains(out, "export NETCUP_REFRESH_TOKEN='ref'") {
		t.Errorf("missing refresh export in %q", out)
	}
}

func TestAuthExport_OmitsRefreshWhenAbsent(t *testing.T) {
	mustSave(t, &netcup.TokenResponse{AccessToken: "acc", TokenType: "Bearer"})

	var buf bytes.Buffer
	if err := authExport(nil, &buf); err != nil {
		t.Fatalf("authExport error = %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "NETCUP_ACCESS_TOKEN") {
		t.Errorf("missing access export in %q", out)
	}
	if strings.Contains(out, "NETCUP_REFRESH_TOKEN") {
		t.Errorf("refresh export should be omitted, got %q", out)
	}
}

func TestAuthExport_NotLoggedIn(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var buf bytes.Buffer
	if err := authExport(nil, &buf); err == nil {
		t.Fatal("authExport error = nil, want error when not logged in")
	}
}

func TestAuthStatus_LoggedInNoSecrets(t *testing.T) {
	mustSave(t, &netcup.TokenResponse{AccessToken: "supersecret", RefreshToken: "refsecret", TokenType: "Bearer"})

	var buf bytes.Buffer
	if err := authStatus(nil, &buf); err != nil {
		t.Fatalf("authStatus error = %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Logged in") {
		t.Errorf("want logged-in status, got %q", out)
	}
	if strings.Contains(out, "supersecret") || strings.Contains(out, "refsecret") {
		t.Errorf("status leaked a token value: %q", out)
	}
	if !strings.Contains(out, "refresh token: present") {
		t.Errorf("want refresh-present indicator, got %q", out)
	}
}

func TestAuthStatus_NotLoggedIn(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var buf bytes.Buffer
	if err := authStatus(nil, &buf); err != nil {
		t.Fatalf("authStatus error = %v", err)
	}
	if !strings.Contains(buf.String(), "Not logged in") {
		t.Errorf("want not-logged-in status, got %q", buf.String())
	}
}

func TestShellSingleQuote_EscapesQuotes(t *testing.T) {
	if got := shellSingleQuote("a'b"); got != `'a'\''b'` {
		t.Fatalf("shellSingleQuote = %q, want %q", got, `'a'\''b'`)
	}
}
