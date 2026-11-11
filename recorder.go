package hartransport

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const harVersion = "1.2"

// Recorder is an http.RoundTripper that delegates every request to an
// underlying RoundTripper and records a HAR entry for each successful
// exchange. A zero-value Recorder uses http.DefaultTransport as its
// underlying transport.
type Recorder struct {
	// Next is the wrapped RoundTripper. When nil, http.DefaultTransport
	// is used on the first request.
	Next http.RoundTripper

	// CreatorName and CreatorVersion identify the producer in the HAR
	// document's log.creator field.
	CreatorName    string
	CreatorVersion string

	entries []Entry
}

var _ http.RoundTripper = (*Recorder)(nil)

// RoundTrip executes the request using the wrapped transport and records
// the request/response pair. Errors from the wrapped transport are
// propagated without recording an entry.
func (r *Recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()

	reqBody, err := readAndRestoreBody(&req.Body)
	if err != nil {
		return nil, err
	}

	next := r.Next
	if next == nil {
		next = http.DefaultTransport
	}
	resp, err := next.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	respBody, err := readAndRestoreBody(&resp.Body)
	if err != nil {
		return nil, err
	}

	entry := Entry{
		StartedDateTime: start.UTC().Format(time.RFC3339Nano),
		Time:            float64(time.Since(start).Microseconds()) / 1000,
		Request:         buildHARRequest(req, reqBody),
		Response:        buildHARResponse(resp, respBody),
		Cache:           Cache{},
		Timings:         Timings{},
	}
	r.entries = append(r.entries, entry)
	return resp, nil
}

// Entries returns the recorded entries in completion order.
func (r *Recorder) Entries() []Entry {
	out := make([]Entry, len(r.entries))
	copy(out, r.entries)
	return out
}

// HAR assembles the recorded entries into a HAR 1.2 document.
func (r *Recorder) HAR() *HAR {
	name := r.CreatorName
	if name == "" {
		name = "hartransport.Recorder"
	}
	return &HAR{
		Log: Log{
			Version: harVersion,
			Creator: Creator{Name: name, Version: r.CreatorVersion},
			Pages:   []Page{},
			Entries: r.Entries(),
		},
	}
}

// WriteHAR writes the recorded entries as indented, valid HAR 1.2 JSON.
func (r *Recorder) WriteHAR(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(r.HAR())
}

func buildHARRequest(req *http.Request, body []byte) Request {
	harReq := Request{
		Method:      req.Method,
		URL:         req.URL.String(),
		HTTPVersion: httpVersion(req.ProtoMajor, req.ProtoMinor),
		Cookies:     requestCookies(req),
		Headers:     headerList(req.Header),
		QueryString: queryList(req.URL),
		HeadersSize: -1,
		BodySize:    len(body),
	}
	if body != nil {
		text, encoding := encodeBody(body)
		harReq.PostData = &PostData{
			MimeType: req.Header.Get("Content-Type"),
			Text:     text,
		}
		if encoding != "" {
			harReq.PostData.Comment = "encoding: " + encoding
		}
	}
	return harReq
}

func buildHARResponse(resp *http.Response, body []byte) Response {
	text, encoding := encodeBody(body)
	statusText := ""
	if parts := strings.SplitN(resp.Status, " ", 2); len(parts) == 2 {
		statusText = parts[1]
	}
	return Response{
		Status:      resp.StatusCode,
		StatusText:  statusText,
		HTTPVersion: httpVersion(resp.ProtoMajor, resp.ProtoMinor),
		Cookies:     responseCookies(resp),
		Headers:     headerList(resp.Header),
		Content: Content{
			Size:     len(body),
			MimeType: resp.Header.Get("Content-Type"),
			Text:     text,
			Encoding: encoding,
		},
		RedirectURL: resp.Header.Get("Location"),
		HeadersSize: -1,
		BodySize:    len(body),
	}
}

func httpVersion(major, minor int) string {
	if major == 0 {
		major, minor = 1, 1
	}
	return "HTTP/" + strconv.Itoa(major) + "." + strconv.Itoa(minor)
}

func headerList(h http.Header) []Header {
	out := make([]Header, 0, len(h))
	for name, values := range h {
		for _, value := range values {
			out = append(out, Header{Name: name, Value: value})
		}
	}
	return out
}

func requestCookies(req *http.Request) []Cookie {
	cookies := req.Cookies()
	out := make([]Cookie, 0, len(cookies))
	for _, c := range cookies {
		out = append(out, Cookie{Name: c.Name, Value: c.Value})
	}
	return out
}

func responseCookies(resp *http.Response) []Cookie {
	cookies := resp.Cookies()
	out := make([]Cookie, 0, len(cookies))
	for _, c := range cookies {
		hc := Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Path:     c.Path,
			Domain:   c.Domain,
			HTTPOnly: c.HttpOnly,
			Secure:   c.Secure,
		}
		if !c.Expires.IsZero() {
			hc.Expires = c.Expires.UTC().Format(time.RFC3339Nano)
		}
		out = append(out, hc)
	}
	return out
}

func queryList(u *url.URL) []Query {
	raw := u.RawQuery
	if raw == "" {
		return []Query{}
	}
	parts := strings.Split(raw, "&")
	out := make([]Query, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		name, value, found := strings.Cut(part, "=")
		decName, err := url.QueryUnescape(name)
		if err != nil {
			decName = name
		}
		if !found {
			out = append(out, Query{Name: decName})
			continue
		}
		decValue, err := url.QueryUnescape(value)
		if err != nil {
			decValue = value
		}
		out = append(out, Query{Name: decName, Value: decValue})
	}
	return out
}

// encodeBody returns the HAR text/encoding pair for a body. UTF-8 bodies
// are stored verbatim; binary bodies are base64-encoded.
func encodeBody(body []byte) (text, encoding string) {
	if utf8.Valid(body) {
		return string(body), ""
	}
	return base64.StdEncoding.EncodeToString(body), "base64"
}

// readAndRestoreBody fully reads *body (if non-nil), restores it with a
// fresh reader so the wrapped transport / caller can still consume it,
// and returns the bytes. A nil body yields nil bytes.
func readAndRestoreBody(body *io.ReadCloser) ([]byte, error) {
	if *body == nil {
		return nil, nil
	}
	data, err := io.ReadAll(*body)
	(*body).Close()
	if err != nil {
		return nil, err
	}
	*body = io.NopCloser(bytes.NewReader(data))
	return data, nil
}
