package netcup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// DefaultDNSEndpoint is the netcup CCP JSON API endpoint that serves DNS
// zone and record management. The SCP REST API has no DNS endpoints.
const DefaultDNSEndpoint = "https://ccp.netcup.net/run/webservice/servers/endpoint.php?JSON"

// DNSCredentials are the CCP API credentials: the customer number plus an API
// key and API password created in the CCP ("Master data" > "API").
type DNSCredentials struct {
	CustomerNumber string
	APIKey         string
	APIPassword    string
}

func (c DNSCredentials) validate() error {
	var missing []string
	if c.CustomerNumber == "" {
		missing = append(missing, "NETCUP_CUSTOMER_NUMBER")
	}
	if c.APIKey == "" {
		missing = append(missing, "NETCUP_DNS_API_KEY")
	}
	if c.APIPassword == "" {
		missing = append(missing, "NETCUP_DNS_API_PASSWORD")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing netcup DNS credentials: set %s", strings.Join(missing, ", "))
	}
	return nil
}

// DNSCredentialsFromEnv reads NETCUP_CUSTOMER_NUMBER, NETCUP_DNS_API_KEY and
// NETCUP_DNS_API_PASSWORD, failing with the names of any that are unset.
func DNSCredentialsFromEnv() (DNSCredentials, error) {
	c := DNSCredentials{
		CustomerNumber: os.Getenv("NETCUP_CUSTOMER_NUMBER"),
		APIKey:         os.Getenv("NETCUP_DNS_API_KEY"),
		APIPassword:    os.Getenv("NETCUP_DNS_API_PASSWORD"),
	}
	if err := c.validate(); err != nil {
		return DNSCredentials{}, err
	}
	return c, nil
}

// DNSError is a failed CCP call. The CCP API answers HTTP 200 even on failure
// and reports the outcome in the JSON envelope, so StatusCode is the envelope's
// "statuscode" (2000 = success), not an HTTP status. It never contains the
// request, so credentials cannot leak through it.
type DNSError struct {
	Action     string
	StatusCode int
	Short      string
	Long       string
}

func (e *DNSError) Error() string {
	msg := fmt.Sprintf("netcup DNS API %s failed (%d): %s", e.Action, e.StatusCode, e.Short)
	if e.Long != "" {
		msg += ": " + e.Long
	}
	return msg
}

// DNSClient talks to the CCP DNS API. Construct it with NewDNSClient. It logs
// in lazily on first use and caches the session id; call Logout when done.
//
// ponytail: not goroutine-safe and no re-login on session expiry (~15 min);
// add a mutex + retry-on-4001 when the long-running provider consumes it.
type DNSClient struct {
	creds      DNSCredentials
	endpoint   string
	httpClient *http.Client
	sessionID  string
}

// DNSOption customizes a DNSClient during construction.
type DNSOption func(*DNSClient)

// WithDNSEndpoint overrides the CCP endpoint URL.
func WithDNSEndpoint(endpoint string) DNSOption {
	return func(c *DNSClient) { c.endpoint = endpoint }
}

// WithDNSHTTPClient sets a custom *http.Client (for timeouts, transports, tests).
// The caller is responsible for the client's redirect policy: the request body
// carries the API key and password, and the default client refuses redirects.
func WithDNSHTTPClient(h *http.Client) DNSOption {
	return func(c *DNSClient) {
		if h != nil {
			c.httpClient = h
		}
	}
}

// NewDNSClient creates a DNSClient. The endpoint defaults to DefaultDNSEndpoint,
// is overridden by NETCUP_DNS_ENDPOINT when set, and finally by any options.
func NewDNSClient(creds DNSCredentials, opts ...DNSOption) (*DNSClient, error) {
	if err := creds.validate(); err != nil {
		return nil, err
	}
	c := &DNSClient{
		creds:    creds,
		endpoint: DefaultDNSEndpoint,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
			// Redirects would re-send the credential-bearing POST body elsewhere.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	if v := os.Getenv("NETCUP_DNS_ENDPOINT"); v != "" {
		c.endpoint = v
	}
	for _, opt := range opts {
		opt(c)
	}
	// Credentials travel in the request body, so refuse cleartext to non-loopback hosts.
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, errors.New("invalid DNS endpoint")
	}
	if u.User != nil {
		return nil, fmt.Errorf("DNS endpoint %q must not contain userinfo", u.Redacted())
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("DNS endpoint %q has no host", u.Redacted())
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !isLoopbackHost(u.Hostname())) {
		return nil, fmt.Errorf("DNS endpoint %q must use https (credentials are sent in the request)", u.Redacted())
	}
	return c, nil
}

// isLoopbackHost reports whether h is "localhost" or a loopback IP address.
func isLoopbackHost(h string) bool {
	h = strings.TrimSuffix(strings.ToLower(h), ".")
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// ccpResponse is the CCP response envelope.
type ccpResponse struct {
	Status       string          `json:"status"`
	StatusCode   int             `json:"statuscode"`
	ShortMessage string          `json:"shortmessage"`
	LongMessage  string          `json:"longmessage"`
	ResponseData json.RawMessage `json:"responsedata"`
}

// do POSTs one {"action","param"} envelope and decodes responsedata into out
// (when out is non-nil). Any envelope status other than "success" is a *DNSError.
func (c *DNSClient) do(ctx context.Context, action string, param map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"action": action, "param": param})
	if err != nil {
		return fmt.Errorf("%w: encoding %s request: %w", ErrPreDispatch, action, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: building request: %w", ErrPreDispatch, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("netcup DNS %s: %w", action, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return newAPIError(resp)
	}

	var r ccpResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("decoding %s response: %w", action, err)
	}
	if r.Status != "success" {
		return &DNSError{Action: action, StatusCode: r.StatusCode, Short: r.ShortMessage, Long: r.LongMessage}
	}
	// CCP sends "" as responsedata on some successes; never decode that into a struct.
	if raw := bytes.TrimSpace(r.ResponseData); out != nil && !bytes.Equal(raw, []byte(`""`)) && !bytes.Equal(raw, []byte("null")) && len(raw) > 0 {
		if err := json.Unmarshal(r.ResponseData, out); err != nil {
			return fmt.Errorf("decoding %s data: %w", action, err)
		}
	}
	return nil
}

func (c *DNSClient) login(ctx context.Context) error {
	var data struct {
		SessionID string `json:"apisessionid"`
	}
	err := c.do(ctx, "login", map[string]any{
		"customernumber": c.creds.CustomerNumber,
		"apikey":         c.creds.APIKey,
		"apipassword":    c.creds.APIPassword,
	}, &data)
	if err != nil {
		return err
	}
	if data.SessionID == "" {
		return errors.New("netcup DNS API login returned no session id")
	}
	c.sessionID = data.SessionID
	return nil
}

// authed runs action with the session credentials merged into param, logging
// in first when there is no session yet.
func (c *DNSClient) authed(ctx context.Context, action string, extra map[string]any, out any) error {
	if c.sessionID == "" {
		if err := c.login(ctx); err != nil {
			return err
		}
	}
	param := map[string]any{
		"customernumber": c.creds.CustomerNumber,
		"apikey":         c.creds.APIKey,
		"apisessionid":   c.sessionID,
	}
	for k, v := range extra {
		param[k] = v
	}
	return c.do(ctx, action, param, out)
}

// Logout ends the CCP session. It is a no-op when no session is open, and the
// local session is cleared even if the server call fails.
func (c *DNSClient) Logout(ctx context.Context) error {
	if c.sessionID == "" {
		return nil
	}
	err := c.authed(ctx, "logout", nil, nil)
	c.sessionID = ""
	return err
}

// flexInt decodes a JSON number that CCP may send either as a number or as a
// quoted string ("86400"), and encodes it as a quoted string like CCP does.
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("invalid number %q: %w", s, err)
	}
	*f = flexInt(n)
	return nil
}

func (f flexInt) MarshalJSON() ([]byte, error) { return json.Marshal(strconv.Itoa(int(f))) }

// normalizeDomain lowercases a domain and strips whitespace and a trailing dot.
func normalizeDomain(d string) (string, error) {
	d = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(d), "."))
	if d == "" {
		return "", errors.New("domain must not be empty")
	}
	return d, nil
}

// DNSRecord is one record of a zone.
type DNSRecord struct {
	ID          string `json:"id"`
	Hostname    string `json:"hostname"`
	Type        string `json:"type"`
	Priority    int    `json:"priority"`
	Destination string `json:"destination"`
	State       string `json:"state,omitempty"`
}

// recordWire is the CCP shape of a record (numbers are strings on the wire).
type recordWire struct {
	ID           string  `json:"id"`
	Hostname     string  `json:"hostname"`
	Type         string  `json:"type"`
	Priority     flexInt `json:"priority"`
	Destination  string  `json:"destination"`
	State        string  `json:"state,omitempty"`
	DeleteRecord bool    `json:"deleterecord,omitempty"`
}

func (w recordWire) record() DNSRecord {
	return DNSRecord{ID: w.ID, Hostname: w.Hostname, Type: w.Type, Priority: int(w.Priority), Destination: w.Destination, State: w.State}
}

func recordToWire(r DNSRecord) recordWire {
	return recordWire{ID: r.ID, Hostname: r.Hostname, Type: r.Type, Priority: flexInt(r.Priority), Destination: r.Destination}
}

// recordSet is the CCP "dnsrecords" container used in responses and requests.
type recordSet struct {
	DNSRecords []recordWire `json:"dnsrecords"`
}

func (s recordSet) records() []DNSRecord {
	out := make([]DNSRecord, 0, len(s.DNSRecords))
	for _, w := range s.DNSRecords {
		out = append(out, w.record())
	}
	return out
}

// ListRecords returns all records of the zone.
func (c *DNSClient) ListRecords(ctx context.Context, domain string) ([]DNSRecord, error) {
	domain, err := normalizeDomain(domain)
	if err != nil {
		return nil, err
	}
	var set recordSet
	if err := c.authed(ctx, "infoDnsRecords", map[string]any{"domainname": domain}, &set); err != nil {
		return nil, err
	}
	return set.records(), nil
}

// DNSZone is the SOA-level configuration of a zone.
type DNSZone struct {
	Name    string `json:"name"`
	TTL     int    `json:"ttl"`
	Serial  int    `json:"serial"`
	Refresh int    `json:"refresh"`
	Retry   int    `json:"retry"`
	Expire  int    `json:"expire"`
	DNSSEC  bool   `json:"dnssec"`
}

type zoneWire struct {
	Name         string  `json:"name"`
	TTL          flexInt `json:"ttl"`
	Serial       flexInt `json:"serial"`
	Refresh      flexInt `json:"refresh"`
	Retry        flexInt `json:"retry"`
	Expire       flexInt `json:"expire"`
	DNSSECStatus bool    `json:"dnssecstatus"`
}

func (w zoneWire) zone() DNSZone {
	return DNSZone{
		Name: w.Name, TTL: int(w.TTL), Serial: int(w.Serial), Refresh: int(w.Refresh),
		Retry: int(w.Retry), Expire: int(w.Expire), DNSSEC: w.DNSSECStatus,
	}
}

// GetZone returns the zone configuration for domain. CCP has no "list zones"
// call, so zones are addressed by domain name.
func (c *DNSClient) GetZone(ctx context.Context, domain string) (*DNSZone, error) {
	domain, err := normalizeDomain(domain)
	if err != nil {
		return nil, err
	}
	var w zoneWire
	if err := c.authed(ctx, "infoDnsZone", map[string]any{"domainname": domain}, &w); err != nil {
		return nil, err
	}
	z := w.zone()
	return &z, nil
}

// UpdateZone writes the zone configuration (typically a zone fetched with
// GetZone and then modified) and returns the zone as stored. TTL, Refresh,
// Retry and Expire must be positive.
func (c *DNSClient) UpdateZone(ctx context.Context, z DNSZone) (*DNSZone, error) {
	domain, err := normalizeDomain(z.Name)
	if err != nil {
		return nil, err
	}
	for _, f := range []struct {
		name string
		v    int
	}{{"ttl", z.TTL}, {"refresh", z.Refresh}, {"retry", z.Retry}, {"expire", z.Expire}} {
		if f.v <= 0 {
			return nil, fmt.Errorf("zone %s must be positive", f.name)
		}
	}
	wire := zoneWire{
		Name: domain, TTL: flexInt(z.TTL), Serial: flexInt(z.Serial), Refresh: flexInt(z.Refresh),
		Retry: flexInt(z.Retry), Expire: flexInt(z.Expire), DNSSECStatus: z.DNSSEC,
	}
	var out zoneWire
	if err := c.authed(ctx, "updateDnsZone", map[string]any{"domainname": domain, "dnszone": wire}, &out); err != nil {
		return nil, err
	}
	updated := out.zone()
	return &updated, nil
}

var dnsRecordTypes = map[string]bool{
	"A": true, "AAAA": true, "MX": true, "CNAME": true, "CAA": true,
	"SRV": true, "TXT": true, "TLSA": true, "NS": true, "DS": true,
}

// validateRecord trims and normalizes r in place (type uppercased) and rejects
// records CCP would refuse. "@" (apex) and "*" (wildcard) are valid hostnames.
func validateRecord(r *DNSRecord) error {
	r.Hostname = strings.TrimSpace(r.Hostname)
	r.Type = strings.ToUpper(strings.TrimSpace(r.Type))
	r.Destination = strings.TrimSpace(r.Destination)
	switch {
	case r.Hostname == "":
		return errors.New("record hostname must not be empty (use @ for the apex)")
	case !dnsRecordTypes[r.Type]:
		return fmt.Errorf("unsupported record type %q", r.Type)
	case r.Destination == "":
		return errors.New("record destination must not be empty")
	case r.Priority < 0:
		return errors.New("record priority must not be negative")
	}
	return nil
}

// ErrRecordNotFound and ErrRecordExists let callers branch with errors.Is.
var (
	ErrRecordNotFound = errors.New("dns record not found")
	ErrRecordExists   = errors.New("dns record already exists")
)

// sameDestination compares destinations the way DNS does for the record type:
// hostnames (CNAME/MX/NS/SRV) ignore case and a trailing dot, addresses ignore
// case, and TXT/CAA/TLSA/DS compare exactly.
func sameDestination(typ, a, b string) bool {
	switch strings.ToUpper(typ) {
	case "CNAME", "MX", "NS", "SRV":
		return strings.EqualFold(strings.TrimSuffix(a, "."), strings.TrimSuffix(b, "."))
	case "A", "AAAA":
		return strings.EqualFold(a, b)
	}
	return a == b
}

// sameRecord reports whether a and b describe the same record content
// (hostname and type case-insensitively; destination per sameDestination).
func sameRecord(a, b DNSRecord) bool {
	return strings.EqualFold(a.Hostname, b.Hostname) && strings.EqualFold(a.Type, b.Type) &&
		sameDestination(a.Type, a.Destination, b.Destination) && a.Priority == b.Priority
}

// findRecord lists the zone and returns the record with id plus the full list.
// A missing id yields an error wrapping ErrRecordNotFound.
func (c *DNSClient) findRecord(ctx context.Context, domain, id string) (DNSRecord, []DNSRecord, error) {
	recs, err := c.ListRecords(ctx, domain)
	if err != nil {
		return DNSRecord{}, nil, err
	}
	for _, e := range recs {
		if e.ID == id {
			return e, recs, nil
		}
	}
	return DNSRecord{}, recs, fmt.Errorf("record %s in %s: %w", id, domain, ErrRecordNotFound)
}

// pushRecords sends changes via updateDnsRecords and returns the zone's full
// record list as CCP reports it afterwards.
func (c *DNSClient) pushRecords(ctx context.Context, domain string, changes []recordWire) ([]DNSRecord, error) {
	var set recordSet
	err := c.authed(ctx, "updateDnsRecords", map[string]any{
		"domainname":   domain,
		"dnsrecordset": recordSet{DNSRecords: changes},
	}, &set)
	if err != nil {
		return nil, err
	}
	return set.records(), nil
}

// CreateRecord adds rec to the zone and returns the stored record. It refuses
// to create a duplicate of an existing record (same hostname, type,
// destination and priority, compared normalization-aware) so a retried create
// cannot mint a second copy.
func (c *DNSClient) CreateRecord(ctx context.Context, domain string, rec DNSRecord) (*DNSRecord, error) {
	domain, err := normalizeDomain(domain)
	if err != nil {
		return nil, err
	}
	if err := validateRecord(&rec); err != nil {
		return nil, err
	}
	before, err := c.ListRecords(ctx, domain)
	if err != nil {
		return nil, err
	}
	had := make(map[string]bool, len(before))
	for _, e := range before {
		if sameRecord(e, rec) {
			return nil, fmt.Errorf("record %s in %s: %w; update or delete it instead", e.ID, domain, ErrRecordExists)
		}
		had[e.ID] = true
	}
	rec.ID = ""
	after, err := c.pushRecords(ctx, domain, []recordWire{recordToWire(rec)})
	if err != nil {
		return nil, err
	}
	var fresh []DNSRecord
	for _, a := range after {
		if had[a.ID] {
			continue
		}
		if sameRecord(a, rec) {
			created := a
			return &created, nil
		}
		if strings.EqualFold(a.Hostname, rec.Hostname) && strings.EqualFold(a.Type, rec.Type) {
			fresh = append(fresh, a)
		}
	}
	// CCP may normalize the destination (e.g. trailing dot); accept a single new look-alike.
	if len(fresh) == 1 {
		return &fresh[0], nil
	}
	return nil, errors.New("record was submitted but not found in the zone afterwards; check with 'dns records list'")
}

// UpdateRecord replaces the record identified by rec.ID with rec and returns
// the stored record. It fails with ErrRecordNotFound if no record with that id
// exists, and with ErrRecordExists if the result would duplicate another record.
func (c *DNSClient) UpdateRecord(ctx context.Context, domain string, rec DNSRecord) (*DNSRecord, error) {
	domain, err := normalizeDomain(domain)
	if err != nil {
		return nil, err
	}
	if rec.ID == "" {
		return nil, errors.New("record id must not be empty")
	}
	if err := validateRecord(&rec); err != nil {
		return nil, err
	}
	_, recs, err := c.findRecord(ctx, domain, rec.ID)
	if err != nil {
		return nil, err
	}
	for _, e := range recs {
		if e.ID != rec.ID && sameRecord(e, rec) {
			return nil, fmt.Errorf("record %s in %s: %w", e.ID, domain, ErrRecordExists)
		}
	}
	after, err := c.pushRecords(ctx, domain, []recordWire{recordToWire(rec)})
	if err != nil {
		return nil, err
	}
	for _, a := range after {
		if a.ID == rec.ID {
			updated := a
			return &updated, nil
		}
	}
	return nil, fmt.Errorf("record %s missing from the zone after update", rec.ID)
}

// DeleteRecord removes the record with the given id from the zone. CCP needs
// the full record on delete, so it is looked up first; an unknown id wraps
// ErrRecordNotFound.
func (c *DNSClient) DeleteRecord(ctx context.Context, domain, id string) error {
	domain, err := normalizeDomain(domain)
	if err != nil {
		return err
	}
	if id == "" {
		return errors.New("record id must not be empty")
	}
	e, _, err := c.findRecord(ctx, domain, id)
	if err != nil {
		return err
	}
	w := recordToWire(e)
	w.DeleteRecord = true
	after, err := c.pushRecords(ctx, domain, []recordWire{w})
	if err != nil {
		return err
	}
	for _, a := range after {
		if a.ID == id {
			return fmt.Errorf("record %s still present after delete", id)
		}
	}
	return nil
}
