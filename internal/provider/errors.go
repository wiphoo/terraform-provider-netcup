package provider

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework/diag"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// apiErrorToDiag maps an error returned by the netcup SDK to a single
// Terraform diagnostic, centralizing the 401/403 IP-allowlist + Bearer token
// hint (see docs/ARCHITECTURE.md and docs/SCP-API-NOTES.md, "Two gates") and
// 404 handling so every data source and resource reports API failures the
// same way.
//
// notFoundIsError controls how a 404 is treated:
//   - true (data sources): a 404 is a hard error; the returned diagnostic
//     should be appended to the caller's diagnostics.
//   - false (resource Read): gone is true and diagnostic is nil, signaling
//     the caller to remove the resource from state instead of erroring.
//
// Any other error (not a *netcup.APIError, e.g. a network failure or a
// TokenSource error) falls back to a generic error diagnostic built from
// err.Error().
func apiErrorToDiag(err error, notFoundIsError bool) (diagnostic diag.Diagnostic, gone bool) {
	var apiErr *netcup.APIError
	if !errors.As(err, &apiErr) {
		return diag.NewErrorDiagnostic("netcup API error", err.Error()), false
	}

	switch apiErr.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return diag.NewErrorDiagnostic(
			"netcup authentication failed",
			fmt.Sprintf(
				"%s\n\nThis is usually one of two gates: the SCP REST API's IP allowlist (configured in the SCP control panel) rejecting this caller's IP, or a missing/expired Bearer access token. Run `netcupctl auth login` or `netcupctl auth refresh` to obtain a fresh token, and confirm the calling IP is allowlisted.",
				apiErr.Error(),
			),
		), false
	case http.StatusNotFound:
		if !notFoundIsError {
			return nil, true
		}
		return diag.NewErrorDiagnostic("netcup object not found", apiErr.Error()), false
	default:
		return diag.NewErrorDiagnostic("netcup API error", apiErr.Error()), false
	}
}
