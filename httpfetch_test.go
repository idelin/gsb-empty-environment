package netsec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func testResponse(req *http.Request, status int, location, body string) *http.Response {
	header := make(http.Header)
	if location != "" {
		header.Set("Location", location)
	}
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func TestHTTPFetcherRejectsCrossHostRedirect(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return testResponse(req, http.StatusFound, "https://evil.example/status/1", ""), nil
	})}
	fetcher := &HTTPFetcher{Client: client}
	_, err := fetcher.FetchStatusList(context.Background(), "https://issuer.example/redirect")
	if !errors.Is(err, ErrStatusListHostMismatch) {
		t.Fatalf("FetchStatusList error = %v, want ErrStatusListHostMismatch", err)
	}
}

func TestHTTPFetcherAllowsSameHostRedirect(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/redirect":
			return testResponse(req, http.StatusFound, "/status/1", ""), nil
		case "/status/1":
			return testResponse(req, http.StatusOK, "", `{"credentialSubject":{"statusPurpose":"revocation"}}`), nil
		default:
			return testResponse(req, http.StatusNotFound, "", ""), nil
		}
	})}

	fetcher := &HTTPFetcher{Client: client}
	body, err := fetcher.FetchStatusList(context.Background(), "https://issuer.example/redirect")
	if err != nil {
		t.Fatalf("FetchStatusList: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("empty status list body")
	}
}

func TestHTTPFetcherLimitsBodySize(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return testResponse(req, http.StatusOK, "", "12345"), nil
	})}

	fetcher := &HTTPFetcher{Client: client, MaxBytes: 4}
	if _, err := fetcher.FetchStatusList(context.Background(), "https://issuer.example/status/1"); err == nil {
		t.Fatal("expected oversized body to be rejected")
	}
}
