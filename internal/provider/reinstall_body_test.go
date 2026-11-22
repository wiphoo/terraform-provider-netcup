package provider

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// A 5xx from the OIDC refresh wraps an APIError in ErrPreDispatch: the reinstall
// was never sent, so it must stay a definitive rejection even though the
// sanitized error no longer carries the sentinel.
func TestPreDispatchClassifiedBeforeBodyStripped(t *testing.T) {
	orig := fmt.Errorf("%w: getting access token: %w", netcup.ErrPreDispatch,
		&netcup.APIError{StatusCode: http.StatusBadGateway, Status: "502 Bad Gateway", Body: "x"})
	if !isDefinitiveReinstallRejection(orig) {
		t.Fatal("original error should be definitive (pre-dispatch)")
	}
	if isDefinitiveReinstallRejection(withoutAPIBody(orig)) {
		t.Fatal("test premise: stripped copy loses the sentinel, so Create must classify before stripping")
	}
}

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
