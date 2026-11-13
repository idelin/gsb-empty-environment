package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"
)

var httpsOrigin = Origin{Scheme: "https", Host: "origin.example.com", Port: 443}

func TestRejectsCleartextAdvertisement(t *testing.T) {
	c := NewCache()
	httpOrigin := Origin{Scheme: "http", Host: "origin.example.com", Port: 80}
	c.Observe(httpOrigin, []string{`h2="alt.example.com:443"; ma=3600`})
	if got := c.Alternatives(httpOrigin); len(got) != 0 {
		t.Fatalf("cleartext advertisement stored %d entries, want 0", len(got))
	}
}

func TestAcceptsHTTPSAdvertisement(t *testing.T) {
	c := NewCache()
	c.Observe(httpsOrigin, []string{`h2="alt.example.com:443"; ma=3600, h3=":8443"; persist=1`})
	got := c.Alternatives(httpsOrigin)
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	var h2, h3 *Entry
	for _, e := range got {
		switch e.Protocol {
		case "h2":
			h2 = e
		case "h3":
			h3 = e
		}
	}
	if h2 == nil || h2.Host != "alt.example.com" || h2.Port != 443 {
		t.Fatalf("bad h2 entry: %+v", h2)
	}
	if h3 == nil || h3.Host != "" || h3.Port != 8443 || !h3.Persist {
		t.Fatalf("bad h3 entry: %+v", h3)
	}
	if ep := h3.Endpoint(httpsOrigin); ep != "origin.example.com:8443" {
		t.Fatalf("endpoint %q, want origin host with advertised port", ep)
	}
}

func TestMaxAgeDefaultAndCap(t *testing.T) {
	c := NewCache()
	before := time.Now()
	c.Observe(httpsOrigin, []string{
		`h2="a.example.com:443"`,              // no ma -> default 86400s
		`h2="b.example.com:443"; ma=99999999`, // over the 7-day cap
	})
	after := time.Now()
	entries := c.Alternatives(httpsOrigin)
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	for _, e := range entries {
		var want time.Duration
		switch e.Host {
		case "a.example.com":
			want = DefaultMaxAge
		case "b.example.com":
			want = MaxMaxAge
		default:
			t.Fatalf("unexpected host %q", e.Host)
		}
		lo := before.Add(want)
		hi := after.Add(want)
		if e.Expires.Before(lo) || e.Expires.After(hi) {
			t.Errorf("host %s expires at %v, want within [%v, %v]", e.Host, e.Expires, lo, hi)
		}
	}
}

func TestClearRemovesAllEntries(t *testing.T) {
	c := NewCache()
	c.Observe(httpsOrigin, []string{`h2="alt.example.com:443", h3=":8443"`})
	if got := c.Alternatives(httpsOrigin); len(got) != 2 {
		t.Fatalf("setup: got %d entries, want 2", len(got))
	}
	c.Observe(httpsOrigin, []string{"clear"})
	if got := c.Alternatives(httpsOrigin); len(got) != 0 {
		t.Fatalf("after clear got %d entries, want 0", len(got))
	}
}

func TestNinthAdvertisementEvictsEarliestExpiring(t *testing.T) {
	c := NewCache()
	for i := 1; i <= MaxEntriesPerOrigin; i++ {
		host := "alt" + string(rune('0'+i)) + ".example.com"
		c.Observe(httpsOrigin, []string{`h2="` + host + `:443"; ma=` + itoa(i*1000)})
	}
	if got := c.Alternatives(httpsOrigin); len(got) != MaxEntriesPerOrigin {
		t.Fatalf("setup: got %d entries, want %d", len(got), MaxEntriesPerOrigin)
	}
	// The 9th entry has the largest ma; the entry with ma=1000 (alt1) must go.
	c.Observe(httpsOrigin, []string{`h2="alt9.example.com:443"; ma=9000`})
	got := c.Alternatives(httpsOrigin)
	if len(got) != MaxEntriesPerOrigin {
		t.Fatalf("got %d entries, want %d", len(got), MaxEntriesPerOrigin)
	}
	for _, e := range got {
		if e.Host == "alt1.example.com" {
			t.Fatalf("earliest-expiring entry was not evicted: %+v", e)
		}
	}
}

func TestHandshakeFailureDisablesEntry(t *testing.T) {
	c := NewCache()
	now := time.Now()
	c.now = func() time.Time { return now }
	c.Observe(httpsOrigin, []string{`h2="alt.example.com:443"; ma=3600`})
	c.RecordHandshakeFailure(httpsOrigin, "h2", "alt.example.com", 443)
	if got := c.Alternatives(httpsOrigin); len(got) != 0 {
		t.Fatalf("entry usable after handshake failure: %+v", got)
	}
	now = now.Add(HandshakeFailurePenalty + time.Second)
	if got := c.Alternatives(httpsOrigin); len(got) != 1 {
		t.Fatalf("entry still disabled after penalty, got %d entries", len(got))
	}
}

func TestExpiredEntriesNotReturned(t *testing.T) {
	c := NewCache()
	now := time.Now()
	c.now = func() time.Time { return now }
	c.Observe(httpsOrigin, []string{`h2="alt.example.com:443"; ma=60`})
	now = now.Add(2 * time.Minute)
	if got := c.Alternatives(httpsOrigin); len(got) != 0 {
		t.Fatalf("expired entry returned: %+v", got)
	}
}

func TestCertificateValidOnlyForAltHostRejected(t *testing.T) {
	roots, leaf := makeChain(t, []string{"alt.example.com"})

	// The certificate is valid for the alternative host but not for the
	// origin: using it for the origin must fail.
	if err := VerifyPeerCertificates(httpsOrigin.Host, []*x509.Certificate{leaf}, roots); err == nil {
		t.Fatal("certificate valid only for the alternative host accepted for the origin")
	}
	// Sanity check: the same chain verifies for its own name.
	if err := VerifyPeerCertificates("alt.example.com", []*x509.Certificate{leaf}, roots); err != nil {
		t.Fatalf("certificate rejected for its own host name: %v", err)
	}
	// The dial config pins verification to the origin host.
	if cfg := TLSConfig(httpsOrigin, roots); cfg.ServerName != httpsOrigin.Host {
		t.Fatalf("TLSConfig ServerName %q, want %q", cfg.ServerName, httpsOrigin.Host)
	}
}

func TestCertificateValidForOriginAccepted(t *testing.T) {
	roots, leaf := makeChain(t, []string{httpsOrigin.Host})
	if err := VerifyPeerCertificates(httpsOrigin.Host, []*x509.Certificate{leaf}, roots); err != nil {
		t.Fatalf("certificate valid for origin rejected: %v", err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// makeChain returns a root pool and a leaf certificate, signed by that root,
// carrying the given DNS names.
func makeChain(t *testing.T, dnsNames []string) (*x509.CertPool, *x509.Certificate) {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: dnsNames[0]},
		DNSNames:     dnsNames,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(leafDER)
	if err != nil {
		t.Fatal(err)
	}

	roots := x509.NewCertPool()
	roots.AddCert(ca)
	return roots, leaf
}
