package netcup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// PowerState is the desired power state passed to SetPowerState. It maps to the
// SCP API's ServerState1 enum (the write side), which is distinct from the
// live/read state reported by ServerInfo.State (ServerState: RUNNING, SHUTOFF,
// …). Only these three values are accepted by PATCH /v1/servers/{id}.
type PowerState string

// Desired power states accepted by SetPowerState. PowerOff is a soft/ACPI
// shutdown by default; pass the POWEROFF stateOption for a hard poweroff.
const (
	PowerOn        PowerState = "ON"
	PowerOff       PowerState = "OFF"
	PowerSuspended PowerState = "SUSPENDED"
)

// serverStatePatch is the merge-patch body for PATCH /v1/servers/{id}.
type serverStatePatch struct {
	State PowerState `json:"state"`
}

// SetPowerState changes a server's power state via PATCH /v1/servers/{id},
// sending the ServerStatePatch merge-patch body {"state": ...}. When
// stateOption is non-empty it is appended as the ?stateOption= query parameter;
// the SCP API documents these values per target state: ON accepts POWERCYCLE
// and RESET, OFF accepts POWEROFF, and SUSPENDED accepts none. The SDK does not
// validate the combination — the API rejects an unsupported pairing with a
// non-2xx *APIError.
//
// netcup executes power changes asynchronously: a 202 returns the *TaskInfo the
// caller can poll with WaitForTask. A 200 means the change applied synchronously
// with no task to track, and returns (nil, nil). Any non-2xx status (including
// 503 when the node is in maintenance) surfaces as an *APIError.
//
// The request uses Content-Type application/merge-patch+json, as the endpoint
// requires; note this differs from the application/json used elsewhere.
func (c *Client) SetPowerState(ctx context.Context, id int32, state PowerState, stateOption string) (*TaskInfo, error) {
	encoded, err := json.Marshal(serverStatePatch{State: state})
	if err != nil {
		return nil, err
	}

	path := fmt.Sprintf("/v1/servers/%d", id)
	if stateOption != "" {
		path += "?stateOption=" + url.QueryEscape(stateOption)
	}

	req, err := c.newRequest(ctx, http.MethodPatch, path, "application/json", bytes.NewReader(encoded), true)
	if err != nil {
		return nil, err
	}
	// The endpoint accepts only merge-patch; newRequest defaults a JSON body to
	// application/json, so override it here.
	req.Header.Set("Content-Type", "application/merge-patch+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode/100 != 2 {
		return nil, newAPIError(resp)
	}

	// 202 carries a TaskInfo for the async operation; anything else 2xx (200)
	// applied synchronously with no task to track.
	if resp.StatusCode == http.StatusAccepted {
		var task TaskInfo
		if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
			return nil, err
		}
		// Drain any trailing bytes so the connection can be reused (keep-alive).
		_, _ = io.Copy(io.Discard, resp.Body)
		return &task, nil
	}

	// Drain any response body so the connection can be reused (keep-alive).
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil, nil
}
