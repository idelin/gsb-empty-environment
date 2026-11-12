package netsec

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// roundTripFunc is an http.RoundTripper that avoids binding any local port,
// which is useful in sandboxed environments where listeners are unavailable.
type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestHTTPTokenFetcherConfidentialClient(t *testing.T) {
	var gotForm url.Values
	var gotAuth, gotContentType string
	fetcher := &HTTPTokenFetcher{
		TokenEndpoint: "https://as.example.com/token",
		ClientID:      "client-1",
		ClientSecret:  "s3cr3t",
		HTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				raw, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatalf("reading body: %v", err)
				}
				gotForm, err = url.ParseQuery(string(raw))
				if err != nil {
					t.Fatalf("ParseQuery: %v", err)
				}
				gotAuth = req.Header.Get("Authorization")
				gotContentType = req.Header.Get("Content-Type")
				return jsonResponse(http.StatusOK, `{
					"access_token":"real-access-token",
					"token_type":"Bearer",
					"expires_in":3600,
					"refresh_token":"real-refresh-token",
					"id_token":"real-id-token",
					"scope":"openid profile"
				}`), nil
			}),
		},
	}

	token, err := fetcher.FetchToken(context.Background(), testAuthReqID)
	if err != nil {
		t.Fatalf("FetchToken: %v", err)
	}

	if gotForm.Get("grant_type") != cibaGrantType {
		t.Fatalf("expected CIBA grant type, got %q", gotForm.Get("grant_type"))
	}
	if gotForm.Get("auth_req_id") != testAuthReqID {
		t.Fatalf("expected auth_req_id %q, got %q", testAuthReqID, gotForm.Get("auth_req_id"))
	}
	if gotForm.Get("client_id") != "" {
		t.Fatalf("confidential client must not send client_id in the body, got %q", gotForm.Get("client_id"))
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("client-1:s3cr3t"))
	if gotAuth != wantAuth {
		t.Fatalf("expected %q, got %q", wantAuth, gotAuth)
	}
	if gotContentType != "application/x-www-form-urlencoded" {
		t.Fatalf("unexpected content type %q", gotContentType)
	}
	if token.AccessToken != "real-access-token" || token.RefreshToken != "real-refresh-token" ||
		token.IDToken != "real-id-token" || token.Scope != "openid profile" {
		t.Fatalf("unexpected token response: %+v", token)
	}
	if token.ExpiresIn != time.Hour {
		t.Fatalf("expected 1h expiry, got %v", token.ExpiresIn)
	}
}

func TestHTTPTokenFetcherPublicClientSendsClientIDInBody(t *testing.T) {
	var gotForm url.Values
	var sentAuth string
	fetcher := &HTTPTokenFetcher{
		TokenEndpoint: "https://as.example.com/token",
		ClientID:      "public-client",
		HTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				raw, _ := io.ReadAll(req.Body)
				gotForm, _ = url.ParseQuery(string(raw))
				sentAuth = req.Header.Get("Authorization")
				return jsonResponse(http.StatusOK, `{"access_token":"t","token_type":"Bearer"}`), nil
			}),
		},
	}

	if _, err := fetcher.FetchToken(context.Background(), testAuthReqID); err != nil {
		t.Fatalf("FetchToken: %v", err)
	}
	if gotForm.Get("client_id") != "public-client" {
		t.Fatalf("expected client_id in body, got %q", gotForm.Get("client_id"))
	}
	if sentAuth != "" {
		t.Fatalf("public client must not use Basic auth, got %q", sentAuth)
	}
}

func TestHTTPTokenFetcherErrors(t *testing.T) {
	t.Run("token endpoint error response", func(t *testing.T) {
		fetcher := &HTTPTokenFetcher{
			TokenEndpoint: "https://as.example.com/token",
			ClientID:      "c",
			HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusBadRequest, `{"error":"access_denied","error_description":"user refused"}`), nil
			})},
		}
		_, err := fetcher.FetchToken(context.Background(), testAuthReqID)
		if err == nil || !strings.Contains(err.Error(), "access_denied") {
			t.Fatalf("expected access_denied error, got %v", err)
		}
	})

	t.Run("non-2xx without error object", func(t *testing.T) {
		fetcher := &HTTPTokenFetcher{
			TokenEndpoint: "https://as.example.com/token",
			ClientID:      "c",
			HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusInternalServerError, "boom"), nil
			})},
		}
		if _, err := fetcher.FetchToken(context.Background(), testAuthReqID); err == nil {
			t.Fatal("expected an error for a 500 response")
		}
	})

	t.Run("transport failure", func(t *testing.T) {
		fetcher := &HTTPTokenFetcher{
			TokenEndpoint: "https://as.example.com/token",
			ClientID:      "c",
			HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, io.EOF
			})},
		}
		if _, err := fetcher.FetchToken(context.Background(), testAuthReqID); err == nil {
			t.Fatal("expected the transport error to be returned")
		}
	})

	t.Run("missing endpoint configuration", func(t *testing.T) {
		fetcher := &HTTPTokenFetcher{ClientID: "c"}
		if _, err := fetcher.FetchToken(context.Background(), testAuthReqID); err == nil {
			t.Fatal("expected an error when no endpoint is configured")
		}
	})

	t.Run("malformed endpoint url", func(t *testing.T) {
		fetcher := &HTTPTokenFetcher{
			TokenEndpoint: "http://[::1:bad-url",
			ClientID:      "c",
		}
		if _, err := fetcher.FetchToken(context.Background(), testAuthReqID); err == nil {
			t.Fatal("expected an error for a malformed endpoint URL")
		}
	})
}
