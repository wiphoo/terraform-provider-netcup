package provider

import (
	"net/http"
	"strings"
	"testing"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

func TestWithoutAPIBody_DropsTruncatedSecretEcho(t *testing.T) {
	// A truncated echo of a long script cannot be matched by value redaction;
	// the body must not reach the diagnostic at all.
	body := "export API_TOKEN=super-secret;" + strings.Repeat("x", 4000)
	err := &netcup.APIError{StatusCode: http.StatusUnprocessableEntity, Status: "422 Unprocessable Entity", Body: body}

	got := withoutAPIBody(err)
	d, _ := apiErrorToDiag(got, true)
	if strings.Contains(d.Detail(), "super-secret") || strings.Contains(d.Detail(), "xxx") {
		t.Errorf("body leaked: %q", d.Detail())
	}
	if !strings.Contains(d.Detail(), "422") {
		t.Errorf("status missing: %q", d.Detail())
	}
	if !isDefinitiveReinstallRejection(got) {
		t.Error("classification changed for a 4xx")
	}
	if err.Body != body {
		t.Error("original error was mutated")
	}
}
