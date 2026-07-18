package netcup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// RescueSystemStatus is the response from GET /v1/servers/{serverId}/rescuesystem,
// mirroring the SCP OpenAPI RescueSystemStatus schema. Password is a pointer
// because the API returns it as nullable: it is populated only while the rescue
// system is active, and even then netcup may surface it on a follow-up read
// shortly after activation completes.
type RescueSystemStatus struct {
	Active   bool    `json:"active"`
	Password *string `json:"password"`
}

// GetRescueSystem calls GET /v1/servers/{id}/rescuesystem and returns the
// server's rescue-system status (whether it is active and, when active, the
// rescue password). A non-2xx status (e.g. 404 for an unknown server) surfaces
// as an *APIError.
func (c *Client) GetRescueSystem(ctx context.Context, id int32) (*RescueSystemStatus, error) {
	req, err := c.newRequest(ctx, http.MethodGet, fmt.Sprintf("/v1/servers/%d/rescuesystem", id), "application/json", nil, true)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode/100 != 2 {
		return nil, newAPIError(resp)
	}

	var status RescueSystemStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, err
	}
	// Drain any trailing bytes so the connection can be reused (keep-alive).
	_, _ = io.Copy(io.Discard, resp.Body)
	return &status, nil
}

// EnableRescueSystem activates the rescue system for a server via
// POST /v1/servers/{id}/rescuesystem. The operation takes no request body
// (verified against the live SCP OpenAPI) and is asynchronous: it returns a
// 202 with the *TaskInfo the caller can poll with WaitForTask. Once the task
// finishes, the rescue password is surfaced on a follow-up GetRescueSystem.
//
// Enabling reboots the server into the rescue environment. A non-2xx status
// surfaces as an *APIError — notably 400 when the rescue system is already
// active.
func (c *Client) EnableRescueSystem(ctx context.Context, id int32) (*TaskInfo, error) {
	req, err := c.newRequest(ctx, http.MethodPost, fmt.Sprintf("/v1/servers/%d/rescuesystem", id), "application/json", nil, true)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode/100 != 2 {
		return nil, newAPIError(resp)
	}

	var task TaskInfo
	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		return nil, err
	}
	// Drain any trailing bytes so the connection can be reused (keep-alive).
	_, _ = io.Copy(io.Discard, resp.Body)
	return &task, nil
}

// DisableRescueSystem deactivates the rescue system for a server via
// DELETE /v1/servers/{id}/rescuesystem. Like activation, deactivation is
// asynchronous: the live SCP API returns a 202 with the *TaskInfo to poll with
// WaitForTask (this deviates from the endpoint's documented shape, which
// implied a bodyless delete — verified against the live OpenAPI and noted in
// docs/SCP-API-NOTES.md). Disabling reboots the server back into its normal
// operating system.
//
// A non-2xx status surfaces as an *APIError — notably 400 when the rescue
// system is already deactivated.
func (c *Client) DisableRescueSystem(ctx context.Context, id int32) (*TaskInfo, error) {
	req, err := c.newRequest(ctx, http.MethodDelete, fmt.Sprintf("/v1/servers/%d/rescuesystem", id), "application/json", nil, true)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode/100 != 2 {
		return nil, newAPIError(resp)
	}

	var task TaskInfo
	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		return nil, err
	}
	// Drain any trailing bytes so the connection can be reused (keep-alive).
	_, _ = io.Copy(io.Discard, resp.Body)
	return &task, nil
}
