package netcup

import (
	"encoding/base64"
	"fmt"
	"testing"
	"time"
)

// fakeJWT builds a minimal JWT string (header.payload.signature) with the
// given payload JSON, matching the shape ParseAccessTokenExpiry decodes. The
// signature segment is not validated by ParseAccessTokenExpiry, so it is left
// empty.
func fakeJWT(payloadJSON string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(payloadJSON))
	return header + "." + payload + "."
}

func TestParseAccessTokenExpiry_Valid(t *testing.T) {
	exp := time.Now().Add(5 * time.Minute).Unix()
	token := fakeJWT(fmt.Sprintf(`{"exp":%d,"sub":"user1"}`, exp))

	got, err := ParseAccessTokenExpiry(token)
	if err != nil {
		t.Fatalf("ParseAccessTokenExpiry() error = %v", err)
	}
	if got.Unix() != exp {
		t.Errorf("expiry = %v (unix %d), want unix %d", got, got.Unix(), exp)
	}
}

func TestParseAccessTokenExpiry_NotAJWT(t *testing.T) {
	_, err := ParseAccessTokenExpiry("not-a-jwt-at-all")
	if err == nil {
		t.Fatal("ParseAccessTokenExpiry() error = nil, want error for a non-JWT string")
	}
}

func TestParseAccessTokenExpiry_InvalidBase64Payload(t *testing.T) {
	_, err := ParseAccessTokenExpiry("header.not!valid!base64.sig")
	if err == nil {
		t.Fatal("ParseAccessTokenExpiry() error = nil, want error for invalid base64 payload")
	}
}

func TestParseAccessTokenExpiry_InvalidJSONPayload(t *testing.T) {
	badPayload := base64.RawURLEncoding.EncodeToString([]byte(`{not json`))
	token := "header." + badPayload + ".sig"

	_, err := ParseAccessTokenExpiry(token)
	if err == nil {
		t.Fatal("ParseAccessTokenExpiry() error = nil, want error for invalid JSON payload")
	}
}

func TestParseAccessTokenExpiry_MissingExpClaim(t *testing.T) {
	token := fakeJWT(`{"sub":"user1"}`)

	_, err := ParseAccessTokenExpiry(token)
	if err == nil {
		t.Fatal("ParseAccessTokenExpiry() error = nil, want error for missing exp claim")
	}
}
