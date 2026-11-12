package netsec

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func fakeResponse(req *http.Request, status int, location, body string) *http.Response {
	header := make(http.Header)
	if location != "" {
		header.Set("Location", location)
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func TestHTTP01ChallengeURLAndHost(t *testing.T) {
	t.Parallel()
	id := mustIP(t, "8.8.8.8")
	challengeURL, err := id.HTTP01ChallengeURL("token-123")
	if err != nil {
		t.Fatalf("HTTP01ChallengeURL: %v", err)
	}
	const wantURL = "http://8.8.8.8/.well-known/acme-challenge/token-123"
	if challengeURL != wantURL {
		t.Fatalf("URL = %q, want %q", challengeURL, wantURL)
	}
	host, err := id.HTTP01HostHeader()
	if err != nil {
		t.Fatalf("HTTP01HostHeader: %v", err)
	}
	if host != "8.8.8.8" {
		t.Fatalf("Host = %q", host)
	}

	id6 := mustIP(t, "2001:4860:4860::8888")
	challengeURL, err = id6.HTTP01ChallengeURL("token_123")
	if err != nil {
		t.Fatalf("HTTP01ChallengeURL(v6): %v", err)
	}
	const wantURL6 = "http://[2001:4860:4860::8888]/.well-known/acme-challenge/token_123"
	if challengeURL != wantURL6 {
		t.Fatalf("URL = %q, want %q", challengeURL, wantURL6)
	}
	host, err = id6.HTTP01HostHeader()
	if err != nil {
		t.Fatalf("HTTP01HostHeader(v6): %v", err)
	}
	if host != "[2001:4860:4860::8888]" {
		t.Fatalf("Host = %q", host)
	}

	for _, token := range []string{"", "../escape", "a/b", "a?b", "a b"} {
		if _, err := id.HTTP01ChallengeURL(token); !errors.Is(err, ErrHTTP01Token) {
			t.Fatalf("token %q: got %v, want ErrHTTP01Token", token, err)
		}
	}
}

func TestFetchHTTP01PinsHostAndAddress(t *testing.T) {
	t.Parallel()
	id := mustIP(t, "8.8.8.8")
	var hosts, urls []string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		hosts = append(hosts, req.Host)
		urls = append(urls, req.URL.String())
		return fakeResponse(req, http.StatusOK, "", "key-auth"), nil
	})}

	body, err := FetchHTTP01(context.Background(), id, "token", client)
	if err != nil {
		t.Fatalf("FetchHTTP01: %v", err)
	}
	if string(body) != "key-auth" {
		t.Fatalf("body = %q", body)
	}
	if len(hosts) != 1 || hosts[0] != "8.8.8.8" {
		t.Fatalf("Host headers = %q", hosts)
	}
	if len(urls) != 1 || urls[0] != "http://8.8.8.8/.well-known/acme-challenge/token" {
		t.Fatalf("URLs = %q", urls)
	}
}

func TestValidateHTTP01(t *testing.T) {
	t.Parallel()
	id := mustIP(t, "8.8.8.8")
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return fakeResponse(req, http.StatusOK, "", "token.thumbprint\n"), nil
	})}
	if err := ValidateHTTP01(context.Background(), id, "token", "token.thumbprint", client); err != nil {
		t.Fatalf("ValidateHTTP01: %v", err)
	}
	if err := ValidateHTTP01(context.Background(), id, "token", "wrong", client); !errors.Is(err, ErrHTTP01KeyAuthorization) {
		t.Fatalf("wrong key authorization: got %v", err)
	}
}

func TestHTTP01FollowsSameAddressRedirect(t *testing.T) {
	t.Parallel()
	id := mustIP(t, "8.8.8.8")
	var calls int
	var hosts []string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		hosts = append(hosts, req.Host)
		if calls == 1 {
			return fakeResponse(req, http.StatusFound,
				"http://8.8.8.8/.well-known/acme-challenge/redirected", ""), nil
		}
		return fakeResponse(req, http.StatusOK, "", "key-auth"), nil
	})}

	if err := ValidateHTTP01(context.Background(), id, "token", "key-auth", client); err != nil {
		t.Fatalf("ValidateHTTP01: %v", err)
	}
	if calls != 2 {
		t.Fatalf("transport calls = %d, want 2", calls)
	}
	for i, host := range hosts {
		if host != "8.8.8.8" {
			t.Fatalf("request %d Host = %q", i, host)
		}
	}
}

func TestHTTP01RejectsCrossAddressRedirect(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		location string
	}{
		{"other IPv4", "http://1.1.1.1/.well-known/acme-challenge/token"},
		{"other IPv6", "http://[2606:4700::1111]/.well-known/acme-challenge/token"},
		{"DNS name", "http://example.com/.well-known/acme-challenge/token"},
		{"IPv4-mapped", "http://[::ffff:8.8.8.8]/.well-known/acme-challenge/token"},
		{"unsupported scheme", "ftp://8.8.8.8/.well-known/acme-challenge/token"},
		{"userinfo", "http://user:pass@8.8.8.8/.well-known/acme-challenge/token"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			id := mustIP(t, "8.8.8.8")
			var calls atomic.Int32
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls.Add(1)
				return fakeResponse(req, http.StatusFound, tt.location, ""), nil
			})}
			err := ValidateHTTP01(context.Background(), id, "token", "key-auth", client)
			if !errors.Is(err, ErrHTTP01Redirect) {
				t.Fatalf("redirect %q: got %v, want ErrHTTP01Redirect", tt.location, err)
			}
			if got := calls.Load(); got != 1 {
				t.Fatalf("transport calls = %d, want 1 (redirect must not be followed)", got)
			}
		})
	}
}

func TestHTTP01RejectsTooManyRedirects(t *testing.T) {
	t.Parallel()
	id := mustIP(t, "8.8.8.8")
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		return fakeResponse(req, http.StatusFound,
			"http://8.8.8.8/.well-known/acme-challenge/again", ""), nil
	})}
	err := fetchWithClient(t, id, client)
	if !errors.Is(err, ErrHTTP01Redirect) {
		t.Fatalf("got %v, want ErrHTTP01Redirect", err)
	}
	if got := calls.Load(); got != http01MaxRedirects {
		t.Fatalf("transport calls = %d, want %d", got, http01MaxRedirects)
	}
}

func fetchWithClient(t *testing.T, id Identifier, client *http.Client) error {
	t.Helper()
	_, err := FetchHTTP01(context.Background(), id, "token", client)
	return err
}
