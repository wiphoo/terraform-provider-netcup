package provider

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

func TestRedactSecrets_JSONEscaped(t *testing.T) {
	script := "#!/bin/sh\necho \"s3cret\" \\ <tok>&"
	pw := `pa"ss\word<&>`
	for _, enc := range []func(string) string{
		func(s string) string { b, _ := json.Marshal(s); return string(b) },
		func(s string) string {
			var buf bytes.Buffer
			e := json.NewEncoder(&buf)
			e.SetEscapeHTML(false)
			_ = e.Encode(s)
			return buf.String()
		},
	} {
		body := `{"customScript":` + enc(script) + `,"additionalUserPassword":` + enc(pw) + `}`
		got := redactSecrets("netcup API error: "+body, pw, script)
		for _, leak := range []string{"s3cret", "echo", "pa\\\"ss", "<tok>", "u003c"} {
			if strings.Contains(got, leak) {
				t.Errorf("leaked %q in %q", leak, got)
			}
		}
	}
}

func TestRedactSecrets_OverlappingSecrets(t *testing.T) {
	pw := "hunter2"
	script := "echo hunter2; export API_TOKEN=another-secret"
	got := redactSecrets(`{"customScript":"`+script+`"}`, pw, script)
	if strings.Contains(got, "another-secret") || strings.Contains(got, "hunter2") {
		t.Errorf("leaked overlapping secret: %q", got)
	}
}

func TestRedactSecrets(t *testing.T) {
	err := &netcup.APIError{StatusCode: http.StatusUnprocessableEntity, Status: "422", Body: `{"additionalUserPassword":"hunter2!","customScript":"echo s3cret"}`}
	d, _ := apiErrorToDiag(err, true)
	got := redactSecrets(d.Detail(), "hunter2!", "", "echo s3cret")
	if strings.Contains(got, "hunter2!") || strings.Contains(got, "s3cret") {
		t.Errorf("secret not redacted: %q", got)
	}
	if !strings.Contains(got, "(sensitive)") {
		t.Errorf("want (sensitive) marker: %q", got)
	}
}
