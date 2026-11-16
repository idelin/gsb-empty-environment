package netsec

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, time.November, 16, 12, 0, 0, 0, time.UTC)

func testCache(now *time.Time) *Cache {
	return New(WithClock(func() time.Time { return *now }))
}

func httpsResponse(t *testing.T, origin string, values ...string) *http.Response {
	t.Helper()
	if !strings.Contains(origin, "://") {
		origin = "https://" + origin
	}
	u, err := url.Parse(origin)
	if err != nil {
		t.Fatalf("parse test origin: %v", err)
	}
	header := make(http.Header)
	for _, value := range values {
		header.Add("Alt-Svc", value)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     header,
		Request:    &http.Request{Method: http.MethodGet, URL: u},
		TLS:        &tls.ConnectionState{},
	}
}

func TestPlaintextAdvertisementIgnored(t *testing.T) {
	const rawResponse = "HTTP/1.1 200 OK\r\n" +
		"Alt-Svc: h2=\"attacker.example:8443\"; ma=3600\r\n" +
		"Content-Length: 0\r\n\r\n"
	req := &http.Request{Method: http.MethodGet, URL: &url.URL{Scheme: "http", Host: "origin.example"}}
	resp, err := http.ReadResponse(bufio.NewReader(strings.NewReader(rawResponse)), req)
	if err != nil {
		t.Fatalf("read plaintext response: %v", err)
	}
	resp.Body.Close()

	cache := New()
	if got := cache.Store(resp); got != 0 {
		t.Fatalf("Store returned %d advertisements from plaintext response, want 0", got)
	}
	if got := cache.Lookup("https://origin.example"); len(got) != 0 {
		t.Fatalf("cache contains %d entries from plaintext response, want 0", len(got))
	}

	plain := httpsResponse(t, "https://origin.example", `h3="attacker.example:443"`)
	plain.TLS = nil
	plain.Request.URL.Scheme = "http"
	if got := cache.Store(plain); got != 0 {
		t.Fatalf("Store returned %d advertisements for response without TLS, want 0", got)
	}
}

func TestHTTPSAdvertisementAndMaxAgeCap(t *testing.T) {
	now := testNow
	resp := httpsResponse(t, "https://origin.example:9443", `h3=":8443"; ma=99999999; persist=1`)

	cache := testCache(&now)
	if got := cache.Store(resp); got != 1 {
		t.Fatalf("Store returned %d advertisements, want 1", got)
	}
	entries := cache.Lookup("https://origin.example:9443")
	if len(entries) != 1 {
		t.Fatalf("Lookup returned %d entries, want 1", len(entries))
	}
	entry := entries[0]
	if entry.Protocol != ProtocolH3 {
		t.Errorf("Protocol = %q, want %q", entry.Protocol, ProtocolH3)
	}
	if entry.Host != "origin.example" {
		t.Errorf("Host = %q, want origin.example for omitted alt host", entry.Host)
	}
	if entry.Port != 8443 {
		t.Errorf("Port = %d, want 8443", entry.Port)
	}
	if !entry.Persist {
		t.Error("Persist = false, want true")
	}
	if got, want := entry.Expires.Sub(now), MaxMaxAge; got != want {
		t.Errorf("lifetime = %v, want capped lifetime %v", got, want)
	}
}

func TestDefaultMaxAge(t *testing.T) {
	now := testNow
	cache := testCache(&now)
	cache.Store(httpsResponse(t, "https://origin.example", `h2=":8443"`))

	entries := cache.Lookup("https://origin.example")
	if len(entries) != 1 {
		t.Fatalf("Lookup returned %d entries, want 1", len(entries))
	}
	if got, want := entries[0].Expires.Sub(now), DefaultMaxAge; got != want {
		t.Errorf("default lifetime = %v, want %v", got, want)
	}
	if entries[0].Host != "origin.example" {
		t.Errorf("Host = %q, want origin.example", entries[0].Host)
	}
}

func TestClearRemovesOnlySourceEntries(t *testing.T) {
	now := testNow
	cache := testCache(&now)
	cache.Store(httpsResponse(t, "https://origin.example",
		`h2="alt1.example:443"; ma=600`,
		`h3="alt2.example:443"; ma=600`,
	))
	cache.Store(httpsResponse(t, "https://other.example", `h2="alt3.example:443"; ma=600`))

	if got := len(cache.Lookup("https://origin.example")); got != 2 {
		t.Fatalf("before clear: %d origin entries, want 2", got)
	}
	if got := cache.Store(httpsResponse(t, "https://origin.example", "  CLEAR  ")); got != 0 {
		t.Errorf("Store(clear) returned %d stored entries, want 0", got)
	}
	if got := len(cache.Lookup("https://origin.example")); got != 0 {
		t.Errorf("after clear: %d origin entries, want 0", got)
	}
	if got := len(cache.Lookup("https://other.example")); got != 1 {
		t.Errorf("other origin entries = %d, want 1", got)
	}
}

func TestNinthAdvertisementEvictsEarliestExpiry(t *testing.T) {
	now := testNow
	cache := testCache(&now)

	for i := 0; i < 8; i++ {
		value := fmt.Sprintf(`h2="alt%d.example:8443"; ma=%d`, i, 100+i)
		if got := cache.Store(httpsResponse(t, "https://origin.example", value)); got != 1 {
			t.Fatalf("Store advertisement %d returned %d, want 1", i+1, got)
		}
	}
	if got := cache.Store(httpsResponse(t, "https://origin.example", `h2="alt8.example:8443"; ma=1000`)); got != 1 {
		t.Fatalf("Store ninth advertisement returned %d, want 1", got)
	}

	entries := cache.Lookup("https://origin.example")
	if len(entries) != MaxEntriesPerOrigin {
		t.Fatalf("retained %d entries, want %d", len(entries), MaxEntriesPerOrigin)
	}
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		seen[entry.Host] = true
	}
	if seen["alt0.example"] {
		t.Error("earliest-expiring entry alt0.example was not evicted")
	}
	if !seen["alt8.example"] {
		t.Error("ninth advertisement alt8.example was not retained")
	}
}

func TestHandshakeFailureDisablesEntryForFiveMinutes(t *testing.T) {
	now := testNow
	cache := testCache(&now)
	if !cache.Add("https://origin.example", ProtocolH2, "alt.example", 8443, 10*time.Minute, false) {
		t.Fatal("Add failed")
	}

	entry, ok := cache.Select("https://origin.example")
	if !ok {
		t.Fatal("Select did not return the advertised endpoint")
	}
	if !cache.MarkFailure("https://origin.example", entry) {
		t.Fatal("MarkFailure did not find the endpoint")
	}

	lookedUp := cache.Lookup("https://origin.example")
	if len(lookedUp) != 1 {
		t.Fatalf("Lookup returned %d entries, want 1", len(lookedUp))
	}
	if got, want := lookedUp[0].DisabledUntil, now.Add(FailureCooldown); !got.Equal(want) {
		t.Errorf("DisabledUntil = %v, want %v", got, want)
	}
	if _, ok := cache.Select("https://origin.example"); ok {
		t.Error("Select returned an endpoint immediately after a handshake failure")
	}

	now = now.Add(FailureCooldown - time.Nanosecond)
	if _, ok := cache.Select("https://origin.example"); ok {
		t.Error("Select returned an endpoint before the cooldown elapsed")
	}
	now = now.Add(2 * time.Nanosecond)
	if _, ok := cache.Select("https://origin.example"); !ok {
		t.Error("Select did not return the endpoint after the cooldown elapsed")
	}
}

func TestRefreshDoesNotResetFailureCooldown(t *testing.T) {
	now := testNow
	cache := testCache(&now)
	cache.Add("https://origin.example", ProtocolH3, "alt.example", 443, 10*time.Minute, false)
	if !cache.MarkFailed("https://origin.example", ProtocolH3, "alt.example", 443) {
		t.Fatal("MarkFailed did not find the endpoint")
	}

	cache.Add("https://origin.example", ProtocolH3, "alt.example", 443, 10*time.Minute, false)
	if _, ok := cache.Select("https://origin.example"); ok {
		t.Error("refreshing an advertisement reset the handshake-failure cooldown")
	}
}
