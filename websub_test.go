package netsec_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	netsec "example.org/netsec/q0421"
)

const (
	testTopic    = "https://example.com/feed"
	testCallback = "https://callback.example/hook"
)

type callbackRecord struct {
	method string
	query  url.Values
	header http.Header
	body   []byte
}

type callbackRecorder struct {
	mu      sync.Mutex
	records []callbackRecord
}

func (r *callbackRecorder) add(record callbackRecord) {
	r.mu.Lock()
	r.records = append(r.records, record)
	r.mu.Unlock()
}

func (r *callbackRecorder) snapshot() []callbackRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]callbackRecord(nil), r.records...)
}

type fakeResponse struct {
	status int
	body   []byte
	header http.Header
}

// fakeTransport models the callback endpoint without binding a network
// listener, which also keeps the tests usable in restricted sandboxes.
type fakeTransport struct {
	recorder *callbackRecorder
	respond  func(*http.Request) fakeResponse
}

func (f *fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		_ = req.Body.Close()
	}
	if f.recorder != nil {
		f.recorder.add(callbackRecord{
			method: req.Method,
			query:  req.URL.Query(),
			header: req.Header.Clone(),
			body:   body,
		})
	}
	response := f.respond(req)
	header := response.header
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{
		StatusCode: response.status,
		Header:     header,
		Body:       io.NopCloser(bytes.NewReader(response.body)),
		Request:    req,
	}, nil
}

func testHub(t *testing.T, recorder *callbackRecorder, respond func(*http.Request) fakeResponse) *netsec.Manager {
	t.Helper()
	client := &http.Client{Transport: &fakeTransport{recorder: recorder, respond: respond}}
	return netsec.NewHub(
		netsec.WithHTTPClient(client),
		netsec.WithAllowPrivateCallbacks(),
	)
}

func echoChallenge(verifications *atomic.Int32) func(*http.Request) fakeResponse {
	return func(req *http.Request) fakeResponse {
		switch req.Method {
		case http.MethodGet:
			if verifications != nil {
				verifications.Add(1)
			}
			return fakeResponse{
				status: http.StatusOK,
				body:   []byte(req.URL.Query().Get("hub.challenge")),
			}
		case http.MethodPost:
			return fakeResponse{status: http.StatusNoContent}
		default:
			return fakeResponse{status: http.StatusMethodNotAllowed}
		}
	}
}

func postSubscription(t *testing.T, hub *netsec.Manager, callback, topic string, lease int, secret string) *http.Response {
	t.Helper()
	form := url.Values{
		"hub.mode":     {"subscribe"},
		"hub.callback": {callback},
		"hub.topic":    {topic},
	}
	if lease != 0 {
		form.Set("hub.lease_seconds", strconv.Itoa(lease))
	}
	if secret != "" {
		form.Set("hub.secret", secret)
	}

	request, err := http.NewRequest(http.MethodPost, "/hub", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := &captureResponse{header: make(http.Header)}
	hub.ServeHTTP(response, request)
	return response.result()
}

type captureResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (r *captureResponse) Header() http.Header {
	return r.header
}

func (r *captureResponse) WriteHeader(status int) {
	r.status = status
}

func (r *captureResponse) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(body)
}

func (r *captureResponse) result() *http.Response {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return &http.Response{
		StatusCode: r.status,
		Header:     r.header,
		Body:       io.NopCloser(bytes.NewReader(r.body.Bytes())),
	}
}

func TestSuccessfulSubscriptionVerification(t *testing.T) {
	recorder := &callbackRecorder{}
	var verifications atomic.Int32
	hub := testHub(t, recorder, echoChallenge(&verifications))

	response := postSubscription(t, hub, testCallback, testTopic, 300, "top-secret")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("subscription status = %d, want 202", response.StatusCode)
	}
	hub.Wait()

	subscription, ok := hub.GetSubscription(testCallback, testTopic)
	if !ok {
		t.Fatal("verified subscription was not stored")
	}
	if subscription.LeaseSeconds != 300 {
		t.Fatalf("lease = %d, want 300", subscription.LeaseSeconds)
	}
	if subscription.Secret != "top-secret" {
		t.Fatalf("secret = %q, want top-secret", subscription.Secret)
	}
	if got := hub.SubscriptionCount(); got != 1 {
		t.Fatalf("subscription count = %d, want 1", got)
	}

	records := recorder.snapshot()
	if len(records) != 1 {
		t.Fatalf("callback count = %d, want 1", len(records))
	}
	record := records[0]
	if record.method != http.MethodGet {
		t.Fatalf("callback method = %s, want GET", record.method)
	}
	if got := record.query.Get("hub.mode"); got != "subscribe" {
		t.Fatalf("hub.mode = %q", got)
	}
	if got := record.query.Get("hub.topic"); got != testTopic {
		t.Fatalf("hub.topic = %q", got)
	}
	if got := record.query.Get("hub.lease_seconds"); got != "300" {
		t.Fatalf("hub.lease_seconds = %q", got)
	}
	challenge := record.query.Get("hub.challenge")
	decoded, err := hex.DecodeString(challenge)
	if err != nil || len(decoded) < netsec.ChallengeBytes {
		t.Fatalf("challenge %q is not at least %d random bytes in hex: %v", challenge, netsec.ChallengeBytes, err)
	}
}

func TestAcknowledgesBeforeCallingCallback(t *testing.T) {
	callbackStarted := make(chan struct{})
	releaseCallback := make(chan struct{})
	var once sync.Once
	hub := testHub(t, nil, func(req *http.Request) fakeResponse {
		once.Do(func() { close(callbackStarted) })
		<-releaseCallback
		return fakeResponse{
			status: http.StatusOK,
			body:   []byte(req.URL.Query().Get("hub.challenge")),
		}
	})

	acknowledged := make(chan int, 1)
	go func() {
		acknowledged <- postSubscription(t, hub, testCallback, testTopic, 100, "").StatusCode
	}()

	select {
	case status := <-acknowledged:
		if status != http.StatusAccepted {
			t.Fatalf("status = %d, want 202", status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("hub did not acknowledge before the callback completed")
	}

	select {
	case <-callbackStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("callback was not invoked after acknowledgement")
	}
	close(releaseCallback)
	hub.Wait()
}

func TestWrongChallengeDoesNotActivateSubscription(t *testing.T) {
	recorder := &callbackRecorder{}
	hub := testHub(t, recorder, func(req *http.Request) fakeResponse {
		return fakeResponse{status: http.StatusOK, body: []byte("not-the-challenge")}
	})

	response := postSubscription(t, hub, testCallback, testTopic, 100, "")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", response.StatusCode)
	}
	hub.Wait()
	if _, ok := hub.GetSubscription(testCallback, testTopic); ok {
		t.Fatal("subscription was activated despite an incorrect challenge")
	}
	if got := hub.SubscriptionCount(); got != 0 {
		t.Fatalf("subscription count = %d, want 0", got)
	}
}

func TestNon2XXCallbackDoesNotActivateSubscription(t *testing.T) {
	hub := testHub(t, nil, func(req *http.Request) fakeResponse {
		return fakeResponse{status: http.StatusInternalServerError}
	})

	if response := postSubscription(t, hub, testCallback, testTopic, 100, ""); response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", response.StatusCode)
	}
	hub.Wait()
	if _, ok := hub.GetSubscription(testCallback, testTopic); ok {
		t.Fatal("subscription was activated after a non-2xx callback")
	}
}

func TestPrivateCallbackRejected(t *testing.T) {
	const privateCallback = "https://127.0.0.1/hook"
	var calls atomic.Int32
	hub := netsec.NewHub(
		netsec.WithHTTPClient(&http.Client{Transport: &fakeTransport{
			respond: func(req *http.Request) fakeResponse {
				calls.Add(1)
				return fakeResponse{status: http.StatusOK}
			},
		}}),
	)

	response := postSubscription(t, hub, privateCallback, testTopic, 100, "")
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.StatusCode)
	}
	if calls.Load() != 0 {
		t.Fatal("private callback was contacted")
	}
}

func TestNonHTTPSAndOversizedSecretRejected(t *testing.T) {
	hub := testHub(t, nil, echoChallenge(nil))

	insecure := strings.Replace(testCallback, "https://", "http://", 1)
	if response := postSubscription(t, hub, insecure, testTopic, 100, ""); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("non-HTTPS status = %d, want 400", response.StatusCode)
	}

	secret := strings.Repeat("s", 200)
	if response := postSubscription(t, hub, testCallback, testTopic, 100, secret); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("200-byte secret status = %d, want 400", response.StatusCode)
	}

	longSecret := strings.Repeat("s", 199)
	if response := postSubscription(t, hub, testCallback, testTopic, 100, longSecret); response.StatusCode != http.StatusAccepted {
		t.Fatalf("199-byte secret status = %d, want 202", response.StatusCode)
	}
	hub.Wait()
}

func TestLeaseSecondsCapped(t *testing.T) {
	recorder := &callbackRecorder{}
	hub := testHub(t, recorder, echoChallenge(nil))

	response := postSubscription(t, hub, testCallback, testTopic, 999999, "")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", response.StatusCode)
	}
	hub.Wait()

	records := recorder.snapshot()
	if len(records) != 1 {
		t.Fatalf("callback count = %d, want 1", len(records))
	}
	if got := records[0].query.Get("hub.lease_seconds"); got != "864000" {
		t.Fatalf("callback lease = %q, want 864000", got)
	}
	subscription, ok := hub.GetSubscription(testCallback, testTopic)
	if !ok {
		t.Fatal("subscription missing")
	}
	if subscription.LeaseSeconds != netsec.MaxLeaseSeconds {
		t.Fatalf("stored lease = %d, want %d", subscription.LeaseSeconds, netsec.MaxLeaseSeconds)
	}
	if got := subscription.ExpiresAt.Sub(subscription.UpdatedAt); got != time.Duration(netsec.MaxLeaseSeconds)*time.Second {
		t.Fatalf("expiry - update = %v, want capped lease", got)
	}
}

func TestRepeatedSubscriptionOnlyUpdatesLease(t *testing.T) {
	recorder := &callbackRecorder{}
	hub := testHub(t, recorder, echoChallenge(nil))

	first := postSubscription(t, hub, testCallback, testTopic, 100, "")
	if first.StatusCode != http.StatusAccepted {
		t.Fatalf("first status = %d", first.StatusCode)
	}
	hub.Wait()
	firstSub, ok := hub.GetSubscription(testCallback, testTopic)
	if !ok {
		t.Fatal("first subscription missing")
	}

	second := postSubscription(t, hub, testCallback, testTopic, 500, "")
	if second.StatusCode != http.StatusAccepted {
		t.Fatalf("second status = %d", second.StatusCode)
	}
	hub.Wait()

	if got := hub.SubscriptionCount(); got != 1 {
		t.Fatalf("subscription count = %d, want 1", got)
	}
	updated, ok := hub.GetSubscription(testCallback, testTopic)
	if !ok {
		t.Fatal("updated subscription missing")
	}
	if updated.LeaseSeconds != 500 {
		t.Fatalf("lease = %d, want 500", updated.LeaseSeconds)
	}
	if !updated.CreatedAt.Equal(firstSub.CreatedAt) {
		t.Fatalf("created time changed from %v to %v", firstSub.CreatedAt, updated.CreatedAt)
	}
}

func TestUnverifiedSubscriptionFloodIsLimited(t *testing.T) {
	var calls atomic.Int32
	hub := testHub(t, nil, func(req *http.Request) fakeResponse {
		calls.Add(1)
		return fakeResponse{status: http.StatusBadGateway}
	})

	for i := 0; i < netsec.MaxUnverifiedPerHour; i++ {
		response := postSubscription(t, hub, testCallback, testTopic+"?attempt="+strconv.Itoa(i), 100, "")
		if response.StatusCode != http.StatusAccepted {
			t.Fatalf("attempt %d status = %d, want 202", i+1, response.StatusCode)
		}
	}
	response := postSubscription(t, hub, testCallback, testTopic+"?attempt=21", 100, "")
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("21st attempt status = %d, want 429", response.StatusCode)
	}
	if got := response.Header.Get("Retry-After"); got != "3600" {
		t.Fatalf("Retry-After = %q, want 3600", got)
	}

	hub.Wait()
	if got := calls.Load(); got != netsec.MaxUnverifiedPerHour {
		t.Fatalf("callback calls = %d, want %d", got, netsec.MaxUnverifiedPerHour)
	}
	if got := hub.SubscriptionCount(); got != 0 {
		t.Fatalf("subscription count = %d, want 0", got)
	}
}

func TestHMACSignatureAndDistribution(t *testing.T) {
	const secret = "key"
	const payload = "The quick brown fox jumps over the lazy dog"
	expected := "sha256=f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8"
	if got := netsec.Signature(secret, []byte(payload)); got != expected {
		t.Fatalf("signature = %q, want %q", got, expected)
	}

	recorder := &callbackRecorder{}
	hub := testHub(t, recorder, echoChallenge(nil))
	if response := postSubscription(t, hub, testCallback, testTopic, 100, secret); response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", response.StatusCode)
	}
	hub.Wait()

	results := hub.Distribute(context.Background(), testTopic, []byte(payload))
	if len(results) != 1 {
		t.Fatalf("delivery results = %d, want 1", len(results))
	}
	if results[0].Err != nil || results[0].StatusCode != http.StatusNoContent {
		t.Fatalf("delivery result = %+v", results[0])
	}

	records := recorder.snapshot()
	if len(records) != 2 {
		t.Fatalf("callback records = %d, want 2", len(records))
	}
	post := records[1]
	if post.method != http.MethodPost {
		t.Fatalf("distribution method = %s, want POST", post.method)
	}
	if got := post.header.Get(netsec.SignatureHeader); got != expected {
		t.Fatalf("X-Hub-Signature = %q, want %q", got, expected)
	}
	if !bytes.Equal(post.body, []byte(payload)) {
		t.Fatalf("distribution body = %q", post.body)
	}
}

func TestChallengeIsRandom(t *testing.T) {
	recorder := &callbackRecorder{}
	hub := testHub(t, recorder, echoChallenge(nil))

	for i := 0; i < 2; i++ {
		if response := postSubscription(t, hub, testCallback, testTopic+"?n="+strconv.Itoa(i), 100, ""); response.StatusCode != http.StatusAccepted {
			t.Fatalf("status = %d", response.StatusCode)
		}
		hub.Wait()
	}
	records := recorder.snapshot()
	if len(records) != 2 {
		t.Fatalf("callback count = %d, want 2", len(records))
	}
	if records[0].query.Get("hub.challenge") == records[1].query.Get("hub.challenge") {
		t.Fatal("challenges were reused")
	}
}
