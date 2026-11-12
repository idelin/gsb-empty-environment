package webauthn

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type stubRoundTripper struct {
	status      int
	contentType string
	body        string
	roundErr    error
	gotURL      string
	gotAccept   string
}

func (s *stubRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if s.roundErr != nil {
		return nil, s.roundErr
	}
	s.gotURL = req.URL.String()
	s.gotAccept = req.Header.Get("Accept")
	return &http.Response{
		StatusCode: s.status,
		Header:     http.Header{"Content-Type": []string{s.contentType}},
		Body:       io.NopCloser(strings.NewReader(s.body)),
	}, nil
}

func TestFetchManifest(t *testing.T) {
	validBody := `{"origins":["https://example-brand.com","https://brand2.example"]}`

	t.Run("fetches well known url and parses manifest", func(t *testing.T) {
		stub := &stubRoundTripper{
			status:      http.StatusOK,
			contentType: "application/json; charset=utf-8",
			body:        validBody,
		}
		client := &http.Client{Transport: stub}

		manifest, err := FetchManifest(context.Background(), client, testRPID)
		if err != nil {
			t.Fatalf("FetchManifest: %v", err)
		}
		wantURL := "https://" + testRPID + "/.well-known/webauthn"
		if stub.gotURL != wantURL {
			t.Fatalf("request URL = %q, want %q", stub.gotURL, wantURL)
		}
		if stub.gotAccept != "application/json" {
			t.Fatalf("Accept header = %q", stub.gotAccept)
		}
		if !manifest.Allows("https://brand2.example") {
			t.Fatal("parsed manifest does not contain expected origin")
		}
	})

	t.Run("non ok status rejected", func(t *testing.T) {
		stub := &stubRoundTripper{status: http.StatusNotFound, contentType: "application/json", body: "{}"}
		_, err := FetchManifest(context.Background(), &http.Client{Transport: stub}, testRPID)
		if !errors.Is(err, ErrManifest) {
			t.Fatalf("error = %v, want ErrManifest", err)
		}
	})

	t.Run("invalid json body rejected", func(t *testing.T) {
		stub := &stubRoundTripper{status: http.StatusOK, contentType: "application/json", body: "{broken"}
		_, err := FetchManifest(context.Background(), &http.Client{Transport: stub}, testRPID)
		if !errors.Is(err, ErrManifest) {
			t.Fatalf("error = %v, want ErrManifest", err)
		}
	})

	t.Run("wrong content type rejected", func(t *testing.T) {
		stub := &stubRoundTripper{status: http.StatusOK, contentType: "text/html", body: validBody}
		_, err := FetchManifest(context.Background(), &http.Client{Transport: stub}, testRPID)
		if !errors.Is(err, ErrManifest) {
			t.Fatalf("error = %v, want ErrManifest", err)
		}
	})

	t.Run("oversized body rejected", func(t *testing.T) {
		stub := &stubRoundTripper{
			status:      http.StatusOK,
			contentType: "application/json",
			body:        strings.Repeat("a", maxManifestBytes+1),
		}
		_, err := FetchManifest(context.Background(), &http.Client{Transport: stub}, testRPID)
		if !errors.Is(err, ErrManifest) {
			t.Fatalf("error = %v, want ErrManifest", err)
		}
	})

	t.Run("invalid rp id rejected before request", func(t *testing.T) {
		stub := &stubRoundTripper{status: http.StatusOK, contentType: "application/json", body: validBody}
		_, err := FetchManifest(context.Background(), &http.Client{Transport: stub}, "not a domain")
		if !errors.Is(err, ErrRPID) {
			t.Fatalf("error = %v, want ErrRPID", err)
		}
	})

	t.Run("transport error wrapped", func(t *testing.T) {
		stub := &stubRoundTripper{roundErr: errors.New("connection refused")}
		_, err := FetchManifest(context.Background(), &http.Client{Transport: stub}, testRPID)
		if !errors.Is(err, ErrManifest) {
			t.Fatalf("error = %v, want ErrManifest", err)
		}
	})
}

func TestParseClientData(t *testing.T) {
	raw := `{
		"type": "webauthn.get",
		"challenge": "cHh6",
		"origin": "https://auth.example-brand.com",
		"crossOrigin": true,
		"topOrigin": "https://brand2.example"
	}`
	encoded := []byte(base64.RawURLEncoding.EncodeToString([]byte(raw)))

	data, err := ParseClientData(encoded)
	if err != nil {
		t.Fatalf("ParseClientData: %v", err)
	}
	if data.Origin != "https://auth.example-brand.com" {
		t.Fatalf("Origin = %q", data.Origin)
	}
	if !data.CrossOrigin {
		t.Fatal("CrossOrigin = false, want true")
	}
	if data.TopOrigin != "https://brand2.example" {
		t.Fatalf("TopOrigin = %q", data.TopOrigin)
	}

	// Standard base64 with padding must also decode.
	padded := []byte(base64.StdEncoding.EncodeToString([]byte(raw)))
	if _, err := ParseClientData(padded); err != nil {
		t.Fatalf("ParseClientData padded: %v", err)
	}

	if _, err := ParseClientData([]byte("not base64!!!")); err == nil {
		t.Fatal("expected error for undecodable clientData")
	}

	// Valid base64 that is not JSON must be rejected.
	badJSON := base64.RawURLEncoding.EncodeToString([]byte("{broken"))
	if _, err := ParseClientData([]byte(badJSON)); err == nil {
		t.Fatal("expected error for malformed clientData JSON")
	}
}

func TestEndToEndAssertionFlow(t *testing.T) {
	// Simulate the full pipeline a relying party assertion verifier would run:
	// fetch manifest, construct verifier, then accept a legitimate assertion
	// while rejecting look-alike and framed-attacker variants.
	stub := &stubRoundTripper{
		status:      http.StatusOK,
		contentType: "application/json",
		body: `{"origins":[
			"https://example-brand.com",
			"https://auth.example-brand.com",
			"https://brand2.example"]}`,
	}
	manifest, err := FetchManifest(context.Background(), &http.Client{Transport: stub}, testRPID)
	if err != nil {
		t.Fatalf("FetchManifest: %v", err)
	}

	verifier, err := NewVerifier(testRPID, manifest)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	legit := AssertionRequest{
		ClientData:        ClientData{Origin: "https://auth.example-brand.com"},
		AuthenticatorData: authenticatorData(t, testRPID),
	}
	if err := verifier.VerifyAssertion(legit); err != nil {
		t.Fatalf("legitimate assertion rejected: %v", err)
	}

	attacker := AssertionRequest{
		ClientData: ClientData{
			Origin:      "https://auth.example-brand.com",
			CrossOrigin: true,
			TopOrigin:   "https://examp1e-brand.com",
		},
		AuthenticatorData: authenticatorData(t, testRPID),
	}
	if err := verifier.VerifyAssertion(attacker); !errors.Is(err, ErrTopOriginRequired) {
		t.Fatalf("attacker framed assertion error = %v, want ErrTopOriginRequired", err)
	}
}
