package netcup

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func tokenWithID(id string) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"id":` + id + `,"exp":9999999999}`))
	return "h." + payload + ".s"
}

func TestListSSHKeys(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/users/12345/ssh-keys" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":7,"name":"k8s","key":"ssh-ed25519 AAAA"}]`))
	}))
	defer srv.Close()
	c := New(WithAPIEndpoint(srv.URL), WithAccessToken(tokenWithID("12345")))
	keys, err := c.ListSSHKeys(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 || keys[0].ID != 7 || keys[0].Name != "k8s" {
		t.Fatalf("unexpected keys: %+v", keys)
	}
}

func TestCreateSSHKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/users/12345/ssh-keys" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var body sshKeyCreateRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Name != "k8s" || body.Key != "ssh-ed25519 AAAA" {
			t.Errorf("unexpected body %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":9,"name":"k8s","key":"ssh-ed25519 AAAA"}`))
	}))
	defer srv.Close()
	c := New(WithAPIEndpoint(srv.URL), WithAccessToken(tokenWithID("12345")))
	key, err := c.CreateSSHKey(context.Background(), "k8s", "ssh-ed25519 AAAA")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key.ID != 9 {
		t.Fatalf("got id %d, want 9", key.ID)
	}
}

func TestEnsureSSHKeyReusesContentMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			t.Errorf("must not POST when a content match exists")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":7,"name":"k8s","key":"ssh-ed25519 AAAA"}]`))
	}))
	defer srv.Close()
	c := New(WithAPIEndpoint(srv.URL), WithAccessToken(tokenWithID("12345")))
	key, err := c.EnsureSSHKey(context.Background(), "k8s", "ssh-ed25519 AAAA")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key.ID != 7 {
		t.Fatalf("got id %d, want reused 7", key.ID)
	}
}
