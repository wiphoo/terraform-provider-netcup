package netcup

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

// maxErrorBody bounds how much of an error response body is read into an
// APIError, to avoid unbounded memory use on large/hostile responses.
const maxErrorBody = 4 << 10 // 4 KiB

// APIError describes a non-2xx response from the netcup SCP REST API.
type APIError struct {
	// StatusCode is the HTTP status code (e.g. 401).
	StatusCode int
	// Status is the HTTP status line (e.g. "401 Unauthorized").
	Status string
	// Body is the (truncated) response body, when present.
	Body string
}

// Error implements the error interface with an actionable message.
func (e *APIError) Error() string {
	msg := "netcup API error: " + e.Status
	if body := strings.TrimSpace(e.Body); body != "" {
		msg += ": " + body
	}
	if e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden {
		msg += " (check the access token and that your IP is allowed in the SCP REST API settings)"
	}
	return msg
}

// newAPIError builds an APIError from a response. The caller retains ownership
// of resp.Body and is responsible for closing it.
func newAPIError(resp *http.Response) *APIError {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	status := resp.Status
	if status == "" {
		status = fmt.Sprintf("%d", resp.StatusCode)
	}
	return &APIError{
		StatusCode: resp.StatusCode,
		Status:     status,
		Body:       string(body),
	}
}
