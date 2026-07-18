package netcup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ImageFlavour is one installable OS image variant returned by
// GET /v1/servers/{serverId}/imageflavours. It is the input identifier for the
// OS-reinstall flows; Alias and Text are human-facing labels. Image points at
// the underlying base image and may be nil.
type ImageFlavour struct {
	ID    int32         `json:"id"`
	Name  string        `json:"name"`
	Alias string        `json:"alias"`
	Text  string        `json:"text"`
	Image *ImageMinimal `json:"image"`
}

// ImageMinimal is the base image object embedded in ImageFlavour.
type ImageMinimal struct {
	ID   int32  `json:"id"`
	Name string `json:"name"`
}

// ListImageFlavours calls GET /v1/servers/{id}/imageflavours and returns the
// image flavours installable on the server. An empty slice is a valid response
// (no error); a non-2xx status surfaces as an *APIError.
func (c *Client) ListImageFlavours(ctx context.Context, id int32) ([]ImageFlavour, error) {
	req, err := c.newRequest(ctx, http.MethodGet, fmt.Sprintf("/v1/servers/%d/imageflavours", id), "application/json", nil, true)
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

	var flavours []ImageFlavour
	if err := json.NewDecoder(resp.Body).Decode(&flavours); err != nil {
		return nil, err
	}
	// Drain any trailing bytes so the connection can be reused (keep-alive).
	_, _ = io.Copy(io.Discard, resp.Body)
	return flavours, nil
}
