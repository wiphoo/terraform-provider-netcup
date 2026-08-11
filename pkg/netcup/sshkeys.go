package netcup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// sshKeysPath is the SCP SSH-keys collection endpoint. SSH keys live at the root
// /v1/ssh-keys route (confirmed against the live OpenAPI; see
// docs/SCP-API-NOTES.md and docs/REINSTALL.md), NOT under /v1/users/{userId}.
const sshKeysPath = "/v1/ssh-keys"

// SSHKey is an SSH key registered in the SCP account, as returned by
// GET /v1/ssh-keys.
type SSHKey struct {
	ID        int32  `json:"id"`
	Name      string `json:"name"`
	Key       string `json:"key"`
	CreatedAt string `json:"createdAt,omitempty"`
}

// sshKeyCreateRequest is the JSON body for POST /v1/ssh-keys.
type sshKeyCreateRequest struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

// ListSSHKeys calls GET /v1/ssh-keys and returns the account's registered SSH keys.
func (c *Client) ListSSHKeys(ctx context.Context) ([]SSHKey, error) {
	req, err := c.newRequest(ctx, http.MethodGet, sshKeysPath, "application/json", nil, true)
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
	var keys []SSHKey
	if err := json.NewDecoder(resp.Body).Decode(&keys); err != nil {
		return nil, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return keys, nil
}

// CreateSSHKey calls POST /v1/ssh-keys to register a new SSH key with the given
// name (label) and public-key content, returning the created key.
func (c *Client) CreateSSHKey(ctx context.Context, name, publicKey string) (*SSHKey, error) {
	name = strings.TrimSpace(name)
	publicKey = strings.TrimSpace(publicKey)
	if name == "" || publicKey == "" {
		return nil, fmt.Errorf("%w: ssh key name and public key must not be empty", ErrPreDispatch)
	}
	encoded, err := json.Marshal(sshKeyCreateRequest{Name: name, Key: publicKey})
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(ctx, http.MethodPost, sshKeysPath, "application/json", bytes.NewReader(encoded), true)
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
	var key SSHKey
	if err := json.NewDecoder(resp.Body).Decode(&key); err != nil {
		return nil, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return &key, nil
}

// DeleteSSHKey calls DELETE /v1/ssh-keys/{keyID}. A 2xx (incl. 204) is success;
// any other status surfaces as *APIError.
func (c *Client) DeleteSSHKey(ctx context.Context, keyID int32) error {
	req, err := c.newRequest(ctx, http.MethodDelete, fmt.Sprintf("%s/%d", sshKeysPath, keyID), "application/json", nil, true)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return newAPIError(resp)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}
