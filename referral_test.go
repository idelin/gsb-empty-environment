package netsec

import (
	"strings"
	"testing"
)

var testPolicy = Policy{
	TrustedHosts: []string{"dir1.example.com", "dir2.example.com", "dir3.example.com"},
	SearchRoot:   "dc=example,dc=org",
}

func tlsConn() ConnInfo {
	return ConnInfo{Host: "dir0.example.com", Port: 389, DN: "dc=example,dc=org", StartTLS: true}
}

func onlyDecision(t *testing.T, f *Follower, conn ConnInfo, referrals ...string) Decision {
	t.Helper()
	ds := f.Evaluate(conn, referrals)
	if len(ds) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(ds))
	}
	return ds[0]
}

func TestFollowsLdapsReferral(t *testing.T) {
	f := NewFollower(testPolicy)
	d := onlyDecision(t, f, tlsConn(), "ldaps://dir1.example.com/ou=people,dc=example,dc=org")
	if !d.Follow {
		t.Fatalf("expected follow, got refusal: %s", d.Reason)
	}
	if d.Port != 636 {
		t.Fatalf("expected default ldaps port 636, got %d", d.Port)
	}
	if d.Host != "dir1.example.com" || d.DN != "ou=people,dc=example,dc=org" {
		t.Fatalf("unexpected target %s:%d/%s", d.Host, d.Port, d.DN)
	}
	if !d.Rebind {
		t.Fatal("expected first followed referral to allow rebind")
	}
}

func TestRejectsDowngradeToPlaintext(t *testing.T) {
	f := NewFollower(testPolicy)
	d := onlyDecision(t, f, tlsConn(), "ldap://dir1.example.com/ou=people,dc=example,dc=org")
	if d.Follow {
		t.Fatal("plaintext ldap referral on a StartTLS connection must not be followed")
	}
	if !strings.Contains(d.Reason, "downgrade") {
		t.Fatalf("unexpected reason: %s", d.Reason)
	}
}

func TestRejectsPlaintextOnUnencryptedConnection(t *testing.T) {
	f := NewFollower(testPolicy)
	conn := ConnInfo{Host: "dir0.example.com", Port: 389}
	d := onlyDecision(t, f, conn, "ldap://dir1.example.com/dc=example,dc=org")
	if d.Follow {
		t.Fatal("plaintext referral without any encryption must not be followed")
	}
}

func TestFollowsStartTLSReferralWhenOriginUsedStartTLS(t *testing.T) {
	f := NewFollower(testPolicy)
	d := onlyDecision(t, f, tlsConn(),
		"ldap://dir1.example.com/ou=people,dc=example,dc=org??sub?(uid=alice)?!starttls")
	if !d.Follow {
		t.Fatalf("StartTLS-required referral should be followed, got: %s", d.Reason)
	}
	if d.Port != 389 {
		t.Fatalf("expected default ldap port 389, got %d", d.Port)
	}
}

func TestRejectsHostNotInWhitelist(t *testing.T) {
	f := NewFollower(testPolicy)
	d := onlyDecision(t, f, tlsConn(), "ldaps://evil.attacker.example/ou=people,dc=example,dc=org")
	if d.Follow {
		t.Fatal("referral to non-whitelisted host must not be followed")
	}
	if d.Rebind {
		t.Fatal("credentials must never be prepared for a non-whitelisted host")
	}
}

func TestWhitelistComparisonIsCaseInsensitive(t *testing.T) {
	f := NewFollower(testPolicy)
	d := onlyDecision(t, f, tlsConn(), "LDAPS://DIR1.Example.COM/ou=people,dc=example,dc=org")
	if !d.Follow {
		t.Fatalf("uppercase whitelisted host should be followed, got: %s", d.Reason)
	}
	if d.Host != "dir1.example.com" {
		t.Fatalf("host should be normalized to lowercase, got %q", d.Host)
	}
}

func TestRejectsReferralLoop(t *testing.T) {
	f := NewFollower(testPolicy)
	conn := tlsConn()

	d := onlyDecision(t, f, conn, "ldaps://dir1.example.com/ou=a,dc=example,dc=org")
	if !d.Follow {
		t.Fatalf("first hop should be followed, got: %s", d.Reason)
	}
	d = onlyDecision(t, f, conn, "ldaps://dir2.example.com/ou=b,dc=example,dc=org")
	if !d.Follow {
		t.Fatalf("second hop should be followed, got: %s", d.Reason)
	}
	// Server at dir2 points back at dir1's target: same host, port and DN.
	d = onlyDecision(t, f, conn, "ldaps://dir1.example.com:636/ou=a,dc=example,dc=org")
	if d.Follow {
		t.Fatal("referral loop must stop the chain")
	}
	if !strings.Contains(d.Reason, "loop") {
		t.Fatalf("unexpected reason: %s", d.Reason)
	}
}

func TestRejectsReferralBackToOrigin(t *testing.T) {
	f := NewFollower(testPolicy)
	policy := testPolicy
	policy.TrustedHosts = append(policy.TrustedHosts, "dir0.example.com")
	f = NewFollower(policy)
	d := onlyDecision(t, f, tlsConn(), "ldaps://dir0.example.com:636/dc=example,dc=org")
	// Origin was dir0:389/dc=example,dc=org; different port, so this is not
	// a loop, but a referral to the exact origin target is.
	if !d.Follow {
		t.Fatalf("different port should not count as loop, got: %s", d.Reason)
	}
	d = onlyDecision(t, f, tlsConn(), "ldaps://dir0.example.com:389/dc=example,dc=org")
	if d.Follow {
		t.Fatal("referral back to the original host, port and DN must be rejected as a loop")
	}
}

func TestRejectsDNOutsideSearchRoot(t *testing.T) {
	f := NewFollower(testPolicy)
	d := onlyDecision(t, f, tlsConn(), "ldaps://dir1.example.com/dc=evil,dc=attacker,dc=com")
	if d.Follow {
		t.Fatal("DN outside the search root must not be followed")
	}
	if !strings.Contains(d.Reason, "search root") {
		t.Fatalf("unexpected reason: %s", d.Reason)
	}
}

func TestRejectsDotDotDN(t *testing.T) {
	f := NewFollower(testPolicy)
	d := onlyDecision(t, f, tlsConn(), "ldaps://dir1.example.com/ou=..,dc=example,dc=org")
	if d.Follow {
		t.Fatal("DN containing '..' must not be followed")
	}
}

func TestRejectsPercentEncodedDotDotDN(t *testing.T) {
	f := NewFollower(testPolicy)
	// %2E%2E decodes to ".."; the check must happen after decoding.
	d := onlyDecision(t, f, tlsConn(), "ldaps://dir1.example.com/ou=%2E%2e,dc=example,dc=org")
	if d.Follow {
		t.Fatal("percent-encoded '..' DN must not be followed")
	}
	if !strings.Contains(d.Reason, "..") {
		t.Fatalf("unexpected reason: %s", d.Reason)
	}
}

func TestFollowsPercentEncodedDNWithinRoot(t *testing.T) {
	f := NewFollower(testPolicy)
	// Percent-encoding is decoded before the DN is compared to the root:
	// this decodes to ou=people,dc=example,dc=org and stays in scope.
	d := onlyDecision(t, f, tlsConn(), "ldaps://dir1.example.com/ou=people%2Cdc%3Dexample%2Cdc%3Dorg")
	if !d.Follow {
		t.Fatalf("encoded in-scope DN should be followed, got: %s", d.Reason)
	}
	if d.DN != "ou=people,dc=example,dc=org" {
		t.Fatalf("DN should be decoded and normalized, got %q", d.DN)
	}
}

func TestCredentialsReboundOnlyOnce(t *testing.T) {
	f := NewFollower(testPolicy)
	conn := tlsConn()

	d := onlyDecision(t, f, conn, "ldaps://dir1.example.com/ou=a,dc=example,dc=org")
	if !d.Follow || !d.Rebind {
		t.Fatalf("first hop should rebind credentials, got follow=%v rebind=%v", d.Follow, d.Rebind)
	}
	// Second hop: credentials must not be forwarded along the chain.
	d = onlyDecision(t, f, conn, "ldaps://dir2.example.com/ou=b,dc=example,dc=org")
	if !d.Follow {
		t.Fatalf("second hop should still be followed, got: %s", d.Reason)
	}
	if d.Rebind {
		t.Fatal("credentials must not be forwarded past the first rebind")
	}
	d = onlyDecision(t, f, conn, "ldaps://dir3.example.com/ou=c,dc=example,dc=org")
	if d.Rebind {
		t.Fatal("credentials must not be rebound a second time")
	}
}

func TestAnonymousConnectionStaysAnonymous(t *testing.T) {
	f := NewFollower(testPolicy)
	conn := tlsConn()
	conn.Anonymous = true

	d := onlyDecision(t, f, conn, "ldaps://dir1.example.com/ou=a,dc=example,dc=org")
	if !d.Follow {
		t.Fatalf("anonymous connection may still follow, got: %s", d.Reason)
	}
	if d.Rebind {
		t.Fatal("anonymous connection must never rebind credentials")
	}
}

func TestReferralDepthLimit(t *testing.T) {
	f := NewFollower(testPolicy)
	conn := tlsConn()
	hops := []string{
		"ldaps://dir1.example.com/ou=a,dc=example,dc=org",
		"ldaps://dir2.example.com/ou=b,dc=example,dc=org",
		"ldaps://dir3.example.com/ou=c,dc=example,dc=org",
	}
	for i, ref := range hops {
		d := onlyDecision(t, f, conn, ref)
		if !d.Follow {
			t.Fatalf("hop %d should be followed, got: %s", i+1, d.Reason)
		}
	}
	d := onlyDecision(t, f, conn, "ldaps://dir1.example.com/ou=d,dc=example,dc=org")
	if d.Follow {
		t.Fatal("fourth hop exceeds the maximum referral depth of 3")
	}
	if !strings.Contains(d.Reason, "depth") {
		t.Fatalf("unexpected reason: %s", d.Reason)
	}
}

func TestRejectsUnsupportedScheme(t *testing.T) {
	f := NewFollower(testPolicy)
	d := onlyDecision(t, f, tlsConn(), "http://dir1.example.com/dc=example,dc=org")
	if d.Follow {
		t.Fatal("non-LDAP scheme must not be followed")
	}
}
