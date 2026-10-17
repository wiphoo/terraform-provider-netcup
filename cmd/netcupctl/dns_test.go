package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// startFakeCCP serves a minimal CCP endpoint with one A record and points the
// DNS env vars at it.
func startFakeCCP(t *testing.T) {
	t.Helper()
	recs := []map[string]any{
		{"id": "1", "hostname": "@", "type": "A", "priority": "0", "destination": "203.0.113.10", "state": "yes"},
	}
	next := 1
	zone := map[string]any{"name": "example.com", "ttl": "86400", "serial": "1", "refresh": "28800", "retry": "7200", "expire": "1209600"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Action string                     `json:"action"`
			Param  map[string]json.RawMessage `json:"param"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
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
			for _, in := range set.DNSRecords {
				id, _ := in["id"].(string)
				if del, _ := in["deleterecord"].(bool); del {
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
