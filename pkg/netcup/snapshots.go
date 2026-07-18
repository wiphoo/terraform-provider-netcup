package netcup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// SnapshotMinimal is the per-snapshot projection returned by
// GET /v1/servers/{serverId}/snapshots. It mirrors the SCP OpenAPI
// SnapshotMinimal schema. Description and ExportedSizeInKiB are pointers because
// the API returns them as nullable; Disks may be empty.
type SnapshotMinimal struct {
	UUID              string    `json:"uuid"`
	Name              string    `json:"name"`
	Description       *string   `json:"description"`
	Disks             []string  `json:"disks"`
	CreationTime      time.Time `json:"creationTime"`
	State             string    `json:"state"`
	Online            bool      `json:"online"`
	Exported          bool      `json:"exported"`
	ExportedSizeInKiB *int64    `json:"exportedSizeInKiB"`
}

// ListSnapshots calls GET /v1/servers/{id}/snapshots and returns the server's
// snapshots. An empty slice is a valid response (no error). A non-2xx status
// surfaces as an *APIError.
func (c *Client) ListSnapshots(ctx context.Context, id int32) ([]SnapshotMinimal, error) {
	req, err := c.newRequest(ctx, http.MethodGet, fmt.Sprintf("/v1/servers/%d/snapshots", id), "application/json", nil, true)
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

	var snapshots []SnapshotMinimal
	if err := json.NewDecoder(resp.Body).Decode(&snapshots); err != nil {
		return nil, err
	}
	// Drain any trailing bytes so the connection can be reused (keep-alive).
	_, _ = io.Copy(io.Discard, resp.Body)
	return snapshots, nil
}
