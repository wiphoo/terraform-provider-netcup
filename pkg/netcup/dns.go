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
		creds:      creds,
		endpoint:   DefaultDNSEndpoint,
		httpClient: &http.Client{Timeout: defaultTimeout},
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
		return nil, fmt.Errorf("invalid DNS endpoint %q: %w", c.endpoint, err)
	}
	if h := u.Hostname(); u.Scheme != "https" && h != "localhost" && !(net.ParseIP(h) != nil && net.ParseIP(h).IsLoopback()) {
		return nil, fmt.Errorf("DNS endpoint %q must use https (credentials are sent in the request)", c.endpoint)
	}
	return c, nil
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
	if raw := bytes.TrimSpace(r.ResponseData); out != nil && len(raw) > 0 && string(raw) != `""` && string(raw) != "null" {
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
