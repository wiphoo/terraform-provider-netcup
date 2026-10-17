package netcup

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const (
	testCustomer = "123456"
	testAPIKey   = "key-abc"
	testAPIPass  = "s3cret-pass"
)

// fakeCCP is an in-memory stand-in for the CCP JSON endpoint.
type fakeCCP struct {
	mu         sync.Mutex
	actions    []string
	recs       []map[string]any
	nextID     int
	lastUpdate []map[string]any
	lastZone   json.RawMessage
	dnssec     bool
}

func (f *fakeCCP) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.actions...)
}

func (f *fakeCCP) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Action string                     `json:"action"`
			Param  map[string]json.RawMessage `json:"param"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.actions = append(f.actions, req.Action)

		str := func(k string) string {
			var s string
			_ = json.Unmarshal(req.Param[k], &s)
			return s
		}
		reply := func(status string, code int, short string, data any) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": status, "statuscode": code, "shortmessage": short, "responsedata": data,
			})
		}

		if req.Action == "login" {
			if str("customernumber") != testCustomer || str("apikey") != testAPIKey || str("apipassword") != testAPIPass {
				reply("error", 4013, "Api key or password invalid", "")
				return
			}
			reply("success", 2000, "Login successful", map[string]string{"apisessionid": "sess1"})
			return
		}
		if str("apisessionid") != "sess1" {
			reply("error", 4001, "Session invalid", "")
			return
		}

		switch req.Action {
		case "logout":
			reply("success", 2000, "Logout successful", "")
		case "infoDnsZone":
			reply("success", 2000, "ok", map[string]any{
				"name": "example.com", "ttl": "86400", "serial": "2026101701",
				"refresh": "28800", "retry": "7200", "expire": "1209600", "dnssecstatus": f.dnssec,
			})
		case "updateDnsZone":
			f.lastZone = req.Param["dnszone"]
			reply("success", 2000, "ok", map[string]any{
				"name": "example.com", "ttl": "3600", "serial": "2026101702",
				"refresh": "28800", "retry": "7200", "expire": "1209600", "dnssecstatus": false,
			})
		case "infoDnsRecords":
			reply("success", 2000, "ok", map[string]any{"dnsrecords": f.recs})
		case "updateDnsRecords":
			var set struct {
				DNSRecords []map[string]any `json:"dnsrecords"`
			}
			_ = json.Unmarshal(req.Param["dnsrecordset"], &set)
			f.lastUpdate = nil
			for _, in := range set.DNSRecords {
				cp := make(map[string]any, len(in))
				for k, v := range in {
					cp[k] = v
				}
				f.lastUpdate = append(f.lastUpdate, cp)
			}
			for _, in := range set.DNSRecords {
				id, _ := in["id"].(string)
				if del, _ := in["deleterecord"].(bool); del {
					kept := f.recs[:0:0]
					for _, e := range f.recs {
						if e["id"] != id {
							kept = append(kept, e)
						}
					}
					f.recs = kept
					continue
				}
				in["state"] = "yes"
				if id == "" {
					f.nextID++
					in["id"] = strconv.Itoa(f.nextID)
					f.recs = append(f.recs, in)
					continue
				}
				for i, e := range f.recs {
					if e["id"] == id {
						f.recs[i] = in
					}
				}
			}
			reply("success", 2000, "ok", map[string]any{"dnsrecords": f.recs})
		default:
			reply("error", 5000, "unknown action "+req.Action, "")
		}
	}
}

func newTestDNS(t *testing.T) (*DNSClient, *fakeCCP) {
	t.Helper()
	f := &fakeCCP{nextID: 10, recs: []map[string]any{
		{"id": "1", "hostname": "@", "type": "A", "priority": "0", "destination": "203.0.113.10", "state": "yes"},
		{"id": "2", "hostname": "@", "type": "MX", "priority": "10", "destination": "mail.example.com", "state": "yes"},
	}}
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	c, err := NewDNSClient(
		DNSCredentials{CustomerNumber: testCustomer, APIKey: testAPIKey, APIPassword: testAPIPass},
		WithDNSEndpoint(srv.URL),
	)
	if err != nil {
		t.Fatalf("NewDNSClient: %v", err)
	}
	return c, f
}

func TestNewDNSClient_MissingCredentials(t *testing.T) {
	_, err := NewDNSClient(DNSCredentials{CustomerNumber: "1"})
	if err == nil || !strings.Contains(err.Error(), "NETCUP_DNS_API_KEY") || !strings.Contains(err.Error(), "NETCUP_DNS_API_PASSWORD") {
		t.Fatalf("error = %v, want mention of the missing env vars", err)
	}
}

func TestDNSCredentialsFromEnv(t *testing.T) {
	t.Setenv("NETCUP_CUSTOMER_NUMBER", "42")
	t.Setenv("NETCUP_DNS_API_KEY", "k")
	t.Setenv("NETCUP_DNS_API_PASSWORD", "p")
	got, err := DNSCredentialsFromEnv()
	if err != nil {
		t.Fatalf("DNSCredentialsFromEnv: %v", err)
	}
	if got != (DNSCredentials{CustomerNumber: "42", APIKey: "k", APIPassword: "p"}) {
		t.Errorf("creds = %+v", got)
	}
}

func TestDNS_LoginOncePerSessionAndLogout(t *testing.T) {
	c, f := newTestDNS(t)
	ctx := context.Background()
	if _, err := c.ListRecords(ctx, "example.com"); err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if _, err := c.ListRecords(ctx, "example.com"); err != nil {
		t.Fatalf("ListRecords #2: %v", err)
	}
	if err := c.Logout(ctx); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if err := c.Logout(ctx); err != nil { // no session: no-op
		t.Fatalf("Logout #2: %v", err)
	}
	want := "login,infoDnsRecords,infoDnsRecords,logout"
	if got := strings.Join(f.calls(), ","); got != want {
		t.Errorf("calls = %q, want %q", got, want)
	}
}

func TestDNS_LoginFailureDoesNotLeakSecrets(t *testing.T) {
	f := &fakeCCP{}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	c, err := NewDNSClient(
		DNSCredentials{CustomerNumber: testCustomer, APIKey: testAPIKey, APIPassword: "wrong"},
		WithDNSEndpoint(srv.URL),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.ListRecords(context.Background(), "example.com")
	var dnsErr *DNSError
	if !errors.As(err, &dnsErr) {
		t.Fatalf("error = %v, want *DNSError", err)
	}
	if dnsErr.StatusCode != 4013 {
		t.Errorf("StatusCode = %d, want 4013", dnsErr.StatusCode)
	}
	for _, secret := range []string{"wrong", testAPIKey} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error leaks %q: %v", secret, err)
		}
	}
}

func TestListRecords_Priorities(t *testing.T) {
	c, _ := newTestDNS(t)
	recs, err := c.ListRecords(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("len = %d, want 2", len(recs))
	}
	if recs[0].Type != "A" || recs[0].Priority != 0 || recs[1].Type != "MX" || recs[1].Priority != 10 {
		t.Errorf("records = %+v", recs)
	}
}

func TestNormalizeDomain(t *testing.T) {
	got, err := normalizeDomain(" Example.COM. ")
	if err != nil || got != "example.com" {
		t.Errorf("got %q, %v; want example.com", got, err)
	}
	if _, err := normalizeDomain(""); err == nil {
		t.Error("empty domain: error = nil")
	}
}

func TestFlexInt(t *testing.T) {
	tests := []struct {
		in      string
		want    flexInt
		wantErr bool
	}{
		{`"86400"`, 86400, false},
		{`86400`, 86400, false},
		{`null`, 0, false},
		{`""`, 0, false},
		{`"abc"`, 0, true},
	}
	for _, tt := range tests {
		var f flexInt
		err := json.Unmarshal([]byte(tt.in), &f)
		if (err != nil) != tt.wantErr || f != tt.want {
			t.Errorf("Unmarshal(%s) = %d, %v; want %d, err=%v", tt.in, f, err, tt.want, tt.wantErr)
		}
	}
	b, err := json.Marshal(flexInt(86400))
	if err != nil || string(b) != `"86400"` {
		t.Errorf("Marshal = %s, %v; want \"86400\"", b, err)
	}
}

func TestDNS_NonSuccessHTTPIsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	c, err := NewDNSClient(DNSCredentials{CustomerNumber: "1", APIKey: "k", APIPassword: "p"}, WithDNSEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.ListRecords(context.Background(), "example.com")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *APIError", err)
	}
}

func TestDNS_EmptyStringResponseDataIsNotDecoded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","statuscode":2000,"responsedata":""}`))
	}))
	defer srv.Close()
	c, err := NewDNSClient(DNSCredentials{CustomerNumber: "1", APIKey: "k", APIPassword: "p"}, WithDNSEndpoint(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	var out struct{ Name string }
	if err := c.do(context.Background(), "x", nil, &out); err != nil {
		t.Fatalf("do with \"\" responsedata: %v", err)
	}
}

func TestNewDNSClient_EndpointValidation(t *testing.T) {
	creds := DNSCredentials{CustomerNumber: "1", APIKey: "k", APIPassword: "p"}
	rejects := []string{
		"http://example.com/x", "http://localhost.evil.com", "http://127.0.0.1@evil.com",
		"ftp://x", "https:///x", "https://user:pw@host",
	}
	for _, ep := range rejects {
		_, err := NewDNSClient(creds, WithDNSEndpoint(ep))
		if err == nil {
			t.Errorf("%s: error = nil, want rejection", ep)
		} else if strings.Contains(err.Error(), "pw") {
			t.Errorf("%s: error leaks credentials: %v", ep, err)
		}
	}
	accepts := []string{
		"http://127.0.0.1:1234", "http://localhost:80", "HTTP://LOCALHOST", "http://[::1]:80", "https://ccp.example.com/x",
	}
	for _, ep := range accepts {
		if _, err := NewDNSClient(creds, WithDNSEndpoint(ep)); err != nil {
			t.Errorf("%s: error = %v, want nil", ep, err)
		}
	}
	t.Run("env", func(t *testing.T) {
		t.Setenv("NETCUP_DNS_ENDPOINT", "http://example.com/x")
		if _, err := NewDNSClient(creds); err == nil {
			t.Error("env http remote: error = nil, want rejection")
		}
		t.Setenv("NETCUP_DNS_ENDPOINT", "http://127.0.0.1:1234")
		if _, err := NewDNSClient(creds); err != nil {
			t.Errorf("env loopback: %v", err)
		}
	})
}

func TestDNS_DoesNotFollowRedirects(t *testing.T) {
	var hits int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits++ }))
	defer target.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redir.Close()
	c, err := NewDNSClient(DNSCredentials{CustomerNumber: "1", APIKey: "k", APIPassword: "p"}, WithDNSEndpoint(redir.URL))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.ListRecords(context.Background(), "example.com")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *APIError", err)
	}
	if hits != 0 {
		t.Errorf("redirect target hit %d times, want 0", hits)
	}
}

func TestGetZone(t *testing.T) {
	c, _ := newTestDNS(t)
	z, err := c.GetZone(context.Background(), " Example.COM. ")
	if err != nil {
		t.Fatalf("GetZone: %v", err)
	}
	want := DNSZone{Name: "example.com", TTL: 86400, Serial: 2026101701, Refresh: 28800, Retry: 7200, Expire: 1209600}
	if *z != want {
		t.Errorf("zone = %+v, want %+v", *z, want)
	}
}

func TestUpdateZone_SendsStringsAndValidates(t *testing.T) {
	c, f := newTestDNS(t)
	ctx := context.Background()

	for _, field := range []string{"ttl", "refresh", "retry", "expire"} {
		z := DNSZone{Name: "example.com", TTL: 1, Refresh: 1, Retry: 1, Expire: 1}
		switch field {
		case "ttl":
			z.TTL = 0
		case "refresh":
			z.Refresh = 0
		case "retry":
			z.Retry = 0
		case "expire":
			z.Expire = -1
		}
		if _, err := c.UpdateZone(ctx, z); err == nil || !strings.Contains(err.Error(), "zone "+field+" must be positive") {
			t.Fatalf("UpdateZone %s invalid: error = %v, want %q", field, err, "zone "+field+" must be positive")
		}
	}
	if got := f.calls(); len(got) != 0 {
		t.Fatalf("validation failure must not hit the network, calls = %v", got)
	}

	z, err := c.UpdateZone(ctx, DNSZone{Name: "example.com", TTL: 3600, Serial: 2026101701, Refresh: 28800, Retry: 7200, Expire: 1209600})
	if err != nil {
		t.Fatalf("UpdateZone: %v", err)
	}
	if z.TTL != 3600 {
		t.Errorf("TTL = %d, want 3600", z.TTL)
	}
	if !strings.Contains(string(f.lastZone), `"ttl":"3600"`) {
		t.Errorf("zone body = %s, want ttl sent as a string", f.lastZone)
	}
}

func TestGetZone_DNSSEC(t *testing.T) {
	c, f := newTestDNS(t)
	f.dnssec = true
	z, err := c.GetZone(context.Background(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !z.DNSSEC {
		t.Error("DNSSEC = false, want true")
	}
}

func TestValidateRecord(t *testing.T) {
	tests := []struct {
		name    string
		rec     DNSRecord
		wantErr string
	}{
		{"ok A", DNSRecord{Hostname: "www", Type: "a", Destination: "203.0.113.5"}, ""},
		{"ok wildcard", DNSRecord{Hostname: "*", Type: "CNAME", Destination: "example.com."}, ""},
		{"empty hostname", DNSRecord{Type: "A", Destination: "1.2.3.4"}, "hostname"},
		{"bad type", DNSRecord{Hostname: "@", Type: "BOGUS", Destination: "x"}, "type"},
		{"empty destination", DNSRecord{Hostname: "@", Type: "A"}, "destination"},
		{"negative priority", DNSRecord{Hostname: "@", Type: "MX", Destination: "m", Priority: -1}, "priority"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRecord(&tt.rec)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("error = %v, want nil", err)
				}
				if tt.rec.Type != strings.ToUpper(tt.rec.Type) {
					t.Errorf("Type not uppercased: %q", tt.rec.Type)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestCreateRecord(t *testing.T) {
	c, f := newTestDNS(t)
	ctx := context.Background()

	got, err := c.CreateRecord(ctx, "example.com", DNSRecord{Hostname: "www", Type: "a", Destination: "203.0.113.5"})
	if err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}
	if got.ID != "11" || got.Hostname != "www" || got.Type != "A" || got.Destination != "203.0.113.5" {
		t.Errorf("record = %+v", got)
	}
	if len(f.lastUpdate) != 1 || f.lastUpdate[0]["id"] != "" {
		t.Errorf("update body = %v, want one record with empty id", f.lastUpdate)
	}
}

func TestCreateRecord_RefusesExactDuplicate(t *testing.T) {
	c, f := newTestDNS(t)
	_, err := c.CreateRecord(context.Background(), "example.com",
		DNSRecord{Hostname: "@", Type: "A", Destination: "203.0.113.10"})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %v, want already-exists", err)
	}
	for _, a := range f.calls() {
		if a == "updateDnsRecords" {
			t.Error("duplicate create must not send updateDnsRecords")
		}
	}
}

func TestUpdateRecord(t *testing.T) {
	c, _ := newTestDNS(t)
	got, err := c.UpdateRecord(context.Background(), "example.com",
		DNSRecord{ID: "2", Hostname: "@", Type: "MX", Priority: 20, Destination: "mail2.example.com"})
	if err != nil {
		t.Fatalf("UpdateRecord: %v", err)
	}
	if got.Priority != 20 || got.Destination != "mail2.example.com" {
		t.Errorf("record = %+v", got)
	}
	if _, err := c.UpdateRecord(context.Background(), "example.com",
		DNSRecord{ID: "999", Hostname: "@", Type: "A", Destination: "1.2.3.4"}); err == nil {
		t.Error("UpdateRecord of unknown id: error = nil")
	}
}

func TestDeleteRecord(t *testing.T) {
	c, f := newTestDNS(t)
	ctx := context.Background()
	if err := c.DeleteRecord(ctx, "example.com", "1"); err != nil {
		t.Fatalf("DeleteRecord: %v", err)
	}
	if del, _ := f.lastUpdate[0]["deleterecord"].(bool); !del || f.lastUpdate[0]["hostname"] != "@" {
		t.Errorf("update body = %v, want full record with deleterecord=true", f.lastUpdate)
	}
	recs, _ := c.ListRecords(ctx, "example.com")
	if len(recs) != 1 || recs[0].ID != "2" {
		t.Errorf("records after delete = %+v", recs)
	}
	if err := c.DeleteRecord(ctx, "example.com", "1"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("second delete error = %v, want not-found", err)
	}
}
