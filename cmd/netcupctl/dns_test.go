package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// startFakeCCP serves a minimal CCP endpoint with one A record and points the
// DNS env vars at it.
func startFakeCCP(t *testing.T) {
	t.Helper()
	recs := []map[string]any{
		{"id": "1", "hostname": "@", "type": "A", "priority": "0", "destination": "203.0.113.10", "state": "yes"},
	}
	var mu sync.Mutex
	next := 1
	zone := map[string]any{"name": "example.com", "ttl": "86400", "serial": "1", "refresh": "28800", "retry": "7200", "expire": "1209600"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Action string                     `json:"action"`
			Param  map[string]json.RawMessage `json:"param"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		defer mu.Unlock()
		reply := func(data any) {
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "statuscode": 2000, "responsedata": data})
		}
		switch req.Action {
		case "login":
			reply(map[string]string{"apisessionid": "sess"})
		case "logout":
			reply("")
		case "infoDnsZone":
			reply(zone)
		case "updateDnsZone":
			var in map[string]any
			_ = json.Unmarshal(req.Param["dnszone"], &in)
			for k, v := range in {
				zone[k] = v
			}
			reply(zone)
		case "infoDnsRecords":
			reply(map[string]any{"dnsrecords": recs})
		case "updateDnsRecords":
			var set struct {
				DNSRecords []map[string]any `json:"dnsrecords"`
			}
			_ = json.Unmarshal(req.Param["dnsrecordset"], &set)
			for _, raw := range set.DNSRecords {
				id, _ := raw["id"].(string)
				in := map[string]any{"state": "yes"}
				for _, k := range []string{"id", "hostname", "type", "priority", "destination"} {
					in[k] = raw[k]
				}
				_ = id
				if del, _ := raw["deleterecord"].(bool); del {
					kept := recs[:0:0]
					for _, e := range recs {
						if e["id"] != id {
							kept = append(kept, e)
						}
					}
					recs = kept
				} else if id == "" {
					next++
					in["id"] = strconv.Itoa(next)
					recs = append(recs, in)
				} else {
					for i, e := range recs {
						if e["id"] == id {
							recs[i] = in
						}
					}
				}
			}
			reply(map[string]any{"dnsrecords": recs})
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("NETCUP_DNS_ENDPOINT", srv.URL)
	t.Setenv("NETCUP_CUSTOMER_NUMBER", "123456")
	t.Setenv("NETCUP_DNS_API_KEY", "key")
	t.Setenv("NETCUP_DNS_API_PASSWORD", "pass")
}

func TestDNSRecordsList_Text(t *testing.T) {
	startFakeCCP(t)
	var buf bytes.Buffer
	if err := dnsRecordsList([]string{"example.com"}, &buf); err != nil {
		t.Fatalf("dnsRecordsList: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"ID", "HOSTNAME", "203.0.113.10", "A"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %q", want, out)
		}
	}
}

func TestDNSRecordsAdd_JSON(t *testing.T) {
	startFakeCCP(t)
	var buf bytes.Buffer
	err := dnsRecordsAdd([]string{"example.com", "www", "a", "203.0.113.5", "--json"}, &buf)
	if err != nil {
		t.Fatalf("dnsRecordsAdd: %v", err)
	}
	var got struct {
		ID          string `json:"id"`
		Hostname    string `json:"hostname"`
		Type        string `json:"type"`
		Destination string `json:"destination"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not JSON: %v: %q", err, buf.String())
	}
	if got.Hostname != "www" || got.Type != "A" || got.Destination != "203.0.113.5" || got.ID == "" {
		t.Errorf("record = %+v", got)
	}
}

func TestDNSRecordsDelete(t *testing.T) {
	t.Run("declined prompt aborts", func(t *testing.T) {
		startFakeCCP(t)
		var out, errW bytes.Buffer
		err := dnsRecordsDelete([]string{"example.com", "1"}, &out, &errW, strings.NewReader("n\n"))
		if err == nil || !strings.Contains(err.Error(), "aborted") {
			t.Fatalf("declined delete error = %v, want aborted", err)
		}
		if !strings.Contains(errW.String(), "WARNING") {
			t.Errorf("stderr missing warning: %q", errW.String())
		}
		var list bytes.Buffer
		if err := dnsRecordsList([]string{"example.com"}, &list); err != nil {
			t.Fatalf("list: %v", err)
		}
		if !strings.Contains(list.String(), "203.0.113.10") {
			t.Errorf("record 1 missing after declined delete: %q", list.String())
		}
	})
	t.Run("--yes skips the prompt", func(t *testing.T) {
		startFakeCCP(t)
		var out, errW bytes.Buffer
		if err := dnsRecordsDelete([]string{"example.com", "1", "--yes"}, &out, &errW, strings.NewReader("")); err != nil {
			t.Fatalf("delete --yes: %v", err)
		}
		if !strings.Contains(out.String(), "Deleted DNS record 1") {
			t.Errorf("output = %q", out.String())
		}
	})
}

func TestDNSRecordsUpdate_ChangesDestination(t *testing.T) {
	startFakeCCP(t)
	var buf bytes.Buffer
	if err := dnsRecordsUpdate([]string{"example.com", "1", "--destination", "203.0.113.99"}, &buf); err != nil {
		t.Fatalf("dnsRecordsUpdate: %v", err)
	}
	if !strings.Contains(buf.String(), "203.0.113.99") || strings.Contains(buf.String(), "203.0.113.10") {
		t.Errorf("output = %q, want new destination only", buf.String())
	}
}

func TestDNSZoneUpdate_PrintsUpdatedZone(t *testing.T) {
	startFakeCCP(t)
	var buf bytes.Buffer
	if err := dnsZoneUpdate([]string{"example.com", "--ttl", "3600"}, &buf); err != nil {
		t.Fatalf("dnsZoneUpdate: %v", err)
	}
	if !strings.Contains(buf.String(), "3600") || strings.Contains(buf.String(), "86400") {
		t.Errorf("output = %q, want ttl 3600", buf.String())
	}
}

func TestDNSZoneGet_Text(t *testing.T) {
	startFakeCCP(t)
	var buf bytes.Buffer
	if err := dnsZoneGet([]string{"example.com"}, &buf); err != nil {
		t.Fatalf("dnsZoneGet: %v", err)
	}
	if !strings.Contains(buf.String(), "86400") {
		t.Errorf("output missing ttl: %q", buf.String())
	}
}

func TestDNSZoneUpdate_NeedsAFlag(t *testing.T) {
	startFakeCCP(t)
	err := dnsZoneUpdate([]string{"example.com"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--ttl") {
		t.Fatalf("error = %v, want hint about --ttl", err)
	}
}

func TestDNS_MissingCredentials(t *testing.T) {
	t.Setenv("NETCUP_CUSTOMER_NUMBER", "")
	t.Setenv("NETCUP_DNS_API_KEY", "")
	t.Setenv("NETCUP_DNS_API_PASSWORD", "")
	err := dnsRecordsList([]string{"example.com"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "NETCUP_CUSTOMER_NUMBER") {
		t.Fatalf("error = %v, want missing-credentials hint", err)
	}
}

func TestDNSRecordsAdd_DashDestination(t *testing.T) {
	startFakeCCP(t)
	var buf bytes.Buffer
	if err := dnsRecordsAdd([]string{"example.com", "@", "TXT", "--", "-x"}, &buf); err != nil {
		t.Fatalf("add -- -x: %v", err)
	}
	if !strings.Contains(buf.String(), "-x") {
		t.Errorf("output = %q", buf.String())
	}
}

func TestDNSRecordsUpdate_DashDestination(t *testing.T) {
	startFakeCCP(t)
	var buf bytes.Buffer
	if err := dnsRecordsUpdate([]string{"example.com", "1", "--type", "TXT", "--destination=-x"}, &buf); err != nil {
		t.Fatalf("update --destination=-x: %v", err)
	}
	if !strings.Contains(buf.String(), "-x") {
		t.Errorf("output = %q", buf.String())
	}
}

func TestDNSRecordsUpdate_PriorityZeroApplied(t *testing.T) {
	startFakeCCP(t)
	var out bytes.Buffer
	if err := dnsRecordsAdd([]string{"example.com", "@", "MX", "mail.example.com", "--priority", "10"}, &out); err != nil {
		t.Fatalf("add: %v", err)
	}
	out.Reset()
	if err := dnsRecordsUpdate([]string{"example.com", "2", "--priority", "0", "--json"}, &out); err != nil {
		t.Fatalf("update: %v", err)
	}
	var got struct {
		Priority int `json:"priority"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Priority != 0 {
		t.Errorf("priority = %d, err %v, out %q", got.Priority, err, out.String())
	}
}

func TestDNSRecordsUpdate_BadFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"negative priority", []string{"example.com", "1", "--priority", "-1"}, "--priority must be >= 0"},
		{"empty hostname", []string{"example.com", "1", "--hostname="}, "--hostname"},
		{"empty type", []string{"example.com", "1", "--type="}, "--type"},
		{"empty destination", []string{"example.com", "1", "--destination="}, "--destination"},
		{"nothing set", []string{"example.com", "1"}, "at least one of"},
		{"unknown id", []string{"example.com", "999", "--destination", "1.2.3.4"}, "not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			startFakeCCP(t)
			err := dnsRecordsUpdate(tt.args, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestDNSZoneUpdate_BadValues(t *testing.T) {
	for _, flagName := range []string{"ttl", "refresh", "retry", "expire"} {
		for _, v := range []string{"0", "-5"} {
			t.Run(flagName+"="+v, func(t *testing.T) {
				startFakeCCP(t)
				err := dnsZoneUpdate([]string{"example.com", "--" + flagName, v}, &bytes.Buffer{})
				want := "--" + flagName + " must be positive"
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %v, want %q", err, want)
				}
			})
		}
	}
}

func TestDNSRecordsDelete_CredentialsBeforePrompt(t *testing.T) {
	t.Setenv("NETCUP_CUSTOMER_NUMBER", "")
	t.Setenv("NETCUP_DNS_API_KEY", "")
	t.Setenv("NETCUP_DNS_API_PASSWORD", "")
	var out, errW bytes.Buffer
	err := dnsRecordsDelete([]string{"example.com", "1"}, &out, &errW, strings.NewReader("y\n"))
	if err == nil || !strings.Contains(err.Error(), "NETCUP_CUSTOMER_NUMBER") {
		t.Fatalf("error = %v, want missing-credentials", err)
	}
	if strings.Contains(errW.String(), "WARNING") {
		t.Errorf("prompted before credential check: %q", errW.String())
	}
}

func TestDNS_WrongPositionalCount(t *testing.T) {
	startFakeCCP(t)
	tests := map[string]func() error{
		"zone get":       func() error { return dnsZoneGet(nil, &bytes.Buffer{}) },
		"zone update":    func() error { return dnsZoneUpdate([]string{"a.com", "b.com", "--ttl", "5"}, &bytes.Buffer{}) },
		"records list":   func() error { return dnsRecordsList([]string{"a.com", "x"}, &bytes.Buffer{}) },
		"records add":    func() error { return dnsRecordsAdd([]string{"a.com", "@", "A"}, &bytes.Buffer{}) },
		"records update": func() error { return dnsRecordsUpdate([]string{"a.com", "--ttl"}, &bytes.Buffer{}) },
		"records delete": func() error {
			return dnsRecordsDelete([]string{"a.com"}, &bytes.Buffer{}, &bytes.Buffer{}, strings.NewReader(""))
		},
	}
	for name, fn := range tests {
		t.Run(name, func(t *testing.T) {
			if err := fn(); err == nil {
				t.Error("error = nil, want usage error")
			}
		})
	}
}

func TestDNSRecordsDelete_JSON(t *testing.T) {
	startFakeCCP(t)
	var out bytes.Buffer
	if err := dnsRecordsDelete([]string{"example.com", "1", "--yes", "--json"}, &out, &bytes.Buffer{}, strings.NewReader("")); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var got struct {
		Domain  string `json:"domain"`
		ID      string `json:"id"`
		Deleted bool   `json:"deleted"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || !got.Deleted || got.ID != "1" || got.Domain != "example.com" {
		t.Errorf("got %+v err %v out %q", got, err, out.String())
	}
}

func TestDNSZoneGet_JSON(t *testing.T) {
	startFakeCCP(t)
	var out bytes.Buffer
	if err := dnsZoneGet([]string{"example.com", "--json"}, &out); err != nil {
		t.Fatalf("zone get: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v: %q", err, out.String())
	}
}

func TestDNS_FlagsBeforePositionals(t *testing.T) {
	startFakeCCP(t)
	var out bytes.Buffer
	if err := dnsRecordsAdd([]string{"--json", "--priority", "5", "example.com", "www", "MX", "mail.example.com"}, &out); err != nil {
		t.Fatalf("add: %v", err)
	}
	var got struct {
		Hostname string `json:"hostname"`
		Priority int    `json:"priority"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Hostname != "www" || got.Priority != 5 {
		t.Errorf("got %+v err %v out %q", got, err, out.String())
	}
}
