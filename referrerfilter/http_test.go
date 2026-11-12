package referrerfilter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// testServer is an httptest double of an OCI distribution registry. It records
// every requested path so tests can assert that filtered descriptors never
// generate a manifest fetch and that fallback uses the exact sha256-<hex> tag.
type testServer struct {
	referrersStatus int
	referrers       map[string][]Descriptor
	tagIndexes      map[string][]byte
	manifests       map[string][]byte
	requests        []string
}

func newTestServer() *testServer {
	return &testServer{
		referrersStatus: http.StatusOK,
		referrers:       map[string][]Descriptor{},
		tagIndexes:      map[string][]byte{},
		manifests:       map[string][]byte{},
	}
}

func (s *testServer) handler() http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		s.requests = append(s.requests, request.Method+" "+request.URL.Path)

		path := request.URL.Path
		segments := strings.Split(strings.TrimPrefix(path, "/v2/"), "/")
		switch {
		case len(segments) == 4 && segments[2] == "referrers":
			if s.referrersStatus != http.StatusOK {
				writer.WriteHeader(s.referrersStatus)
				return
			}
			digest := segments[3]
			raw, _ := json.Marshal(indexManifest{
				MediaType: MediaTypeOCIIndex,
				Manifests: s.referrers[digest],
			})
			writer.Header().Set("Content-Type", MediaTypeOCIIndex)
			_, _ = writer.Write(raw)

		case len(segments) == 4 && segments[2] == "manifests":
			reference, err := url.PathUnescape(path[strings.LastIndex(path, "/")+1:])
			if err != nil {
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			if raw, ok := s.manifests[reference]; ok {
				writer.Header().Set("Content-Type", MediaTypeOCIManifest)
				_, _ = writer.Write(raw)
				return
			}
			if raw, ok := s.tagIndexes[reference]; ok {
				writer.Header().Set("Content-Type", MediaTypeOCIIndex)
				_, _ = writer.Write(raw)
				return
			}
			writer.WriteHeader(http.StatusNotFound)

		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}
}

func (s *testServer) requestedPaths() []string {
	return append([]string(nil), s.requests...)
}

func (s *testServer) hasRequest(want string) bool {
	for _, request := range s.requests {
		if request == want {
			return true
		}
	}
	return false
}

// handlerTransport dispatches requests to the test handler without binding a
// socket, which sandboxed environments forbid. The request still traverses
// the real net/http client, response headers and status handling.
type handlerTransport struct {
	handler http.Handler
}

func (t *handlerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	t.handler.ServeHTTP(recorder, request)
	response := recorder.Result()
	response.Request = request
	return response, nil
}

func newHTTPRegistry(t *testing.T, state *testServer) *HTTPRegistry {
	t.Helper()
	return &HTTPRegistry{
		BaseURL:    "https://registry.example.test",
		HTTPClient: &http.Client{Transport: &handlerTransport{handler: state.handler()}},
		Header:     http.Header{"Authorization": []string{"Bearer test-token"}},
	}
}

func TestHTTPRegistryReferrersAPIFlow(t *testing.T) {
	subject := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000020")
	serverState := newTestServer()

	goodRaw, good := buildManifest(t, subject, testArtifactSignature, nil)
	forgedRaw, forged := buildManifest(t,
		mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000021"),
		testArtifactSignature, nil)
	unknownRaw, unknown := buildManifest(t, subject, testArtifactUnknown, nil)

	serverState.referrers[subject.String()] = []Descriptor{unknown, forged, good}
	serverState.manifests[good.Digest.String()] = goodRaw
	serverState.manifests[forged.Digest.String()] = forgedRaw
	serverState.manifests[unknown.Digest.String()] = unknownRaw

	registry := newHTTPRegistry(t, serverState)
	verifier := newRecordingVerifier()
	result, err := FilterAndVerify(
		context.Background(), registry, "example/app", subject.String(),
		NewAllowList(testArtifactSignature), verifier,
	)
	if err != nil {
		t.Fatalf("FilterAndVerify returned error: %v", err)
	}
	if result.FallbackUsed {
		t.Fatal("Referrers API succeeded; fallback must not be used")
	}
	if len(result.Verified) != 1 || result.Verified[0].Descriptor.Digest.String() != good.Digest.String() {
		t.Fatalf("verified = %v", result.Verified)
	}
	if got := sortedStrings(rejectionReasons(result)); !reflect.DeepEqual(got,
		sortedStrings([]string{ReasonArtifactTypeNotAllowed, ReasonSubjectMismatch})) {
		t.Fatalf("rejection reasons = %v", got)
	}
	if !reflect.DeepEqual(verifier.calls, []string{good.Digest.String()}) {
		t.Fatalf("verifier calls = %v", verifier.calls)
	}

	wantReferrers := "GET /v2/example/app/referrers/" + subject.String()
	if !serverState.hasRequest(wantReferrers) {
		t.Fatalf("missing request %q in %v", wantReferrers, serverState.requestedPaths())
	}
	// The disallowed descriptor must never cause a manifest fetch.
	unknownPath := "GET /v2/example/app/manifests/" + unknown.Digest.String()
	if serverState.hasRequest(unknownPath) {
		t.Fatalf("disallowed descriptor was fetched: %v", serverState.requestedPaths())
	}
	forgedPath := "GET /v2/example/app/manifests/" + forged.Digest.String()
	if !serverState.hasRequest(forgedPath) {
		t.Fatalf("forged-subject descriptor should still be fetched for binding check: %v", serverState.requestedPaths())
	}
}

func TestHTTPRegistryFallbackFlow(t *testing.T) {
	subject := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000022")
	serverState := newTestServer()
	serverState.referrersStatus = http.StatusNotFound

	goodRaw, good := buildManifest(t, subject, testArtifactSignature, func(document map[string]any) {
		document["annotations"] = map[string]any{"role": "good"}
	})
	tamperedRaw, tampered := buildManifest(t, subject, testArtifactSignature, func(document map[string]any) {
		document["annotations"] = map[string]any{"role": "tampered"}
	})
	tag := "sha256-" + subject.Hex()
	serverState.tagIndexes[tag] = indexBytes(t, []Descriptor{tampered, good})
	serverState.manifests[good.Digest.String()] = goodRaw
	serverState.manifests[tampered.Digest.String()] = tamper(tamperedRaw)

	registry := newHTTPRegistry(t, serverState)
	result, err := FilterAndVerify(
		context.Background(), registry, "example/app", subject.String(),
		NewAllowList(testArtifactSignature), newRecordingVerifier(),
	)
	if err != nil {
		t.Fatalf("FilterAndVerify returned error: %v", err)
	}
	if !result.FallbackUsed {
		t.Fatal("404 on Referrers API must trigger fallback")
	}
	if len(result.Verified) != 1 {
		t.Fatalf("expected one verified reference, got %d (%v)", len(result.Verified), rejectionReasons(result))
	}
	if got := rejectionReasons(result); !reflect.DeepEqual(got, []string{ReasonDigestMismatch}) {
		t.Fatalf("rejection reasons = %v", got)
	}
	wantTag := "GET /v2/example/app/manifests/" + tag
	if !serverState.hasRequest(wantTag) {
		t.Fatalf("fallback tag request missing: %v", serverState.requestedPaths())
	}
}

func TestHTTPRegistryFallbackTagMissing(t *testing.T) {
	subject := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000023")
	serverState := newTestServer()
	serverState.referrersStatus = http.StatusNotFound

	_, err := FilterAndVerify(
		context.Background(), newHTTPRegistry(t, serverState), "example/app", subject.String(),
		NewAllowList(testArtifactSignature), newRecordingVerifier(),
	)
	if err == nil {
		t.Fatal("expected error when both Referrers API and fallback tag are absent")
	}
	if !strings.Contains(err.Error(), "manifest not found") {
		t.Fatalf("expected wrapped not-found error, got %v", err)
	}
}

func TestHTTPRegistryReferrersServerError(t *testing.T) {
	subject := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000024")
	serverState := newTestServer()
	serverState.referrersStatus = http.StatusInternalServerError

	_, err := FilterAndVerify(
		context.Background(), newHTTPRegistry(t, serverState), "example/app", subject.String(),
		NewAllowList(testArtifactSignature), newRecordingVerifier(),
	)
	if err == nil {
		t.Fatal("expected server error to propagate")
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected HTTPError 500, got %T: %v", err, err)
	}
}

func TestHTTPRegistryDigestRoundTrip(t *testing.T) {
	subject := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000025")
	serverState := newTestServer()

	raw, descriptor := buildManifest(t, subject, testArtifactSignature, nil)
	serverState.manifests[descriptor.Digest.String()] = raw

	registry := newHTTPRegistry(t, serverState)
	fetched, err := registry.FetchManifest(context.Background(), "example/app", descriptor.Digest)
	if err != nil {
		t.Fatalf("FetchManifest returned error: %v", err)
	}
	if !bytes.Equal(fetched, raw) {
		t.Fatal("fetched bytes differ from stored manifest")
	}

	if _, err := registry.FetchManifestByTag(context.Background(), "example/app", "INVALID TAG"); err == nil {
		t.Fatal("expected invalid tag to be rejected before the request")
	}

	badDigest, _ := ParseDigest("sha256:" + strings.Repeat("ff", 32))
	_, err = registry.FetchManifest(context.Background(), "example/app", badDigest)
	if err == nil || !strings.Contains(err.Error(), "manifest not found") {
		t.Fatalf("expected not-found sentinel, got %v", err)
	}
}

func TestParseDigestAcceptsKnownAlgorithms(t *testing.T) {
	valid := []string{
		"sha256:" + strings.Repeat("ab", 32),
		"sha512:" + strings.Repeat("cd", 64),
	}
	for _, value := range valid {
		digest, err := ParseDigest(value)
		if err != nil {
			t.Fatalf("ParseDigest(%q): %v", value, err)
		}
		if digest.String() != value {
			t.Fatalf("round trip = %q, want %q", digest.String(), value)
		}
	}
	invalid := []string{
		"",
		"sha256:short",
		"sha1:" + strings.Repeat("ab", 20),
		"sha256:" + strings.Repeat("zz", 32),
		"sha256" + strings.Repeat("ab", 32),
		fmt.Sprintf("sha256:%s:", strings.Repeat("ab", 32)),
	}
	for _, value := range invalid {
		if _, err := ParseDigest(value); err == nil {
			t.Fatalf("ParseDigest(%q) unexpectedly succeeded", value)
		}
	}
}

func TestDescriptorDigestJSONValidation(t *testing.T) {
	var valid Descriptor
	if err := json.Unmarshal([]byte(`{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:`+strings.Repeat("ab", 32)+`","size":42}`), &valid); err != nil {
		t.Fatalf("unmarshal valid descriptor: %v", err)
	}
	if valid.Digest.Algorithm() != "sha256" || valid.Size != 42 {
		t.Fatalf("descriptor not parsed: %+v", valid)
	}

	for _, payload := range []string{
		`{"digest":""}`,
		`{"digest":"sha256:nope"}`,
		`{"digest":"md5:` + strings.Repeat("ab", 16) + `"}`,
	} {
		var invalid Descriptor
		if err := json.Unmarshal([]byte(payload), &invalid); err == nil {
			t.Fatalf("payload %q should fail digest validation", payload)
		}
	}

	// Round trip preserves the canonical digest form.
	raw, err := json.Marshal(valid)
	if err != nil {
		t.Fatalf("marshal descriptor: %v", err)
	}
	var again Descriptor
	if err := json.Unmarshal(raw, &again); err != nil {
		t.Fatalf("re-unmarshal descriptor: %v", err)
	}
	if again.Digest.String() != valid.Digest.String() {
		t.Fatalf("digest round trip mismatch: %q vs %q", again.Digest, valid.Digest)
	}
}

func TestVerifierFuncAdapter(t *testing.T) {
	called := false
	var verifier Verifier = VerifierFunc(func(_ context.Context, descriptor Descriptor, rawManifest []byte) error {
		called = true
		if descriptor.ArtifactType != testArtifactSignature || len(rawManifest) == 0 {
			t.Fatalf("unexpected verifier input: %+v len=%d", descriptor, len(rawManifest))
		}
		return nil
	})
	subject := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000029")
	raw, descriptor := buildManifest(t, subject, testArtifactSignature, nil)
	if err := verifier.Verify(context.Background(), descriptor, raw); err != nil {
		t.Fatalf("VerifierFunc returned error: %v", err)
	}
	if !called {
		t.Fatal("VerifierFunc adapter did not invoke the wrapped function")
	}
}

func TestValidTagGrammar(t *testing.T) {
	valid := []string{"sha256-" + strings.Repeat("ab", 32), "v1.2.3", "latest_2"}
	invalid := []string{"", strings.Repeat("a", 129), ".leading-dot", "-leading-dash", "has/slash"}
	for _, tag := range valid {
		if !validTag(tag) {
			t.Fatalf("tag %q should be valid", tag)
		}
	}
	for _, tag := range invalid {
		if validTag(tag) {
			t.Fatalf("tag %q should be invalid", tag)
		}
	}
}

func TestHTTPRegistryRejectsUnexpectedManifestContentType(t *testing.T) {
	state := newTestServer()
	subject := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000026")
	raw, descriptor := buildManifest(t, subject, testArtifactSignature, nil)
	state.manifests[descriptor.Digest.String()] = raw
	registry := &HTTPRegistry{
		BaseURL: "https://registry.example.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			recorder := httptest.NewRecorder()
			recorder.Header().Set("Content-Type", "application/octet-stream")
			_, _ = recorder.Write(raw)
			response := recorder.Result()
			response.Request = request
			return response, nil
		})},
	}
	if _, err := registry.FetchManifest(context.Background(), "example/app", descriptor.Digest); err == nil ||
		!strings.Contains(err.Error(), "unexpected manifest content type") {
		t.Fatalf("expected content type rejection, got %v", err)
	}
}

func TestHTTPRegistryRejectsMalformedReferrersResponse(t *testing.T) {
	subject := mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000027")
	registry := &HTTPRegistry{
		BaseURL: "https://registry.example.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			recorder := httptest.NewRecorder()
			recorder.Header().Set("Content-Type", MediaTypeOCIIndex)
			_, _ = recorder.WriteString("{broken-json")
			response := recorder.Result()
			response.Request = request
			return response, nil
		})},
	}
	if _, err := registry.Referrers(context.Background(), "example/app", subject); err == nil ||
		!strings.Contains(err.Error(), "invalid referrers response") {
		t.Fatalf("expected invalid response error, got %v", err)
	}
}

func TestHTTPRegistryDefaultClientAndHeaderPassthrough(t *testing.T) {
	var observedAuth string
	registry := &HTTPRegistry{
		BaseURL: "https://registry.example.test",
		Header:  http.Header{"Authorization": []string{"Bearer passthrough"}},
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			observedAuth = request.Header.Get("Authorization")
			recorder := httptest.NewRecorder()
			recorder.Header().Set("Content-Type", MediaTypeOCIIndex)
			_, _ = recorder.WriteString(`{"schemaVersion":2,"manifests":[]}`)
			response := recorder.Result()
			response.Request = request
			return response, nil
		})},
	}
	descriptors, err := registry.Referrers(context.Background(), "example/app",
		mustDigest(t, "0000000000000000000000000000000000000000000000000000000000000028"))
	if err != nil {
		t.Fatalf("Referrers returned error: %v", err)
	}
	if len(descriptors) != 0 {
		t.Fatalf("expected empty referrer list, got %d", len(descriptors))
	}
	if observedAuth != "Bearer passthrough" {
		t.Fatalf("authorization header not propagated: %q", observedAuth)
	}

	// Nil client falls back to http.DefaultClient without panicking.
	registry.HTTPClient = nil
	if registry.client() != http.DefaultClient {
		t.Fatal("nil HTTPClient must resolve to http.DefaultClient")
	}
}

func TestFallbackUnsupportedForSHA512Subject(t *testing.T) {
	digest, err := ParseDigest("sha512:" + strings.Repeat("ab", 64))
	if err != nil {
		t.Fatalf("parse sha512 digest: %v", err)
	}
	registry := newFakeRegistry()
	registry.referrersError[digest.String()] = ErrReferrersUnsupported

	_, err = FilterAndVerify(
		context.Background(), registry, "example/app", digest.String(),
		NewAllowList(testArtifactSignature), newRecordingVerifier(),
	)
	if err == nil || !strings.Contains(err.Error(), "only defined for sha256") {
		t.Fatalf("expected sha512 fallback rejection, got %v", err)
	}
}

// roundTripFunc adapts a function to http.RoundTripper without a listener.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
