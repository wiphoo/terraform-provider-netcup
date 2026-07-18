package netcup

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListImageFlavoursSuccess(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/servers/123/imageflavours" {
			t.Errorf("path = %q, want /v1/servers/123/imageflavours", r.URL.Path)
		}
		if v := r.Header.Get("Accept"); v != "application/json" {
			t.Errorf("Accept = %q, want application/json", v)
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[
			{"id":1,"name":"ubuntu-2404","alias":"Ubuntu 24.04","text":"Ubuntu 24.04 LTS","image":{"id":10,"name":"Ubuntu"}},
			{"id":2,"name":"debian-12","alias":"Debian 12","text":"Debian 12 Bookworm","image":null}
		]`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	flavours, err := c.ListImageFlavours(context.Background(), 123)
	if err != nil {
		t.Fatalf("ListImageFlavours() error = %v", err)
	}
	if len(flavours) != 2 {
		t.Fatalf("len(flavours) = %d, want 2", len(flavours))
	}
	if flavours[0].ID != 1 || flavours[0].Name != "ubuntu-2404" || flavours[0].Alias != "Ubuntu 24.04" {
		t.Errorf("flavours[0] = %+v, want ID=1 Name=ubuntu-2404 Alias=Ubuntu 24.04", flavours[0])
	}
	if flavours[0].Image == nil || flavours[0].Image.Name != "Ubuntu" || flavours[0].Image.ID != 10 {
		t.Errorf("flavours[0].Image = %+v, want {ID:10 Name:Ubuntu}", flavours[0].Image)
	}
	if flavours[1].Image != nil {
		t.Errorf("flavours[1].Image = %+v, want nil", flavours[1].Image)
	}
	if want := "Bearer tok123"; gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
}

func TestListImageFlavoursEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	flavours, err := c.ListImageFlavours(context.Background(), 123)
	if err != nil {
		t.Fatalf("ListImageFlavours() error = %v, want nil (empty list is valid)", err)
	}
	if len(flavours) != 0 {
		t.Errorf("len(flavours) = %d, want 0", len(flavours))
	}
}

func TestListImageFlavoursNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"server not found"}`))
	}))
	defer srv.Close()

	c := New(WithAPIEndpoint(srv.URL), WithAccessToken("tok123"))
	_, err := c.ListImageFlavours(context.Background(), 999)
	if err == nil {
		t.Fatal("ListImageFlavours() error = nil, want error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error type = %T, want *netcup.APIError", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusNotFound)
	}
}
