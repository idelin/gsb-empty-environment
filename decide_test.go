package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"strings"
	"testing"
	"time"
)

type testKey struct {
	typ  string
	blob string
}

func (k testKey) Type() string    { return k.typ }
func (k testKey) Marshal() []byte { return []byte(k.blob) }

var (
	keyAlpha  = testKey{"ssh-ed25519", "alpha-key-material"}
	keyBeta   = testKey{"ssh-ed25519", "beta-key-material"}
	keyGamma  = testKey{"ssh-rsa", "gamma-key-material"}
	caPrimary = testKey{"ssh-ed25519", "ca-primary"}
	caRogue   = testKey{"ssh-ed25519", "ca-rogue"}
)

func testHMAC(salt, msg []byte) []byte {
	h := hmac.New(sha256.New, salt)
	h.Write(msg)
	return h.Sum(nil)
}

func mustPatterns(t *testing.T, s string) []Pattern {
	t.Helper()
	p, err := ParsePatternList(s)
	if err != nil {
		t.Fatalf("ParsePatternList(%q): %v", s, err)
	}
	return p
}

func TestRevocationTakesPriorityOverAllow(t *testing.T) {
	d := &Decider{
		HostKeys: []HostKeyEntry{
			{Patterns: mustPatterns(t, "web.example.com"), Key: keyAlpha},
		},
		Revocations: []Revocation{{Key: keyAlpha}},
	}
	dec := d.Decide(Input{Host: "web.example.com", Port: 22, Key: keyAlpha})
	if dec.Allowed {
		t.Fatal("revoked key must not be allowed")
	}
	if dec.Reason != ReasonRevoked {
		t.Fatalf("reason = %q, want %q", dec.Reason, ReasonRevoked)
	}
	// The allow entry matched, yet evaluation continued into revocation:
	// both facts must be visible in the evidence trail.
	if !dec.HasEvidence(EvAllowMatch) {
		t.Error("evidence missing allow_match for the matched entry")
	}
	if !dec.HasEvidence(EvRevocationMatch) {
		t.Error("evidence missing revocation_match")
	}
}

func TestRevocationCheckedForEveryEntry(t *testing.T) {
	// Multiple revocation entries for the same key material: all must be
	// recorded, proving evaluation does not stop at the first hit.
	d := &Decider{
		Revocations: []Revocation{{Key: keyAlpha}, {Key: keyAlpha}, {Key: keyBeta}},
	}
	dec := d.Decide(Input{Host: "h", Port: 22, Key: keyAlpha})
	count := 0
	for _, e := range dec.Evidence {
		if e.Kind == EvRevocationMatch {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("got %d revocation evidence entries, want 2", count)
	}
}

func TestOverlappingPatterns(t *testing.T) {
	d := &Decider{
		HostKeys: []HostKeyEntry{
			{Patterns: mustPatterns(t, "*.example.com"), Key: keyAlpha},
			{Patterns: mustPatterns(t, "db.example.com"), Key: keyAlpha},
			{Patterns: mustPatterns(t, "*.internal.example.com,!secure.internal.example.com"), Key: keyBeta},
		},
	}

	// Both the wildcard and the exact entry match: full evidence lists both.
	dec := d.Decide(Input{Host: "db.example.com", Port: 22, Key: keyAlpha})
	if !dec.Allowed {
		t.Fatalf("db.example.com should be allowed: %s", dec.Reason)
	}
	allowCount := 0
	for _, e := range dec.Evidence {
		if e.Kind == EvAllowMatch {
			allowCount++
		}
	}
	if allowCount != 2 {
		t.Fatalf("got %d allow_match entries, want 2 (wildcard + exact)", allowCount)
	}

	// Negated pattern overrides the wildcard in the same list.
	dec = d.Decide(Input{Host: "secure.internal.example.com", Port: 22, Key: keyBeta})
	if dec.Allowed {
		t.Fatal("negated pattern must override wildcard allow")
	}
	if dec.Reason != ReasonNoMatch {
		t.Fatalf("reason = %q, want %q", dec.Reason, ReasonNoMatch)
	}

	// Wildcard still allows its other hosts.
	dec = d.Decide(Input{Host: "api.internal.example.com", Port: 22, Key: keyBeta})
	if !dec.Allowed {
		t.Fatalf("api.internal.example.com should be allowed: %s", dec.Reason)
	}
}

func TestSameKeyAliases(t *testing.T) {
	// The same key material is pinned under two host aliases.
	d := &Decider{
		HostKeys: []HostKeyEntry{
			{Patterns: mustPatterns(t, "host-a.example.com"), Key: keyAlpha},
			{Patterns: mustPatterns(t, "host-b.example.com"), Key: keyAlpha},
		},
	}

	// Connecting to host-a matches only its own entry; the host-b entry
	// records a host mismatch with the same key material.
	dec := d.Decide(Input{Host: "host-a.example.com", Port: 22, Key: keyAlpha})
	if !dec.Allowed {
		t.Fatalf("host-a should be allowed: %s", dec.Reason)
	}
	if !dec.HasEvidence(EvAllowMatch) || !dec.HasEvidence(EvAllowHostMismatch) {
		t.Errorf("evidence should contain allow_match and allow_host_mismatch, got %+v", dec.Evidence)
	}

	// Revoking the key material denies every alias at once.
	d.Revocations = []Revocation{{Key: keyAlpha}}
	for _, host := range []string{"host-a.example.com", "host-b.example.com"} {
		dec = d.Decide(Input{Host: host, Port: 22, Key: keyAlpha})
		if dec.Allowed || dec.Reason != ReasonRevoked {
			t.Errorf("%s: allowed=%v reason=%q, want revoked", host, dec.Allowed, dec.Reason)
		}
	}
}

func TestCertificateAccepted(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	d := &Decider{
		CAs: []CAEntry{{Patterns: mustPatterns(t, "*.example.com"), CA: caPrimary}},
	}
	cert := &Certificate{
		Key:         keyGamma,
		CA:          caPrimary,
		Principals:  []string{"web.example.com"},
		ValidAfter:  now.Add(-time.Hour),
		ValidBefore: now.Add(time.Hour),
	}
	dec := d.Decide(Input{Host: "web.example.com", Port: 22, Now: now, Cert: cert})
	if !dec.Allowed {
		t.Fatalf("valid certificate should be allowed: %s", dec.Reason)
	}
	for _, kind := range []EvidenceKind{EvCertHostnameMatch, EvCertValidityOK, EvCertCAMatch} {
		if !dec.HasEvidence(kind) {
			t.Errorf("evidence missing %s", kind)
		}
	}
}

func TestCertificateWrongCA(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	d := &Decider{
		CAs: []CAEntry{{Patterns: mustPatterns(t, "*.example.com"), CA: caPrimary}},
	}
	cert := &Certificate{
		Key:         keyGamma,
		CA:          caRogue, // same host, certificate swapped to another CA
		Principals:  []string{"web.example.com"},
		ValidAfter:  now.Add(-time.Hour),
		ValidBefore: now.Add(time.Hour),
	}
	dec := d.Decide(Input{Host: "web.example.com", Port: 22, Now: now, Cert: cert})
	if dec.Allowed {
		t.Fatal("certificate from untrusted CA must be rejected")
	}
	if !strings.Contains(dec.Reason, ReasonCertCA) {
		t.Fatalf("reason = %q, want it to mention %q", dec.Reason, ReasonCertCA)
	}
	if !dec.HasEvidence(EvCertCABad) {
		t.Error("evidence missing cert_ca_untrusted")
	}
	// Hostname and validity still checked and recorded as passing.
	if !dec.HasEvidence(EvCertHostnameMatch) || !dec.HasEvidence(EvCertValidityOK) {
		t.Error("passing certificate checks must remain in the evidence trail")
	}
}

func TestCertificateCAOutOfScope(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	d := &Decider{
		// The right CA, but scoped to a different zone.
		CAs: []CAEntry{{Patterns: mustPatterns(t, "*.corp.example.com"), CA: caPrimary}},
	}
	cert := &Certificate{
		Key:         keyGamma,
		CA:          caPrimary,
		Principals:  []string{"web.example.com"},
		ValidAfter:  now.Add(-time.Hour),
		ValidBefore: now.Add(time.Hour),
	}
	dec := d.Decide(Input{Host: "web.example.com", Port: 22, Now: now, Cert: cert})
	if dec.Allowed {
		t.Fatal("CA outside its scoped patterns must be rejected")
	}
	if !strings.Contains(dec.Reason, ReasonCertCA) {
		t.Fatalf("reason = %q, want CA scope rejection", dec.Reason)
	}
}

func TestCertificateValidityWindow(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	d := &Decider{
		CAs: []CAEntry{{Patterns: mustPatterns(t, "web.example.com"), CA: caPrimary}},
	}
	cert := &Certificate{
		Key:         keyGamma,
		CA:          caPrimary,
		Principals:  []string{"web.example.com"},
		ValidAfter:  now.Add(-time.Hour),
		ValidBefore: now.Add(time.Hour),
	}
	for _, at := range []time.Time{now.Add(-2 * time.Hour), now.Add(2 * time.Hour), now.Add(time.Hour)} {
		dec := d.Decide(Input{Host: "web.example.com", Port: 22, Now: at, Cert: cert})
		if dec.Allowed {
			t.Fatalf("at=%s certificate must be rejected", at)
		}
		if !strings.Contains(dec.Reason, ReasonCertValidity) {
			t.Fatalf("at=%s reason = %q, want validity rejection", at, dec.Reason)
		}
		if !dec.HasEvidence(EvCertValidityBad) {
			t.Errorf("at=%s evidence missing cert_validity_expired", at)
		}
	}
}

func TestCertificateHostnameMismatch(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	d := &Decider{
		CAs: []CAEntry{{Patterns: mustPatterns(t, "*.example.com"), CA: caPrimary}},
	}
	cert := &Certificate{
		Key:         keyGamma,
		CA:          caPrimary,
		Principals:  []string{"other.example.com"},
		ValidAfter:  now.Add(-time.Hour),
		ValidBefore: now.Add(time.Hour),
	}
	dec := d.Decide(Input{Host: "web.example.com", Port: 22, Now: now, Cert: cert})
	if dec.Allowed {
		t.Fatal("certificate for another hostname must be rejected")
	}
	if !strings.Contains(dec.Reason, ReasonCertHostname) {
		t.Fatalf("reason = %q, want hostname rejection", dec.Reason)
	}
}

func TestCertificateRevokedKey(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	d := &Decider{
		CAs:         []CAEntry{{Patterns: mustPatterns(t, "*.example.com"), CA: caPrimary}},
		Revocations: []Revocation{{Key: keyGamma}},
	}
	cert := &Certificate{
		Key:         keyGamma,
		CA:          caPrimary,
		Principals:  []string{"web.example.com"},
		ValidAfter:  now.Add(-time.Hour),
		ValidBefore: now.Add(time.Hour),
	}
	dec := d.Decide(Input{Host: "web.example.com", Port: 22, Now: now, Cert: cert})
	if dec.Allowed || dec.Reason != ReasonRevoked {
		t.Fatalf("revoked certified key: allowed=%v reason=%q", dec.Allowed, dec.Reason)
	}
	if !dec.HasEvidence(EvRevocationMatch) || !dec.HasEvidence(EvCertCAMatch) {
		t.Error("evidence must show both the CA match and the overriding revocation")
	}
}

func TestHashedHostname(t *testing.T) {
	salt := []byte("0123456789abcdef")
	d := &Decider{
		HMAC: testHMAC,
		HostKeys: []HostKeyEntry{
			{Patterns: []Pattern{HashPattern(salt, "secret.example.com", 22, testHMAC)}, Key: keyAlpha},
		},
	}
	dec := d.Decide(Input{Host: "secret.example.com", Port: 22, Key: keyAlpha})
	if !dec.Allowed {
		t.Fatalf("hashed hostname should match: %s", dec.Reason)
	}
	dec = d.Decide(Input{Host: "other.example.com", Port: 22, Key: keyAlpha})
	if dec.Allowed {
		t.Fatal("different hostname must not match the hashed pattern")
	}
	if !dec.HasEvidence(EvAllowHostMismatch) {
		t.Error("evidence missing allow_host_mismatch for the non-matching host")
	}

	// Without an injected HMAC the hashed pattern can never match.
	d.HMAC = nil
	dec = d.Decide(Input{Host: "secret.example.com", Port: 22, Key: keyAlpha})
	if dec.Allowed {
		t.Fatal("hashed pattern must not match without an HMAC function")
	}
}

func TestPortSpecificPattern(t *testing.T) {
	d := &Decider{
		HostKeys: []HostKeyEntry{
			{Patterns: mustPatterns(t, "[jump.example.com]:2222"), Key: keyAlpha},
		},
	}
	if dec := d.Decide(Input{Host: "jump.example.com", Port: 2222, Key: keyAlpha}); !dec.Allowed {
		t.Fatalf("port 2222 should match: %s", dec.Reason)
	}
	if dec := d.Decide(Input{Host: "jump.example.com", Port: 22, Key: keyAlpha}); dec.Allowed {
		t.Fatal("port 22 must not match a [host]:2222 pattern")
	}
}

func TestKeyMismatchOnMatchingPattern(t *testing.T) {
	d := &Decider{
		HostKeys: []HostKeyEntry{
			{Patterns: mustPatterns(t, "web.example.com"), Key: keyAlpha},
		},
	}
	dec := d.Decide(Input{Host: "web.example.com", Port: 22, Key: keyBeta})
	if dec.Allowed {
		t.Fatal("different key material must be rejected")
	}
	if !dec.HasEvidence(EvAllowKeyMismatch) {
		t.Error("evidence missing allow_key_mismatch")
	}
}

func TestParsePatternRoundTrip(t *testing.T) {
	for _, s := range []string{
		"web.example.com",
		"!evil.example.com",
		"*.example.com",
		"[jump.example.com]:2222",
	} {
		p, err := ParsePattern(s)
		if err != nil {
			t.Fatalf("ParsePattern(%q): %v", s, err)
		}
		if got := p.String(); got != s {
			t.Errorf("round trip: got %q, want %q", got, s)
		}
	}
	if _, err := ParsePattern("[host]2222"); err == nil {
		t.Error("expected error for malformed [host]:port pattern")
	}
	if _, err := ParsePattern("|1|not-base64"); err == nil {
		t.Error("expected error for malformed hashed pattern")
	}
}
