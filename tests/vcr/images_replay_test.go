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

	first := flavours[0]
	if first.ID == 0 {
		t.Errorf("flavours[0].ID = 0, want a non-zero image flavour id")
	}
	if first.Alias == "" || first.Text == "" {
		t.Errorf("flavours[0] = %+v, want non-empty Alias and Text", first)
	}
	if first.Image == nil || first.Image.ID == 0 {
		t.Errorf("flavours[0].Image = %+v, want a decoded nested image with an id", first.Image)
	}

	// The last recorded flavour has a null image — the nullable field must
	// decode to a nil pointer, not panic or fabricate a zero image.
	if last := flavours[len(flavours)-1]; last.Image != nil {
		t.Errorf("flavours[last].Image = %+v, want nil for a null image", last.Image)
	}
}
