package netcup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ServerImageSetup is the request body for POST /v1/servers/{serverId}/image,
// mirroring the SCP OpenAPI ServerImageSetup schema. All fields are optional;
// pointer/slice fields are omitted from the request when nil/empty so only
// caller-set values are sent.
type ServerImageSetup struct {
	ImageFlavourID            *int32  `json:"imageFlavourId,omitempty"`
	DiskName                  *string `json:"diskName,omitempty"`
	RootPartitionFullDiskSize *bool   `json:"rootPartitionFullDiskSize,omitempty"`
	Hostname                  *string `json:"hostname,omitempty"`
	Locale                    *string `json:"locale,omitempty"`
	Timezone                  *string `json:"timezone,omitempty"`
	AdditionalUserUsername    *string `json:"additionalUserUsername,omitempty"`
	AdditionalUserPassword    *string `json:"additionalUserPassword,omitempty"`
	SSHKeyIDs                 []int32 `json:"sshKeyIds,omitempty"`
	SSHPasswordAuthentication *bool   `json:"sshPasswordAuthentication,omitempty"`
	CustomScript              *string `json:"customScript,omitempty"`
	EmailToExecutingUser      *bool   `json:"emailToExecutingUser,omitempty"`
}

// ReinstallServer starts a native OS (re)install on a server via
// POST /v1/servers/{serverId}/image. DESTRUCTIVE — this wipes the server.
// Asynchronous: a 202 returns the *TaskInfo the caller can poll with
// WaitForTask. Non-2xx (including 422 ValidationError) surfaces as *APIError.
//
// ImageFlavourID is required by the SCP API; returns ErrPreDispatch when nil.
func (c *Client) ReinstallServer(ctx context.Context, id int32, setup ServerImageSetup) (*TaskInfo, error) {
	if setup.ImageFlavourID == nil {
		return nil, fmt.Errorf("%w: imageFlavourId is required", ErrPreDispatch)
	}
	encoded, err := json.Marshal(setup)
	if err != nil {
		return nil, err
	}

	req, err := c.newRequest(ctx, http.MethodPost, fmt.Sprintf("/v1/servers/%d/image", id), "application/json", bytes.NewReader(encoded), true)
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
