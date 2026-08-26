package netcup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
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

// ServerSnapshotCreate is the request body for POST
// /v1/servers/{serverId}/snapshots, mirroring the SCP OpenAPI
// ServerSnapshotCreate schema. Name is required; the other fields are optional
// and omitted from the request when not set.
type ServerSnapshotCreate struct {
	Name           string  `json:"name"`
	Description    *string `json:"description,omitempty"`
	DiskName       *string `json:"diskName,omitempty"`
	OnlineSnapshot bool    `json:"onlineSnapshot,omitempty"`
}

// CreateSnapshot starts an asynchronous snapshot of a server via
// POST /v1/servers/{serverId}/snapshots. A 202 returns the *TaskInfo the caller
// can poll with WaitForTask. Name is required; returns ErrPreDispatch when empty.
//
// An online snapshot (OnlineSnapshot true) captures the running server without
// naming a disk, but the SCP API rejects it with a 400 on UEFI systems
// (server.snapshot.create.error.online.uefi). An offline snapshot (the default)
// must name the target disk via DiskName. Non-2xx surfaces as *APIError.
func (c *Client) CreateSnapshot(ctx context.Context, id int32, opts ServerSnapshotCreate) (*TaskInfo, error) {
	if strings.TrimSpace(opts.Name) == "" {
		return nil, fmt.Errorf("%w: snapshot name is required", ErrPreDispatch)
	}
	encoded, err := json.Marshal(opts)
	if err != nil {
		return nil, err
	}

	req, err := c.newRequest(ctx, http.MethodPost, fmt.Sprintf("/v1/servers/%d/snapshots", id), "application/json", bytes.NewReader(encoded), true)
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

// DeleteSnapshot starts the asynchronous deletion of a snapshot via
// DELETE /v1/servers/{serverId}/snapshots/{name}. DESTRUCTIVE — the snapshot is
// irrecoverably removed. A 202 returns the *TaskInfo the caller can poll with
// WaitForTask. Name is required; returns ErrPreDispatch when empty. Non-2xx
// surfaces as *APIError (404 when the server or snapshot does not exist).
func (c *Client) DeleteSnapshot(ctx context.Context, id int32, name string) (*TaskInfo, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("%w: snapshot name is required", ErrPreDispatch)
	}

	req, err := c.newRequest(ctx, http.MethodDelete, fmt.Sprintf("/v1/servers/%d/snapshots/%s", id, url.PathEscape(name)), "application/json", nil, true)
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

// RestoreSnapshot starts the asynchronous revert of a server from a snapshot via
// POST /v1/servers/{serverId}/snapshots/{name}/revert. DESTRUCTIVE — the
// server's disks are reverted to the snapshot and the server is rebooted; data
// changed since the snapshot is lost. A 202 returns the *TaskInfo the caller can
// poll with WaitForTask. Name is required; returns ErrPreDispatch when empty.
// Non-2xx surfaces as *APIError (404 when the server or snapshot does not exist).
func (c *Client) RestoreSnapshot(ctx context.Context, id int32, name string) (*TaskInfo, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("%w: snapshot name is required", ErrPreDispatch)
	}

	req, err := c.newRequest(ctx, http.MethodPost, fmt.Sprintf("/v1/servers/%d/snapshots/%s/revert", id, url.PathEscape(name)), "application/json", nil, true)
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
