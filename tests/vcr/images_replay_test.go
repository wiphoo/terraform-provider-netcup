package vcr

import (
	"context"
	"testing"
)

// TestListImageFlavours replays GET /v1/servers/{id}/imageflavours and asserts
// the flavour list decodes, including the nested (nullable) image. Read-only,
// so it is live-refreshable with VCR_RECORD=1. The human-facing `name` is
// redacted by the generic "name" rule, so assertions use the stable `alias`,
// `text`, and `id` fields instead.
func TestListImageFlavours(t *testing.T) {
	const cassetteName = "TestListImageFlavours"
	client := NewClient(t, cassetteName)
	id := ServerIDForTest(t, cassetteName)

	flavours, err := client.ListImageFlavours(context.Background(), id)
	if err != nil {
		t.Fatalf("ListImageFlavours() error = %v", err)
	}
	if len(flavours) == 0 {
		t.Fatal("ListImageFlavours() returned no flavours, want at least one")
	}

	// Assertions are position-independent so a live VCR_RECORD=1 refresh of this
	// read-only cassette (which returns the real OS catalog in its own order,
	// with `image` nullable per the OpenAPI) doesn't fail on an artifact of the
	// authored fixture. Required fields (id, alias, text) must hold for every
	// flavour; the nested `image` must decode to a real object or a nil pointer
	// (never a fabricated zero struct), and at least one flavour must carry it.
	sawImage := false
	for _, f := range flavours {
		if f.ID == 0 {
			t.Errorf("flavour %+v has a zero ID, want a non-zero image flavour id", f)
		}
		if f.Alias == "" || f.Text == "" {
			t.Errorf("flavour %d = %+v, want non-empty Alias and Text", f.ID, f)
		}
		if f.Image != nil {
			sawImage = true
			if f.Image.ID == 0 {
				t.Errorf("flavour %d has a non-nil Image with a zero ID", f.ID)
			}
		}
	}
	if !sawImage {
		t.Error("no flavour carried a decoded nested image; want at least one")
	}
}
