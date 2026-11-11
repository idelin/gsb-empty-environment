package hartransport

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"testing"
)

const (
	binaryBody = "\x00\x01\x02\xff\xfe binary \x00"
	formBody   = "name=gopher&role=engineer"
)

const testServerURL = "http://hartransport.test"

// fakeServer is an in-memory RoundTripper that replaces httptest so the
// tests need no socket permissions.
type fakeServer struct{}

func (fakeServer) RoundTrip(req *http.Request) (*http.Response, error) {
	return handlerTransport(req)
}

func handlerTransport(req *http.Request) (*http.Response, error) {
	mux := http.NewServeMux()

	mux.HandleFunc("/get", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "get ok: "+r.URL.Query().Get("q"))
	})

	mux.HandleFunc("/post", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "post ok: "+string(body))
	})

	mux.HandleFunc("/binary", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte(binaryBody))
	})

	mux.HandleFunc("/redirect-start", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "abc123", Path: "/"})
		http.Redirect(w, r, "/redirect-target", http.StatusFound)
	})

	mux.HandleFunc("/redirect-target", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "reached target; cookie="+r.Header.Get("Cookie"))
	})

	mux.HandleFunc("/repeat", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "repeat")
	})

	rr := newResponseRecorder()
	mux.ServeHTTP(rr, req)
	return rr.result(req), nil
}

// responseRecorder is a minimal http.ResponseWriter capturing headers,
// status and body for reconstruction into an *http.Response.
type responseRecorder struct {
	header     http.Header
	body       bytes.Buffer
	statusCode int
}

func newResponseRecorder() *responseRecorder {
	return &responseRecorder{header: make(http.Header), statusCode: http.StatusOK}
}

func (r *responseRecorder) Header() http.Header { return r.header }

func (r *responseRecorder) Write(p []byte) (int, error) { return r.body.Write(p) }

func (r *responseRecorder) WriteHeader(statusCode int) { r.statusCode = statusCode }

func (r *responseRecorder) result(req *http.Request) *http.Response {
	return &http.Response{
		Status:        http.StatusText(r.statusCode),
		StatusCode:    r.statusCode,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        r.header.Clone(),
		Body:          io.NopCloser(bytes.NewReader(r.body.Bytes())),
		ContentLength: int64(r.body.Len()),
		Request:       req,
	}
}

func recordHAR(t *testing.T) *HAR {
	t.Helper()
	rec := &Recorder{
		Next:           fakeServer{},
		CreatorName:    "hartransport-test",
		CreatorVersion: "1.0",
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	client := &http.Client{Transport: rec, Jar: jar}

	getReq, _ := http.NewRequest(http.MethodGet, testServerURL+"/get?q=hello&a=1", nil)
	getReq.Header.Set("Accept", "text/plain")
	resp, err := client.Do(getReq)
	if err != nil {
		t.Fatalf("GET /get: %v", err)
	}
	if got := readBody(t, resp); got != "get ok: hello" {
		t.Fatalf("GET /get body = %q", got)
	}

	postReq, _ := http.NewRequest(http.MethodPost, testServerURL+"/post", strings.NewReader(formBody))
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err = client.Do(postReq)
	if err != nil {
		t.Fatalf("POST /post: %v", err)
	}
	if got := readBody(t, resp); got != "post ok: "+formBody {
		t.Fatalf("POST /post body = %q", got)
	}

	binReq, _ := http.NewRequest(http.MethodGet, testServerURL+"/binary", nil)
	resp, err = client.Do(binReq)
	if err != nil {
		t.Fatalf("GET /binary: %v", err)
	}
	if got := readBody(t, resp); got != binaryBody {
		t.Fatalf("binary body mismatch: %q", got)
	}

	resp, err = client.Get(testServerURL + "/redirect-start")
	if err != nil {
		t.Fatalf("GET /redirect-start: %v", err)
	}
	redirectBody := readBody(t, resp)
	if !strings.Contains(redirectBody, "reached target") ||
		!strings.Contains(redirectBody, "sid=abc123") {
		t.Fatalf("redirect chain body = %q", redirectBody)
	}

	for i := 0; i < 2; i++ {
		resp, err = client.Get(testServerURL + "/repeat")
		if err != nil {
			t.Fatalf("GET /repeat #%d: %v", i, err)
		}
		if got := readBody(t, resp); got != "repeat" {
			t.Fatalf("repeat body = %q", got)
		}
	}

	return rec.HAR()
}

func TestRoundTripReplay(t *testing.T) {

	har := recordHAR(t)
	if len(har.Log.Entries) < 6 {
		t.Fatalf("recorded %d entries, want >= 6", len(har.Log.Entries))
	}

	rt := NewReplayTransport(har)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Transport: rt, Jar: jar}

	getReq, _ := http.NewRequest(http.MethodGet, testServerURL+"/get?q=hello&a=1", nil)
	getReq.Header.Set("Accept", "text/plain")
	resp, err := client.Do(getReq)
	if err != nil {
		t.Fatalf("replay GET: %v", err)
	}
	if got := readBody(t, resp); got != "get ok: hello" {
		t.Fatalf("replay GET body = %q", got)
	}

	postReq, _ := http.NewRequest(http.MethodPost, testServerURL+"/post", strings.NewReader(formBody))
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err = client.Do(postReq)
	if err != nil {
		t.Fatalf("replay POST: %v", err)
	}
	if got := readBody(t, resp); got != "post ok: "+formBody {
		t.Fatalf("replay POST body = %q", got)
	}

	resp, err = client.Get(testServerURL + "/binary")
	if err != nil {
		t.Fatalf("replay binary: %v", err)
	}
	if got := readBody(t, resp); got != binaryBody {
		t.Fatalf("replay binary body = %q", got)
	}

	resp, err = client.Get(testServerURL + "/redirect-start")
	if err != nil {
		t.Fatalf("replay redirect: %v", err)
	}
	redirectBody := readBody(t, resp)
	if !strings.Contains(redirectBody, "reached target") ||
		!strings.Contains(redirectBody, "sid=abc123") {
		t.Fatalf("replay redirect body = %q", redirectBody)
	}
	if resp.Request.URL.Path != "/redirect-target" {
		t.Fatalf("did not follow recorded redirect, ended at %s", resp.Request.URL.Path)
	}

	for i := 0; i < 2; i++ {
		resp, err = client.Get(testServerURL + "/repeat")
		if err != nil {
			t.Fatalf("replay repeat #%d: %v", i, err)
		}
		if got := readBody(t, resp); got != "repeat" {
			t.Fatalf("replay repeat body = %q", got)
		}
	}

	// Every recorded entry has now been consumed; a third identical
	// request must fail with the closest candidate marked consumed.
	_, err = client.Get(testServerURL + "/repeat")
	var matchErr *MatchError
	if !errors.As(err, &matchErr) {
		t.Fatalf("third repeat error = %T %v, want *MatchError", err, err)
	}
	if !matchErr.AlreadyConsumed {
		t.Fatalf("expected AlreadyConsumed, got %+v", matchErr)
	}
	if matchErr.Closest == nil || matchErr.Closest.Request.URL != testServerURL+"/repeat" {
		t.Fatalf("unexpected closest entry: %+v", matchErr.Closest)
	}
}

func TestMatchErrorDiffs(t *testing.T) {

	rt := NewReplayTransport(recordHAR(t))
	client := &http.Client{Transport: rt}

	// Wrong method on an existing URL.
	req, _ := http.NewRequest(http.MethodDelete, testServerURL+"/get?q=hello&a=1", nil)
	_, err := client.Do(req)
	var matchErr *MatchError
	if !errors.As(err, &matchErr) {
		t.Fatalf("error = %T, want *MatchError", err)
	}
	if !hasDiff(matchErr, "method") {
		t.Fatalf("missing method diff: %v", matchErr)
	}

	// Wrong body for a POST: body diff plus closest candidate must be
	// the recorded POST.
	wrongPost, _ := http.NewRequest(http.MethodPost, testServerURL+"/post", strings.NewReader("name=wrong"))
	wrongPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_, err = client.Do(wrongPost)
	if !errors.As(err, &matchErr) {
		t.Fatalf("error = %T, want *MatchError", err)
	}
	if !hasDiff(matchErr, "body") {
		t.Fatalf("missing body diff: %v", matchErr)
	}
	if matchErr.Closest.Request.URL != testServerURL+"/post" {
		t.Fatalf("closest = %s, want /post", matchErr.Closest.Request.URL)
	}

	// Unknown URL entirely.
	_, err = client.Get(testServerURL + "/does-not-exist")
	if !errors.As(err, &matchErr) {
		t.Fatalf("error = %T, want *MatchError", err)
	}
	if !hasDiff(matchErr, "url") {
		t.Fatalf("missing url diff: %v", matchErr)
	}
}

func TestSelectedHeaders(t *testing.T) {

	rt := NewReplayTransport(recordHAR(t))
	rt.MatchHeaders = []string{"X-Tenant"}
	client := &http.Client{Transport: rt}

	// /get was recorded with Accept: text/plain, but Accept is no longer
	// part of the match set, so omitting it must still replay.
	resp, err := client.Get(testServerURL + "/get?q=hello&a=1")
	if err != nil {
		t.Fatalf("replay with reduced header set: %v", err)
	}
	if got := readBody(t, resp); got != "get ok: hello" {
		t.Fatalf("body = %q", got)
	}

	// A non-matching selected header must surface as a header diff.
	// Use a fresh transport so candidate selection is undisturbed.
	rt2 := NewReplayTransport(recordHAR(t))
	rt2.MatchHeaders = []string{"X-Tenant"}
	client2 := &http.Client{Transport: rt2}
	req, _ := http.NewRequest(http.MethodGet, testServerURL+"/get?a=1&q=hello", nil)
	req.Header.Set("X-Tenant", "tenant-a")
	_, err = client2.Do(req)
	var matchErr *MatchError
	if !errors.As(err, &matchErr) {
		t.Fatalf("error = %T, want *MatchError", err)
	}
	if !hasDiff(matchErr, "header:X-Tenant") {
		t.Fatalf("missing header diff: %v", matchErr)
	}
}

func TestNormalizedURLMatch(t *testing.T) {

	rt := NewReplayTransport(recordHAR(t))
	client := &http.Client{Transport: rt}

	// Uppercase host, default port, reordered query parameters, and a
	// fragment must all normalize to the recorded /get URL.
	req, _ := http.NewRequest(http.MethodGet,
		strings.ToUpper(testServerURL)+":80/get?a=1&q=hello#frag", nil)
	req.Header.Set("Accept", "text/plain")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("normalized URL replay: %v", err)
	}
	if got := readBody(t, resp); got != "get ok: hello" {
		t.Fatalf("body = %q", got)
	}
}

func TestHARJSONRoundTrip(t *testing.T) {

	rec := &Recorder{Next: fakeServer{}}
	recClient := &http.Client{Transport: rec}
	resp, err := recClient.Get(testServerURL + "/get?q=json")
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	readBody(t, resp)

	var buf bytes.Buffer
	if err := rec.WriteHAR(&buf); err != nil {
		t.Fatalf("WriteHAR: %v", err)
	}

	var decoded HAR
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("HAR is not valid JSON: %v\n%s", err, buf.String())
	}
	if decoded.Log.Version != harVersion {
		t.Fatalf("HAR version = %q, want %q", decoded.Log.Version, harVersion)
	}
	if len(decoded.Log.Entries) != 1 {
		t.Fatalf("decoded entries = %d, want 1", len(decoded.Log.Entries))
	}

	rt, err := NewReplayTransportFromReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("NewReplayTransportFromReader: %v", err)
	}
	replayClient := &http.Client{Transport: rt}
	resp, err = replayClient.Get(testServerURL + "/get?q=json")
	if err != nil {
		t.Fatalf("replay from serialized HAR: %v", err)
	}
	if got := readBody(t, resp); got != "get ok: json" {
		t.Fatalf("replay body = %q", got)
	}
}

func TestSetCookieReplayedFromRecord(t *testing.T) {

	rt := NewReplayTransport(recordHAR(t))
	client := &http.Client{
		Transport: rt,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Get(testServerURL + "/redirect-start")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	if location := resp.Header.Get("Location"); location != "/redirect-target" {
		t.Fatalf("Location = %q, want /redirect-target", location)
	}

	var cookieNames []string
	for _, c := range resp.Cookies() {
		cookieNames = append(cookieNames, c.Name)
	}
	found := false
	for _, name := range cookieNames {
		if name == "sid" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Set-Cookie not replayed, cookies = %v", cookieNames)
	}
}

func TestEmptyHAR(t *testing.T) {
	rt := NewReplayTransport(&HAR{})
	_, err := rt.RoundTrip(mustRequest(http.MethodGet, testServerURL+"/get"))
	var matchErr *MatchError
	if !errors.As(err, &matchErr) {
		t.Fatalf("error = %T, want *MatchError", err)
	}
	if matchErr.ClosestIndex != -1 || matchErr.Closest != nil {
		t.Fatalf("empty HAR should have no candidate, got %+v", matchErr)
	}
	if !strings.Contains(matchErr.Error(), "no entries") {
		t.Fatalf("error message = %q", matchErr.Error())
	}
}

func TestConcurrentReplay(t *testing.T) {
	rt := NewReplayTransport(recordHAR(t))
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Transport: rt, Jar: jar}
	// Prime the jar with the cookie the recording client held.
	req := mustRequest(http.MethodGet, testServerURL+"/redirect-start")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("prime cookie jar: %v", err)
	}
	readBody(t, resp)

	const workers = 8
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := client.Get(testServerURL + "/repeat")
			if err != nil {
				errCh <- err
				return
			}
			if got := readBody(t, resp); got != "repeat" {
				errCh <- errors.New("bad repeat body: " + got)
			}
		}()
	}
	wg.Wait()
	close(errCh)

	var failures int
	for err := range errCh {
		var matchErr *MatchError
		if !errors.As(err, &matchErr) || !matchErr.AlreadyConsumed ||
			matchErr.Closest == nil || matchErr.Closest.Request.URL != testServerURL+"/repeat" {
			t.Errorf("unexpected worker error: %v", err)
		}
		failures++
	}
	// Only two /repeat entries exist, so all but two workers must see a
	// MatchError with the consumed candidate.
	if failures != workers-2 {
		t.Fatalf("failures = %d, want %d", failures, workers-2)
	}
}

func mustRequest(method, target string) *http.Request {
	req, err := http.NewRequest(method, target, nil)
	if err != nil {
		panic(err)
	}
	return req
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(data)
}

func hasDiff(err *MatchError, field string) bool {
	for _, d := range err.Differences {
		if d.Field == field {
			return true
		}
	}
	return false
}
