package hartransport

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
)

// DefaultMatchHeaders is the default set of request headers used when
// deciding whether a replay request matches a recorded entry. Header
// names are matched case-insensitively.
var DefaultMatchHeaders = []string{
	"Accept",
	"Accept-Language",
	"Authorization",
	"Content-Type",
	"Cookie",
	"Origin",
	"Referer",
	"User-Agent",
	"X-Requested-With",
}

// FieldDifference describes one mismatched field between a replay
// request and its closest recorded entry.
type FieldDifference struct {
	// Field is "method", "url", "body", or "header:<Name>".
	Field string
	// Expected is the value recorded in the HAR.
	Expected string
	// Actual is the value from the replay request.
	Actual string
}

// MatchError is returned when no unconsumed recorded entry matches the
// replay request. It identifies the closest candidate and every field
// that differs.
type MatchError struct {
	// Method and URL describe the unmatched replay request.
	Method string
	URL    string

	// ClosestIndex is the index of the closest candidate, or -1 when
	// the HAR contains no entries at all.
	ClosestIndex int
	// Closest is the closest recorded entry, or nil when no entries
	// exist.
	Closest *Entry
	// AlreadyConsumed reports whether the closest candidate was already
	// replayed (the same request appeared more times than recorded).
	AlreadyConsumed bool

	// Differences lists every mismatched field against the closest
	// candidate.
	Differences []FieldDifference
}

func (e *MatchError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "hartransport: no recorded entry matches %s %s", e.Method, e.URL)
	if e.Closest == nil {
		b.WriteString(" (HAR contains no entries)")
		return b.String()
	}
	fmt.Fprintf(&b, "; closest candidate is entry #%d %s %s",
		e.ClosestIndex, e.Closest.Request.Method, e.Closest.Request.URL)
	if e.AlreadyConsumed {
		b.WriteString(" (already consumed)")
	}
	for _, d := range e.Differences {
		fmt.Fprintf(&b, "\n  - %s: expected %q, got %q", d.Field, d.Expected, d.Actual)
	}
	return b.String()
}

// ReplayTransport is an http.RoundTripper that serves requests from a
// recorded HAR instead of performing real network calls. It is safe for
// concurrent use.
type ReplayTransport struct {
	// MatchHeaders lists the request headers included in matching. When
	// nil, DefaultMatchHeaders is used. Header names are
	// case-insensitive; headers absent from both the request and the
	// recorded entry count as equal.
	MatchHeaders []string

	entries  []replayEntry
	consumed []bool
	mu       sync.Mutex
}

type replayEntry struct {
	entry      Entry
	normalized string
	reqHeaders http.Header
	reqBody    []byte
}

var _ http.RoundTripper = (*ReplayTransport)(nil)

// NewReplayTransport builds a replay transport from a parsed HAR.
func NewReplayTransport(h *HAR) *ReplayTransport {
	entries := make([]replayEntry, len(h.Log.Entries))
	for i, e := range h.Log.Entries {
		entries[i] = newReplayEntry(e)
	}
	return &ReplayTransport{
		entries:  entries,
		consumed: make([]bool, len(entries)),
	}
}

// NewReplayTransportFromReader parses HAR JSON from r.
func NewReplayTransportFromReader(r io.Reader) (*ReplayTransport, error) {
	var h HAR
	if err := json.NewDecoder(r).Decode(&h); err != nil {
		return nil, fmt.Errorf("hartransport: parsing HAR: %w", err)
	}
	return NewReplayTransport(&h), nil
}

// NewReplayTransportFromFile reads and parses a HAR file.
func NewReplayTransportFromFile(path string) (*ReplayTransport, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("hartransport: opening HAR: %w", err)
	}
	defer f.Close()
	return NewReplayTransportFromReader(f)
}

func newReplayEntry(e Entry) replayEntry {
	normalized, err := normalizeURL(e.Request.URL)
	if err != nil {
		normalized = e.Request.URL
	}
	headers := make(http.Header, len(e.Request.Headers))
	for _, h := range e.Request.Headers {
		headers.Add(h.Name, h.Value)
	}
	var body []byte
	if e.Request.PostData != nil {
		body = decodeHARBody(e.Request.PostData.Text, e.Request.PostData.Comment)
	}
	return replayEntry{
		entry:      e,
		normalized: normalized,
		reqHeaders: headers,
		reqBody:    body,
	}
}

// RoundTrip matches the request against the recorded entries and returns
// the recorded response. Matching considers the HTTP method, a
// normalized URL, the configured request headers, and the request body.
// When the same request occurs multiple times in the HAR, entries are
// consumed in recording order. A failed match returns *MatchError.
func (t *ReplayTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := readAndRestoreBody(&req.Body)
	if err != nil {
		return nil, err
	}

	actualURL, err := normalizeURL(req.URL.String())
	if err != nil {
		actualURL = req.URL.String()
	}
	matchHeaders := t.MatchHeaders
	if matchHeaders == nil {
		matchHeaders = DefaultMatchHeaders
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	for i := range t.entries {
		if t.consumed[i] {
			continue
		}
		if t.entries[i].matches(req, body, actualURL, matchHeaders) {
			t.consumed[i] = true
			return buildResponse(t.entries[i].entry, req)
		}
	}

	return nil, t.matchError(req, body, actualURL, matchHeaders)
}

func (e replayEntry) matches(req *http.Request, body []byte, normalizedURL string, matchHeaders []string) bool {
	return len(e.differences(req, body, normalizedURL, matchHeaders)) == 0
}

// differences computes every mismatched field against the candidate.
func (e replayEntry) differences(req *http.Request, body []byte, normalizedURL string, matchHeaders []string) []FieldDifference {
	var diffs []FieldDifference

	if e.entry.Request.Method != req.Method {
		diffs = append(diffs, FieldDifference{
			Field: "method", Expected: e.entry.Request.Method, Actual: req.Method,
		})
	}

	if e.normalized != normalizedURL {
		diffs = append(diffs, FieldDifference{
			Field: "url", Expected: e.entry.Request.URL, Actual: req.URL.String(),
		})
	}

	for _, name := range matchHeaders {
		want := e.reqHeaders.Get(name)
		got := req.Header.Get(name)
		if want != got {
			diffs = append(diffs, FieldDifference{
				Field: "header:" + http.CanonicalHeaderKey(name), Expected: want, Actual: got,
			})
		}
	}

	if !bytes.Equal(e.reqBody, body) {
		diffs = append(diffs, FieldDifference{
			Field: "body", Expected: previewBody(e.reqBody), Actual: previewBody(body),
		})
	}

	return diffs
}

func (t *ReplayTransport) matchError(req *http.Request, body []byte, normalizedURL string, matchHeaders []string) error {
	mErr := &MatchError{
		Method:       req.Method,
		URL:          req.URL.String(),
		ClosestIndex: -1,
	}

	allDiffs := make([][]FieldDifference, len(t.entries))
	bestCount := -1
	for i := range t.entries {
		diffs := t.entries[i].differences(req, body, normalizedURL, matchHeaders)
		allDiffs[i] = diffs
		if bestCount == -1 || len(diffs) < bestCount {
			bestCount = len(diffs)
		}
	}
	for i, diffs := range allDiffs {
		if len(diffs) != bestCount {
			continue
		}
		// Among equally close candidates, prefer an already consumed
		// exact match so repeated-request exhaustion points at the
		// correct entry.
		if mErr.ClosestIndex == -1 ||
			(t.consumed[i] && !t.consumed[mErr.ClosestIndex]) {
			mErr.ClosestIndex = i
			mErr.Differences = diffs
		}
	}

	if mErr.ClosestIndex >= 0 {
		entry := t.entries[mErr.ClosestIndex].entry
		mErr.Closest = &entry
		mErr.AlreadyConsumed = t.consumed[mErr.ClosestIndex]
	}
	return mErr
}

// buildResponse reconstructs the recorded http.Response, preserving all
// recorded headers (including multiple Set-Cookie fields) and the
// Location header, so redirects and cookies behave as recorded.
func buildResponse(e Entry, req *http.Request) (*http.Response, error) {
	body := decodeHARBody(e.Response.Content.Text, e.Response.Content.Encoding)

	header := make(http.Header, len(e.Response.Headers))
	for _, h := range e.Response.Headers {
		if http.CanonicalHeaderKey(h.Name) == "Transfer-Encoding" {
			continue
		}
		header.Add(h.Name, h.Value)
	}

	major, minor := parseHTTPVersion(e.Response.HTTPVersion)
	statusText := e.Response.StatusText
	if statusText == "" {
		statusText = http.StatusText(e.Response.Status)
	}

	resp := &http.Response{
		Status:        strconv.Itoa(e.Response.Status) + " " + statusText,
		StatusCode:    e.Response.Status,
		Proto:         e.Response.HTTPVersion,
		ProtoMajor:    major,
		ProtoMinor:    minor,
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
	return resp, nil
}

func parseHTTPVersion(version string) (major, minor int) {
	major, minor = 1, 1
	rest, ok := strings.CutPrefix(version, "HTTP/")
	if !ok {
		return
	}
	majorStr, minorStr, found := strings.Cut(rest, ".")
	if !found {
		return
	}
	if n, err := strconv.Atoi(majorStr); err == nil {
		major = n
	}
	if n, err := strconv.Atoi(minorStr); err == nil {
		minor = n
	}
	return
}

func decodeHARBody(text, encoding string) []byte {
	if encoding == "base64" {
		decoded, err := base64.StdEncoding.DecodeString(text)
		if err == nil {
			return decoded
		}
	}
	return []byte(text)
}

// normalizeURL canonicalizes a URL so equivalent spellings match:
// lower-cased scheme and host, default ports removed, empty path
// normalized to "/", fragments dropped, and query parameters sorted.
func normalizeURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Host = stripDefaultPort(u.Scheme, u.Host)
	u.Fragment = ""
	if u.Path == "" {
		u.Path = "/"
	}
	if u.RawQuery != "" {
		u.RawQuery = u.Query().Encode()
	}
	return u.String(), nil
}

func stripDefaultPort(scheme, host string) string {
	hostname, port, found := strings.Cut(host, ":")
	if !found {
		return host
	}
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		return hostname
	}
	return host
}

const maxPreviewLength = 200

func previewBody(body []byte) string {
	if len(body) <= maxPreviewLength {
		return string(body)
	}
	return string(body[:maxPreviewLength]) + "...(truncated)"
}
