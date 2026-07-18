package vcr

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/dnaeon/go-vcr/cassette"
)

func TestFakeIPv4Deterministic(t *testing.T) {
	a := fakeIPv4("192.0.2.10")
	b := fakeIPv4("192.0.2.10")
	if a != b {
		t.Fatalf("fakeIPv4 not deterministic: %q != %q", a, b)
	}
}

func TestFakeIPv4Range(t *testing.T) {
	_, cidr, err := net.ParseCIDR("203.0.113.0/24")
	if err != nil {
		t.Fatalf("ParseCIDR: %v", err)
	}
	for _, real := range []string{"192.0.2.10", "10.0.0.1", "8.8.8.8"} {
		fake := fakeIPv4(real)
		ip := net.ParseIP(fake)
		if ip == nil {
			t.Fatalf("fakeIPv4(%q) = %q is not a valid IP", real, fake)
		}
		if !cidr.Contains(ip) {
			t.Errorf("fakeIPv4(%q) = %q, want inside 203.0.113.0/24", real, fake)
		}
	}
}

func TestFakeIPv4DistinctInputs(t *testing.T) {
	a := fakeIPv4("192.0.2.10")
	b := fakeIPv4("192.0.2.11")
	if a == b {
		t.Errorf("fakeIPv4 mapped two different real IPs to the same fake: %q", a)
	}
}

func TestFakeIPv4NonIP(t *testing.T) {
	if got := fakeIPv4("not-an-ip"); got != "not-an-ip" {
		t.Errorf("fakeIPv4(non-IP) = %q, want unchanged", got)
	}
}

func TestRDNSIPFromInteractionUsesRedactedURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "ipv4",
			url:  "https://example.com/v1/rdns/ipv4/203.0.113.77",
			want: "203.0.113.77",
		},
		{
			name: "ipv6",
			url:  "https://example.com/v1/rdns/ipv6/2001:db8::77",
			want: "2001:db8::77",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ia := &cassette.Interaction{
				Request: cassette.Request{URL: tt.url},
			}

			ip, ok := rdnsIPFromInteraction(ia)
			if !ok {
				t.Fatal("rdnsIPFromInteraction did not find IP")
			}
			if ip != tt.want {
				t.Errorf("rdnsIPFromInteraction() = %q, want %s", ip, tt.want)
			}
		})
	}
}

func TestRDNSIPFromInteractionFallsBackToRequestBody(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "ipv4",
			body: `{"ip":"203.0.113.88","rdns":"host-a1b2c3d4.example.com"}`,
			want: "203.0.113.88",
		},
		{
			name: "ipv6",
			body: `{"ip":"2001:db8::88","rdns":"host-a1b2c3d4.example.com"}`,
			want: "2001:db8::88",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ia := &cassette.Interaction{
				Request: cassette.Request{Body: tt.body},
			}

			ip, ok := rdnsIPFromInteraction(ia)
			if !ok {
				t.Fatal("rdnsIPFromInteraction did not find IP")
			}
			if ip != tt.want {
				t.Errorf("rdnsIPFromInteraction() = %q, want %s", ip, tt.want)
			}
		})
	}
}

func TestFakeIPv6Deterministic(t *testing.T) {
	a := fakeIPv6("2001:db8:2:8f7::")
	b := fakeIPv6("2001:db8:2:8f7::")
	if a != b {
		t.Fatalf("fakeIPv6 not deterministic: %q != %q", a, b)
	}
}

func TestFakeIPv6Range(t *testing.T) {
	_, cidr, err := net.ParseCIDR("2001:db8::/32")
	if err != nil {
		t.Fatalf("ParseCIDR: %v", err)
	}
	for _, real := range []string{"2001:db8:2:8f7::", "fe80::1", "fe80::0200:5eff:fe00:5301"} {
		fake := fakeIPv6(real)
		ip := net.ParseIP(fake)
		if ip == nil {
			t.Fatalf("fakeIPv6(%q) = %q is not a valid IP", real, fake)
		}
		if !cidr.Contains(ip) {
			t.Errorf("fakeIPv6(%q) = %q, want inside 2001:db8::/32", real, fake)
		}
	}
}

func TestFakeIPv6RejectsIPv4(t *testing.T) {
	if got := fakeIPv6("192.0.2.10"); got != "192.0.2.10" {
		t.Errorf("fakeIPv6(IPv4) = %q, want unchanged", got)
	}
}

func TestFakeHostnameDeterministic(t *testing.T) {
	a := fakeHostname("example.host")
	b := fakeHostname("example.host")
	if a != b {
		t.Fatalf("fakeHostname not deterministic: %q != %q", a, b)
	}
	if a == "example.host" {
		t.Errorf("fakeHostname did not rewrite input")
	}
}

func TestFakeHostnameDistinctInputs(t *testing.T) {
	a := fakeHostname("host-a.example.org")
	b := fakeHostname("host-b.example.org")
	if a == b {
		t.Errorf("fakeHostname mapped two different real hostnames to the same fake: %q", a)
	}
}

func TestFakeMACDeterministic(t *testing.T) {
	a := fakeMAC("00:00:5e:00:53:01")
	b := fakeMAC("00:00:5e:00:53:01")
	if a != b {
		t.Fatalf("fakeMAC not deterministic: %q != %q", a, b)
	}
	if a == "00:00:5e:00:53:01" {
		t.Errorf("fakeMAC did not rewrite input")
	}
	if !strings.HasPrefix(a, "02:00:") {
		t.Errorf("fakeMAC = %q, want 02:00: prefix for synthetic MAC", a)
	}
}

// TestFakeMACRedactsLocalAdminMAC proves the 02: idempotence gap is closed: a
// real MAC that already starts with 02: (common for virtual NICs) must still be
// hashed, not returned unchanged — otherwise a live interface identifier would
// be committed verbatim. See PR #57 discussion_r3560302684.
func TestFakeMACRedactsLocalAdminMAC(t *testing.T) {
	const realLocalAdminMAC = "02:42:ac:11:00:01"
	got := fakeMAC(realLocalAdminMAC)
	if got == realLocalAdminMAC {
		t.Fatalf("fakeMAC returned a 02:-prefixed real MAC unchanged: %q", got)
	}
	if !strings.HasPrefix(got, "02:") {
		t.Errorf("fakeMAC = %q, want 02: prefix", got)
	}
}

// TestFakeHostnameCanonicalizesEquivalentPTRs covers a SetRDNS request PTR
// like "Foo.Example" and a later GetRDNS response PTR like "foo.example." —
// which pkg/netcup/rdns.go's EqualRDNSHostnames (used by ConfirmRDNS)
// treats as the same value. Without normalizing before hashing, these would
// map to two different fake hostnames, breaking a replayed set/read-back
// comparison even though the live recording succeeded.
func TestFakeHostnameCanonicalizesEquivalentPTRs(t *testing.T) {
	set := fakeHostname("Foo.Example")
	readBack := fakeHostname("foo.example.")
	if set != readBack {
		t.Errorf("fakeHostname(%q) = %q, fakeHostname(%q) = %q; want equal (SDK treats these PTRs as equivalent)",
			"Foo.Example", set, "foo.example.", readBack)
	}
}

func TestFakeHostnamePreservesAlreadyRedactedHostnames(t *testing.T) {
	const redacted = "host-a1b2c3d4.example.com"
	for _, input := range []string{redacted, "HOST-A1B2C3D4.EXAMPLE.COM", redacted + "."} {
		if got := fakeHostname(input); got != redacted {
			t.Errorf("fakeHostname(%q) = %q, want %q", input, got, redacted)
		}
	}
}

func TestFakeHostnamePreservesEmpty(t *testing.T) {
	if got := fakeHostname(""); got != "" {
		t.Errorf("fakeHostname(\"\") = %q, want \"\" (no PTR / no nickname is meaningful state)", got)
	}
}

func TestFakeHostnameFormat(t *testing.T) {
	got := fakeHostname("example.host")
	addr, err := netip.ParseAddr(got)
	if err == nil {
		t.Errorf("fakeHostname produced something IP-shaped: %q (%v)", got, addr)
	}
	if !hasSuffix(got, "."+fakeHostnameDomain) {
		t.Errorf("fakeHostname(%q) = %q, want suffix %q", "example.host", got, "."+fakeHostnameDomain)
	}
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

func TestIsIPv4Netmask(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"255.255.255.0", true},
		{"255.255.0.0", true},
		{"255.0.0.0", true},
		{"255.255.255.255", true},
		{"255.255.255.128", true},
		{"0.0.0.0", true},
		{"192.0.2.1", false},
		{"203.0.113.5", false},
		{"255.255.255.1", false}, // non-contiguous, not a valid mask
	}
	for _, tc := range tests {
		ip := net.ParseIP(tc.ip)
		if got := isIPv4Netmask(ip); got != tc.want {
			t.Errorf("isIPv4Netmask(%q) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}

func TestRedactJSONBodyServerFields(t *testing.T) {
	body := `{
		"id": 990099,
		"name": "test-server-01",
		"hostname": "example.host",
		"nickname": null,
		"userId": 555123,
		"template": {"id": 1581, "name": "VPS Lite 1 G12s"},
		"site": {"id": 1, "city": "Nuremberg"},
		"disabled": false,
		"ipv4Addresses": [
			{"id": 1, "ip": "192.0.2.10", "netmask": "255.255.255.0", "gateway": "192.0.2.1", "broadcast": "192.0.2.255"}
		],
		"ipv6Addresses": [
			{"id": 1, "networkPrefix": "2001:db8:2:8f7::", "networkPrefixLength": 64, "gateway": "fe80::1"}
		]
	}`

	// Extract original values from the source body for comparison.
	var orig map[string]interface{}
	dec := json.NewDecoder(strings.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&orig); err != nil {
		t.Fatalf("decode source body: %v", err)
	}
	origID, ok := orig["id"].(json.Number)
	if !ok || origID.String() == "" {
		t.Fatal("source body is missing a numeric id field")
	}
	origName, _ := orig["name"].(string)

	out, ok := redactJSONBody(body)
	if !ok {
		t.Fatalf("redactJSONBody: not recognized as JSON")
	}

	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("redacted output is not valid JSON: %v\n%s", err, out)
	}

	// Preserved as-is.
	if got["disabled"] != false {
		t.Errorf("disabled was modified: %v", got["disabled"])
	}
	site := got["site"].(map[string]interface{})
	if site["city"] != "Nuremberg" {
		t.Errorf("site.city was modified: %v", site["city"])
	}

	// Redacted.
	if got["hostname"] == "example.host" {
		t.Errorf("hostname was not redacted")
	}
	if got["userId"].(float64) != fakeUserIDValue {
		t.Errorf("userId = %v, want %d", got["userId"], fakeUserIDValue)
	}
	if got["name"] == origName {
		t.Errorf("name was not redacted: %v", got["name"])
	}
	var idStr string
	switch v := got["id"].(type) {
	case json.Number:
		idStr = v.String()
	case float64:
		idStr = strconv.FormatFloat(v, 'f', 0, 64)
	}
	if idStr == origID.String() {
		t.Errorf("id was not redacted: %v", got["id"])
	}
	v4 := got["ipv4Addresses"].([]interface{})[0].(map[string]interface{})
	if v4["ip"] == "192.0.2.10" {
		t.Errorf("ipv4 ip was not redacted")
	}
	if v4["gateway"] == "192.0.2.1" {
		t.Errorf("ipv4 gateway was not redacted")
	}
	if v4["broadcast"] == "192.0.2.255" {
		t.Errorf("ipv4 broadcast was not redacted")
	}
	if v4["netmask"] != "255.255.255.0" {
		t.Errorf("netmask should be preserved as-is, got %v", v4["netmask"])
	}
	if v4ID, ok := v4["id"].(float64); ok && fmt.Sprintf("%.0f", v4ID) == "1" {
		t.Errorf("ipv4Addresses[0].id was not redacted: %v", v4["id"])
	}
	v6 := got["ipv6Addresses"].([]interface{})[0].(map[string]interface{})
	if v6["networkPrefix"] == "2001:db8:2:8f7::" {
		t.Errorf("ipv6 networkPrefix was not redacted")
	}
	if v6["gateway"] == "fe80::1" {
		t.Errorf("ipv6 gateway was not redacted")
	}
}

// TestRedactJSONBodyServerLiveInfoInterfaces covers serverLiveInfo.interfaces[]
// (see pkg/netcup/testdata/server_detail.json), which isn't modeled by any Go
// struct (ServerInfo only decodes "state") but still carries real IPv6
// addresses in the raw response: a plain array (ipv6LinkLocalAddresses) and
// an array of CIDR strings (ipv6NetworkPrefixes).
func TestRedactJSONBodyServerLiveInfoInterfaces(t *testing.T) {
	body := `{
		"serverLiveInfo": {
			"state": "RUNNING",
			"interfaces": [
				{
					"mac": "00:00:5e:00:53:01",
					"ipv4Addresses": [],
					"ipv6LinkLocalAddresses": ["fe80::0200:5eff:fe00:5301"],
					"ipv6NetworkPrefixes": ["2001:db8:2:8f7::/64"]
				}
			]
		}
	}`

	out, ok := redactJSONBody(body)
	if !ok {
		t.Fatalf("redactJSONBody: not recognized as JSON")
	}

	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("redacted output is not valid JSON: %v\n%s", err, out)
	}

	iface := got["serverLiveInfo"].(map[string]interface{})["interfaces"].([]interface{})[0].(map[string]interface{})

	if iface["mac"] == "00:00:5e:00:53:01" {
		t.Errorf("mac was not redacted: %v", iface["mac"])
	}
	if mac, ok := iface["mac"].(string); ok && !strings.HasPrefix(mac, "02:00:") {
		t.Errorf("mac = %q, want 02:00: prefix for synthetic MAC", mac)
	}

	linkLocal := iface["ipv6LinkLocalAddresses"].([]interface{})
	if linkLocal[0] == "fe80::0200:5eff:fe00:5301" {
		t.Errorf("ipv6LinkLocalAddresses[0] was not redacted: %v", linkLocal[0])
	}
	fake := linkLocal[0].(string)
	if _, err := netip.ParseAddr(fake); err != nil {
		t.Errorf("ipv6LinkLocalAddresses[0] = %q is not a valid IP", fake)
	}

	prefixes := iface["ipv6NetworkPrefixes"].([]interface{})
	prefix := prefixes[0].(string)
	if strings.HasPrefix(prefix, "2001:db8:2:8f7::") {
		t.Errorf("ipv6NetworkPrefixes[0] was not redacted: %v", prefix)
	}
	if !strings.HasSuffix(prefix, "/64") {
		t.Errorf("ipv6NetworkPrefixes[0] = %q, want the /64 suffix preserved", prefix)
	}
	addr, _, _ := strings.Cut(prefix, "/")
	if _, err := netip.ParseAddr(addr); err != nil {
		t.Errorf("ipv6NetworkPrefixes[0] address part = %q is not a valid IP", addr)
	}
}

func TestRedactJSONBodyNonJSON(t *testing.T) {
	if _, ok := redactJSONBody("OK"); ok {
		t.Errorf("redactJSONBody(\"OK\") should not be recognized as JSON")
	}
	if _, ok := redactJSONBody(""); ok {
		t.Errorf("redactJSONBody(\"\") should not be recognized as JSON")
	}
}

func TestRedactResponseBodyPassesThroughNonJSON(t *testing.T) {
	if got := redactResponseBody("OK"); got != "OK" {
		t.Errorf("redactResponseBody(\"OK\") = %q, want unchanged", got)
	}
}

func TestRedactRequestBodyForm(t *testing.T) {
	body := "grant_type=refresh_token&refresh_token=abc123&client_id=scp"
	got := redactRequestBody("application/x-www-form-urlencoded", body)
	values, err := url.ParseQuery(got)
	if err != nil {
		t.Fatalf("redacted form body doesn't parse: %v", err)
	}
	if values.Get("refresh_token") == "abc123" {
		t.Errorf("refresh_token was not redacted: %q", got)
	}
	if values.Get("client_id") != "scp" {
		t.Errorf("client_id should be preserved as-is, got %q", values.Get("client_id"))
	}
}

// TestRedactFormValues covers cassette.Interaction.Request.Form, go-vcr's
// second, independent copy of a form-encoded request's fields (parsed via
// http.Request.ParseForm, stored separately from the serialized Body
// string) — see the AddFilter call in recorder.go, which must redact both.
func TestRedactFormValues(t *testing.T) {
	values := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {"opaque-real-refresh-token"},
		"client_id":     {"scp"},
	}
	redactFormValues(values)
	if values.Get("refresh_token") != redactedTokenPlaceholder {
		t.Errorf("refresh_token = %q, want %q", values.Get("refresh_token"), redactedTokenPlaceholder)
	}
	if values.Get("client_id") != "scp" {
		t.Errorf("client_id should be preserved as-is, got %q", values.Get("client_id"))
	}
}

func TestRedactURLRdns(t *testing.T) {
	got := redactURL("https://www.servercontrolpanel.de/scp-core/api/v1/rdns/ipv4/203.0.113.99")
	if got == "https://www.servercontrolpanel.de/scp-core/api/v1/rdns/ipv4/203.0.113.99" {
		t.Errorf("redactURL did not rewrite the embedded IP: %q", got)
	}
	want := "https://www.servercontrolpanel.de/scp-core/api/v1/rdns/ipv4/" + fakeIPv4("203.0.113.99")
	if got != want {
		t.Errorf("redactURL = %q, want %q", got, want)
	}
}

func TestRedactURLServer(t *testing.T) {
	raw := "https://www.servercontrolpanel.de/scp-core/api/v1/servers/990099"
	got := redactURL(raw)
	if got == raw {
		t.Errorf("redactURL(%q) = %q, want redacted", raw, got)
	}
	fakeID := fakeServerID(json.Number("990099"))
	want := "https://www.servercontrolpanel.de/scp-core/api/v1/servers/" + fakeID.String()
	if got != want {
		t.Errorf("redactURL = %q, want %q", got, want)
	}
	// Non-server, non-rDNS URLs should be unchanged.
	if got := redactURL("https://example.com/health"); got != "https://example.com/health" {
		t.Errorf("redactURL(health) = %q, want unchanged", got)
	}
}

// TestMatchInteraction covers the cassette.Matcher installed via
// rec.SetMatcher in recorder.go (replacing go-vcr's DefaultMatcher's exact
// method+URL string equality): a replay-mode request built from the real IP
// must still match a cassette entry whose URL was redacted (fake IP) at
// save time.
func TestMatchInteraction(t *testing.T) {
	const realIP = "192.0.2.50"
	fakeURL := "https://example.com/v1/rdns/ipv4/" + fakeIPv4(realIP)
	stored := cassette.Request{Method: "GET", URL: fakeURL}

	realReq := httptest.NewRequest("GET", "https://example.com/v1/rdns/ipv4/"+realIP, nil)
	if !matchInteraction(realReq, stored) {
		t.Errorf("matchInteraction did not match a real-IP request against its redacted cassette entry")
	}

	fakeReq := httptest.NewRequest("GET", fakeURL, nil)
	if !matchInteraction(fakeReq, stored) {
		t.Errorf("matchInteraction did not match an already-redacted request (exact-match fast path)")
	}

	otherReq := httptest.NewRequest("GET", "https://example.com/v1/rdns/ipv4/192.0.2.99", nil)
	if matchInteraction(otherReq, stored) {
		t.Errorf("matchInteraction matched an unrelated IP's request")
	}

	wrongMethod := httptest.NewRequest("DELETE", "https://example.com/v1/rdns/ipv4/"+realIP, nil)
	if matchInteraction(wrongMethod, stored) {
		t.Errorf("matchInteraction matched despite a different HTTP method")
	}
}

func TestFakeServerIDDeterministic(t *testing.T) {
	a := fakeServerID(json.Number("990099"))
	b := fakeServerID(json.Number("990099"))
	if a != b {
		t.Fatalf("fakeServerID not deterministic: %q != %q", a, b)
	}
	if a.String() == "990099" {
		t.Errorf("fakeServerID returned the real value unchanged: %s", a)
	}
}

func TestFakeServerIDDistinctInputs(t *testing.T) {
	a := fakeServerID(json.Number("990099"))
	b := fakeServerID(json.Number("991100"))
	if a == b {
		t.Errorf("fakeServerID mapped two different ids to the same fake: %s", a)
	}
}

func TestFakeServerNameDeterministic(t *testing.T) {
	a := fakeServerName("test-server-01")
	b := fakeServerName("test-server-01")
	if a != b {
		t.Fatalf("fakeServerName not deterministic: %q != %q", a, b)
	}
	if a == "test-server-01" {
		t.Errorf("fakeServerName returned the real value unchanged: %s", a)
	}
}

func TestFakeServerNameEmptyPassesThrough(t *testing.T) {
	if got := fakeServerName(""); got != "" {
		t.Errorf("fakeServerName(\"\") = %q, want empty string", got)
	}
}

func TestFakeUsernameDeterministicAndSynthetic(t *testing.T) {
	a := fakeUsername("555123")
	b := fakeUsername("555123")
	if a != b {
		t.Fatalf("fakeUsername not deterministic: %q != %q", a, b)
	}
	if a == "555123" {
		t.Errorf("fakeUsername returned the real value unchanged: %s", a)
	}
	// syntheticUsernamePattern lives in scrub_test.go (same package) — the guard
	// must accept exactly what the redactor emits.
	if !syntheticUsernamePattern.MatchString(a) {
		t.Errorf("fakeUsername(%q) = %q, want to match %s", "555123", a, syntheticUsernamePattern)
	}
}

func TestFakeUsernameEmptyPassesThrough(t *testing.T) {
	if got := fakeUsername(""); got != "" {
		t.Errorf("fakeUsername(\"\") = %q, want empty string", got)
	}
}

// TestRedactJSONBodyV030Fields covers the fields the v0.3.0 surface adds:
// TaskInfo.executingUser.username (an account identifier), the rescue-system
// password (a live root credential), and a snapshot description (free text).
// All three must be rewritten; a null password (rescue inactive) must survive.
func TestRedactJSONBodyV030Fields(t *testing.T) {
	body := `{
		"executingUser": {"id": 42, "username": "555123"},
		"password": "s3cr3t-root-pw",
		"description": "weekly backup for the billing db"
	}`

	out, ok := redactJSONBody(body)
	if !ok {
		t.Fatalf("redactJSONBody: not recognized as JSON")
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("redacted output is not valid JSON: %v\n%s", err, out)
	}

	user := got["executingUser"].(map[string]interface{})
	if user["username"] == "555123" {
		t.Errorf("executingUser.username was not redacted: %v", user["username"])
	}
	if u, ok := user["username"].(string); ok && !syntheticUsernamePattern.MatchString(u) {
		t.Errorf("executingUser.username = %q, want user-<hash>", u)
	}
	if got["password"] != redactedPasswordPlaceholder {
		t.Errorf("password = %v, want %q", got["password"], redactedPasswordPlaceholder)
	}
	if got["description"] != redactedDescriptionPlaceholder {
		t.Errorf("description = %v, want %q", got["description"], redactedDescriptionPlaceholder)
	}
}

// TestRedactJSONBodyNullPasswordPreserved confirms a null password (the rescue
// system inactive) is meaningful state, not a secret, and survives redaction.
func TestRedactJSONBodyNullPasswordPreserved(t *testing.T) {
	out, ok := redactJSONBody(`{"active":false,"password":null}`)
	if !ok {
		t.Fatalf("redactJSONBody: not recognized as JSON")
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("redacted output is not valid JSON: %v\n%s", err, out)
	}
	if got["password"] != nil {
		t.Errorf("password = %v, want null preserved", got["password"])
	}
	if got["active"] != false {
		t.Errorf("active = %v, want false preserved", got["active"])
	}
}

func TestMatchInteractionServer(t *testing.T) {
	const realID = "990099"
	fakeID := fakeServerID(json.Number(realID))
	fakeURL := "https://example.com/v1/servers/" + fakeID.String()
	stored := cassette.Request{Method: "GET", URL: fakeURL}

	realReq := httptest.NewRequest("GET", "https://example.com/v1/servers/"+realID, nil)
	if !matchInteraction(realReq, stored) {
		t.Errorf("matchInteraction did not match a real-ID request against its redacted cassette entry")
	}

	fakeReq := httptest.NewRequest("GET", fakeURL, nil)
	if !matchInteraction(fakeReq, stored) {
		t.Errorf("matchInteraction did not match an already-redacted request (exact-match fast path)")
	}
}

func TestMatchInteractionNonRdnsExactOnly(t *testing.T) {
	stored := cassette.Request{Method: "GET", URL: "https://example.com/v1/servers/990099"}
	req := httptest.NewRequest("GET", "https://example.com/v1/servers/990099", nil)
	if !matchInteraction(req, stored) {
		t.Errorf("matchInteraction did not match an identical non-rDNS URL")
	}

	other := httptest.NewRequest("GET", "https://example.com/v1/servers/991100", nil)
	if matchInteraction(other, stored) {
		t.Errorf("matchInteraction matched a different server id")
	}
}
