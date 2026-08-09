package vcr

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/wiphoo/terraform-provider-netcup/pkg/netcup"
)

// RequestLog records the method and URL of every request a client made through
// the go-vcr recorder. go-vcr replay does NOT fail when a cassette interaction
// goes unconsumed — requestHandler only errors on an unmatched request — so a
// test that must prove a request actually happened (e.g. the task poll a
// wait=true reinstall has to issue) has to observe the requests itself; the
// response-based state assertions alone would replay green even if that request
// was skipped.
type RequestLog struct {
	mu       sync.Mutex
	requests []string // "METHOD url"
}

// Add records a single request.
func (l *RequestLog) Add(method, url string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.requests = append(l.requests, method+" "+url)
}

// Contains reports whether a request with the given method and URL was made.
func (l *RequestLog) Contains(method, url string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range l.requests {
		if r == method+" "+url {
			return true
		}
	}
	return false
}

// Len returns the number of requests made.
func (l *RequestLog) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.requests)
}

// String renders the recorded requests, one per line, for error messages.
func (l *RequestLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.requests, "\n")
}

// loggingTransport records each outgoing request's method+URL before delegating
// to the go-vcr recorder.
type loggingTransport struct {
	inner http.RoundTripper
	log   *RequestLog
}

func (t *loggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.log.Add(req.Method, req.URL.String())
	return t.inner.RoundTrip(req)
}

// NewRequestLoggingClient is NewClient plus a transport shim that records the
// method+URL of every request made through the go-vcr recorder into log. It
// reuses newRecorder so the cassette, matcher, and save-time scrub are wired
// identically to every other replay test. Like newCapturingClient, callers must
// not replace the recorder transport or the endpoint (see NewClient's doc
// comment); a caller-provided extraOpt wins on conflict.
func NewRequestLoggingClient(t *testing.T, cassetteName string, log *RequestLog, extraOpts ...netcup.Option) *netcup.Client {
	t.Helper()
	rec, token := newRecorder(t, cassetteName)
	opts := []netcup.Option{
		netcup.WithAPIEndpoint(netcup.DefaultAPIEndpoint),
		netcup.WithHTTPClient(&http.Client{Transport: &loggingTransport{inner: rec, log: log}, Timeout: clientTimeout}),
		netcup.WithAccessToken(token),
	}
	opts = append(opts, extraOpts...)
	return netcup.New(opts...)
}
