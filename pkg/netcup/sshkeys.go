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

// SSHKey is an SSH key registered in the SCP account, as returned by
// GET /v1/users/{userId}/ssh-keys.
type SSHKey struct {
	ID        int32  `json:"id"`
	Name      string `json:"name"`
	Key       string `json:"key"`
	CreatedAt string `json:"createdAt,omitempty"`
}

// sshKeyCreateRequest is the JSON body for POST /v1/users/{userId}/ssh-keys.
type sshKeyCreateRequest struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

// ResolveUserID derives the numeric SCP account id from the current access
// token's "id" claim. The SCP REST API namespaces SSH keys under the account id
// (/v1/users/{userId}/ssh-keys) and offers no /users/me alias, so the id must be
// read from the token. NOTE: the root /v1/ssh-keys route returns 404 — verified
// against the live API — so the account-scoped path is required.
func (c *Client) ResolveUserID(ctx context.Context) (string, error) {
	token, err := c.bearerToken(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: getting access token: %w", ErrPreDispatch, err)
	}
	if token == "" {
		return "", fmt.Errorf("%w: no access token available to resolve user id", ErrPreDispatch)
	}
	userID, err := ParseAccessTokenUserID(token)
	if err != nil {
		// A malformed token / missing "id" claim fails before any request is built,
		// so mark it pre-dispatch (like the failures above). Otherwise callers such
		// as sshKeyResource.Create would misclassify this definitive
		// token/configuration error as an ambiguous, possibly-dispatched one.
		return "", fmt.Errorf("%w: resolving account id from access token: %w", ErrPreDispatch, err)
	}
	return userID, nil
}

// sshKeysPath builds /v1/users/{userId}/ssh-keys for the authenticated account.
func (c *Client) sshKeysPath(ctx context.Context) (string, error) {
	userID, err := c.ResolveUserID(ctx)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("/v1/users/%s/ssh-keys", userID), nil
}

// ListSSHKeys calls GET /v1/users/{userId}/ssh-keys and returns the account's
// registered SSH keys.
func (c *Client) ListSSHKeys(ctx context.Context) ([]SSHKey, error) {
	path, err := c.sshKeysPath(ctx)
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(ctx, http.MethodGet, path, "application/json", nil, true)
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

// CreateSSHKey calls POST /v1/users/{userId}/ssh-keys to register a new SSH key
// with the given name (label) and public-key content, returning the created key.
func (c *Client) CreateSSHKey(ctx context.Context, name, publicKey string) (*SSHKey, error) {
	name = strings.TrimSpace(name)
	publicKey = strings.TrimSpace(publicKey)
	if name == "" || publicKey == "" {
		return nil, fmt.Errorf("%w: ssh key name and public key must not be empty", ErrPreDispatch)
	}
	path, err := c.sshKeysPath(ctx)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(sshKeyCreateRequest{Name: name, Key: publicKey})
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(ctx, http.MethodPost, path, "application/json", bytes.NewReader(encoded), true)
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

// DeleteSSHKey calls DELETE /v1/users/{userId}/ssh-keys/{keyID}. A 2xx (incl.
// 204) is success; any other status surfaces as *APIError.
func (c *Client) DeleteSSHKey(ctx context.Context, keyID int32) error {
	path, err := c.sshKeysPath(ctx)
	if err != nil {
		return err
	}
	req, err := c.newRequest(ctx, http.MethodDelete, fmt.Sprintf("%s/%d", path, keyID), "application/json", nil, true)
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
