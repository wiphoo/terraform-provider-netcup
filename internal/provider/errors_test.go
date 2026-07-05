package provider

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

func TestApiErrorToDiag_Unauthorized(t *testing.T) {
	err := &netcup.APIError{StatusCode: http.StatusUnauthorized, Status: "401 Unauthorized", Body: "no bearer token"}

	d, gone := apiErrorToDiag(err, true)
	if gone {
		t.Fatal("gone = true, want false for a 401")
	}
	if d == nil {
		t.Fatal("diagnostic = nil, want non-nil for a 401")
	}
	if !strings.Contains(d.Detail(), "IP allowlist") {
		t.Errorf("Detail() = %q, want mention of the IP allowlist", d.Detail())
	}
	if !strings.Contains(d.Detail(), "Bearer") {
		t.Errorf("Detail() = %q, want mention of the Bearer token", d.Detail())
	}
}

func TestApiErrorToDiag_Forbidden(t *testing.T) {
	err := &netcup.APIError{StatusCode: http.StatusForbidden, Status: "403 Forbidden"}

	d, gone := apiErrorToDiag(err, true)
	if gone {
		t.Fatal("gone = true, want false for a 403")
	}
	if d == nil || !strings.Contains(d.Detail(), "IP allowlist") {
		t.Errorf("diagnostic should mention the IP allowlist for a 403, got %+v", d)
	}
}

func TestApiErrorToDiag_NotFound_DataSource(t *testing.T) {
	err := &netcup.APIError{StatusCode: http.StatusNotFound, Status: "404 Not Found"}

	d, gone := apiErrorToDiag(err, true)
	if gone {
		t.Fatal("gone = true, want false when notFoundIsError is true (data source)")
	}
	if d == nil {
		t.Fatal("diagnostic = nil, want an error diagnostic for a data source 404")
	}
	if d.Summary() != "netcup object not found" {
		t.Errorf("Summary() = %q, want %q", d.Summary(), "netcup object not found")
	}
}

func TestApiErrorToDiag_NotFound_ResourceRead(t *testing.T) {
	err := &netcup.APIError{StatusCode: http.StatusNotFound, Status: "404 Not Found"}

	d, gone := apiErrorToDiag(err, false)
	if !gone {
		t.Fatal("gone = false, want true when notFoundIsError is false (resource Read)")
	}
	if d != nil {
		t.Errorf("diagnostic = %+v, want nil when the caller should remove the resource from state", d)
	}
}

func TestApiErrorToDiag_GenericAPIError(t *testing.T) {
	err := &netcup.APIError{StatusCode: http.StatusUnprocessableEntity, Status: "422 Unprocessable Entity", Body: "validation error"}

	d, gone := apiErrorToDiag(err, true)
	if gone {
		t.Fatal("gone = true, want false for a generic API error")
	}
	if d == nil || !strings.Contains(d.Detail(), "422") {
		t.Errorf("diagnostic should surface the status code, got %+v", d)
	}
}

func TestApiErrorToDiag_NonAPIError(t *testing.T) {
	err := errors.New("dial tcp: connection refused")

	d, gone := apiErrorToDiag(err, true)
	if gone {
		t.Fatal("gone = true, want false for a non-APIError")
	}
	if d == nil || !strings.Contains(d.Detail(), "connection refused") {
		t.Errorf("diagnostic should fall back to err.Error(), got %+v", d)
	}
}
