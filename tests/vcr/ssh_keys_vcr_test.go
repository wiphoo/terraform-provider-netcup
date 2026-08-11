package vcr

import (
	"context"
	"encoding/base64"
	"os"
	"testing"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// tokenWithFakeUserID builds a JWT-shaped access token whose "id" claim is the
// redacted fakeUserIDValue. The SDK resolves the account id from the token
// LOCALLY (it is not an HTTP call go-vcr can intercept), so a replay-mode
// ssh-key call must carry a token with the same id the cassette was redacted to,
// otherwise it would build /v1/users/<something-else>/ssh-keys and miss the
// recorded /v1/users/10001/ssh-keys entry.
func tokenWithFakeUserID() string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"id":10001,"exp":9999999999}`))
	return "h." + payload + ".s"
}

// TestVCRSSHKeysLifecycle records/replays a create → list → delete cycle for the
// account-scoped SSH-key endpoints. Regenerate with:
//
//	VCR_RECORD=1 NETCUP_ACCESS_TOKEN=... NETCUP_ACC_SSH_PUBLIC_KEY='ssh-ed25519 ...' \
//	  go test ./tests/vcr/ -run TestVCRSSHKeysLifecycle
func TestVCRSSHKeysLifecycle(t *testing.T) {
	recording := os.Getenv("VCR_RECORD") == "1"

	var extra []netcup.Option
	if !recording {
		// Replay: the recorder's fake token is not a JWT. Supply one carrying the
		// redacted account id (record mode uses the real live token from NewClient).
		extra = append(extra, netcup.WithAccessToken(tokenWithFakeUserID()))
	}
	c := NewClient(t, "TestVCRSSHKeysLifecycle", extra...)
	ctx := context.Background()

	pub := os.Getenv("NETCUP_ACC_SSH_PUBLIC_KEY")
	if recording && pub == "" {
		t.Fatal("VCR_RECORD=1 requires NETCUP_ACC_SSH_PUBLIC_KEY")
	}
	if pub == "" {
		pub = "ssh-ed25519 AAAAreplay vcr" // replay: the key content is redacted in the cassette
	}

	created, err := c.CreateSSHKey(ctx, "vcr-ssh-key", pub)
	if err != nil {
		t.Fatalf("CreateSSHKey: %v", err)
	}
	if created.ID == 0 {
		t.Fatalf("created key has zero id")
	}

	keys, err := c.ListSSHKeys(ctx)
	if err != nil {
		t.Fatalf("ListSSHKeys: %v", err)
	}
	found := false
	for _, k := range keys {
		if k.ID == created.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("created key id %d not present in list of %d keys", created.ID, len(keys))
	}

	if err := c.DeleteSSHKey(ctx, created.ID); err != nil {
		t.Fatalf("DeleteSSHKey: %v", err)
	}
}
