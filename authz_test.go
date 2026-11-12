package netsec

import (
	"errors"
	"net/netip"
	"testing"
	"time"
)

var (
	baseTime = time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	peerIP   = netip.MustParseAddr("10.20.30.40")
	outside  = netip.MustParseAddr("192.168.99.99")
)

func newTestAuthorizer(t *testing.T, ca *testCA) *Authorizer {
	t.Helper()
	now := baseTime
	a, err := NewAuthorizer(ca.pub, WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("NewAuthorizer: %v", err)
	}
	return a
}

func validSpec() certSpec {
	return certSpec{
		serial:      7,
		certType:    CertTypeUser,
		keyID:       "session-gateway-leaf",
		principals:  []string{"alice", "ops"},
		validAfter:  baseTime.Add(-time.Hour),
		validBefore: baseTime.Add(time.Hour),
		criticalOptions: map[string]string{
			OptionForceCommand:  "/usr/local/bin/jump-shell",
			OptionSourceAddress: "10.20.30.0/24,192.168.1.0/24",
		},
		extensions: map[string]string{"permit-pty": ""},
	}
}

func validRequest(raw []byte) Request {
	return Request{
		RawCertificate:   raw,
		Login:            "alice",
		RemoteAddr:       peerIP,
		RequestedCommand: "rm -rf /",
	}
}

func TestAuthorize_ValidCertificate(t *testing.T) {
	ca := newEd25519CA()
	authz := newTestAuthorizer(t, ca)
	raw := ca.mint(validSpec())

	decision, err := authz.Authorize(validRequest(raw))
	if err != nil {
		t.Fatalf("expected authorization, got %v", err)
	}
	if decision.Principal != "alice" {
		t.Fatalf("principal = %q, want alice", decision.Principal)
	}
	if !decision.ForceCommand || decision.Command != "/usr/local/bin/jump-shell" {
		t.Fatalf("force command not enforced: %+v", decision)
	}
	if decision.KeyID != "session-gateway-leaf" {
		t.Fatalf("key id = %q", decision.KeyID)
	}
}

func TestAuthorize_ForceCommandIgnoresClientRequest(t *testing.T) {
	ca := newEd25519CA()
	authz := newTestAuthorizer(t, ca)
	spec := validSpec()
	spec.criticalOptions = map[string]string{OptionForceCommand: "fixed"}
	raw := ca.mint(spec)

	decision, err := authz.Authorize(validRequest(raw))
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if decision.Command != "fixed" {
		t.Fatalf("client command leaked through: %q", decision.Command)
	}

	// Without force-command the client request stands.
	spec.criticalOptions = nil
	raw = ca.mint(spec)
	decision, err = authz.Authorize(validRequest(raw))
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if decision.ForceCommand || decision.Command != "rm -rf /" {
		t.Fatalf("client command should be honored without force-command: %+v", decision)
	}
}

func TestAuthorize_ExpiredCertificate(t *testing.T) {
	cases := []struct {
		name  string
		after time.Time
		bef   time.Time
	}{
		{"already expired", baseTime.Add(-2 * time.Hour), baseTime.Add(-time.Minute)},
		{"not yet valid", baseTime.Add(time.Minute), baseTime.Add(2 * time.Hour)},
		{"expires exactly now (half-open)", baseTime.Add(-time.Hour), baseTime},
		{"valid exactly at lower bound", baseTime, baseTime.Add(time.Hour)},
	}

	ca := newEd25519CA()
	authz := newTestAuthorizer(t, ca)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := validSpec()
			spec.validAfter, spec.validBefore = tc.after, tc.bef
			raw := ca.mint(spec)
			_, err := authz.Authorize(validRequest(raw))

			wantDenied := tc.name != "valid exactly at lower bound"
			if wantDenied && !errors.Is(err, ErrCertificateExpired) {
				t.Fatalf("want ErrCertificateExpired, got %v", err)
			}
			if !wantDenied && err != nil {
				t.Fatalf("lower boundary must be accepted, got %v", err)
			}
		})
	}
}

func TestAuthorize_SourceAddressOutsideCIDR(t *testing.T) {
	ca := newEd25519CA()
	authz := newTestAuthorizer(t, ca)
	raw := ca.mint(validSpec())

	req := validRequest(raw)
	req.RemoteAddr = outside
	_, err := authz.Authorize(req)
	if !errors.Is(err, ErrSourceAddressDenied) {
		t.Fatalf("want ErrSourceAddressDenied, got %v", err)
	}

	// In-range peer passes.
	req.RemoteAddr = netip.MustParseAddr("192.168.1.7")
	if _, err := authz.Authorize(req); err != nil {
		t.Fatalf("in-range peer rejected: %v", err)
	}

	// Bare IP entries act as /32 prefixes.
	spec := validSpec()
	spec.criticalOptions = map[string]string{OptionSourceAddress: "10.20.30.40"}
	raw = ca.mint(spec)
	if _, err := authz.Authorize(validRequest(raw)); err != nil {
		t.Fatalf("bare-IP match failed: %v", err)
	}

	// v4 peer against v6-only list cannot match.
	spec.criticalOptions = map[string]string{OptionSourceAddress: "2001:db8::/32"}
	raw = ca.mint(spec)
	if _, err := authz.Authorize(validRequest(raw)); !errors.Is(err, ErrSourceAddressDenied) {
		t.Fatalf("family mismatch must deny, got %v", err)
	}
}

func TestAuthorize_UnknownCriticalOption(t *testing.T) {
	ca := newEd25519CA()
	authz := newTestAuthorizer(t, ca)
	spec := validSpec()
	spec.criticalOptions = map[string]string{
		OptionForceCommand:                "/bin/sh",
		"permit-port-forwarding-vendor-x": "yes",
	}
	raw := ca.mint(spec)

	_, err := authz.Authorize(validRequest(raw))
	if !errors.Is(err, ErrUnknownCriticalOption) {
		t.Fatalf("want ErrUnknownCriticalOption, got %v", err)
	}
}

func TestAuthorize_PrincipalMismatch(t *testing.T) {
	ca := newEd25519CA()
	authz := newTestAuthorizer(t, ca)
	raw := ca.mint(validSpec())

	req := validRequest(raw)
	req.Login = "bob"
	if _, err := authz.Authorize(req); !errors.Is(err, ErrPrincipalMismatch) {
		t.Fatalf("want ErrPrincipalMismatch, got %v", err)
	}

	// Login mapping rewrites the name before principal lookup.
	mapped, err := NewAuthorizer(ca.pub,
		WithClock(func() time.Time { return baseTime }),
		WithLoginMapper(func(login string) (string, error) {
			if login == "bob" {
				return "ops", nil
			}
			return login, nil
		}),
	)
	if err != nil {
		t.Fatalf("NewAuthorizer: %v", err)
	}
	req = validRequest(raw)
	req.Login = "bob"
	decision, err := mapped.Authorize(req)
	if err != nil {
		t.Fatalf("mapped login denied: %v", err)
	}
	if decision.Principal != "ops" {
		t.Fatalf("principal = %q, want ops", decision.Principal)
	}
}

func TestAuthorize_UntrustedCAAndTampering(t *testing.T) {
	ca := newEd25519CA()
	other := newEd25519CA()
	authz := newTestAuthorizer(t, ca)

	// Signed by a different CA.
	raw := other.mint(validSpec())
	if _, err := authz.Authorize(validRequest(raw)); !errors.Is(err, ErrUntrustedCA) {
		t.Fatalf("foreign CA want ErrUntrustedCA, got %v", err)
	}

	// Tampered after signing.
	raw = ca.mint(validSpec())
	tampered := append([]byte(nil), raw...)
	tampered[len(tampered)-1] ^= 0xff
	if _, err := authz.Authorize(validRequest(tampered)); err == nil {
		t.Fatal("tampered certificate must be rejected")
	}
}

func TestAuthorize_HostCertificateRejected(t *testing.T) {
	ca := newEd25519CA()
	authz := newTestAuthorizer(t, ca)
	spec := validSpec()
	spec.certType = 2 // SSH_CERT_TYPE_HOST
	raw := ca.mint(spec)

	if _, err := authz.Authorize(validRequest(raw)); !errors.Is(err, ErrNotUserCertificate) {
		t.Fatalf("want ErrNotUserCertificate, got %v", err)
	}
}

func TestAuthorize_RSAAndECDSACA(t *testing.T) {
	for _, makeCA := range []func() *testCA{newRSACA, newECDSACA} {
		ca := makeCA()
		authz := newTestAuthorizer(t, ca)
		raw := ca.mint(validSpec())
		if _, err := authz.Authorize(validRequest(raw)); err != nil {
			t.Fatalf("%s CA authorization failed: %v", ca.pub.Type(), err)
		}
	}
}

func TestParseCertificate_RoundTrip(t *testing.T) {
	ca := newEd25519CA()
	raw := ca.mint(validSpec())
	cert, err := ParseCertificate(raw)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	if cert.Type != CertTypeUser || cert.Serial != 7 {
		t.Fatalf("unexpected cert fields: %+v", cert)
	}
	if cert.CriticalOptions[OptionForceCommand] != "/usr/local/bin/jump-shell" {
		t.Fatalf("force-command not parsed: %v", cert.CriticalOptions)
	}
	if err := cert.verifyWith(ca.pub); err != nil {
		t.Fatalf("verifyWith: %v", err)
	}
	if _, err := ParseCertificate(raw[:len(raw)-5]); !errors.Is(err, ErrMalformedCertificate) {
		t.Fatalf("truncated blob want malformed, got %v", err)
	}
}

func TestParseAuthorizedKey_RoundTrip(t *testing.T) {
	ca := newEd25519CA()
	line := EncodeAuthorizedKey(ca.pub, "jump-ca")
	parsed, err := ParseAuthorizedKey(line)
	if err != nil {
		t.Fatalf("ParseAuthorizedKey: %v", err)
	}
	if parsed.Type() != ca.pub.Type() || parsed.Fingerprint() != ca.pub.Fingerprint() {
		t.Fatal("authorized key round trip mismatch")
	}
	if _, err := ParseAuthorizedKey("# comment"); !errors.Is(err, ErrMalformedPublicKey) {
		t.Fatalf("comment line want malformed, got %v", err)
	}
}

func TestNewAuthorizer_RequiresCA(t *testing.T) {
	if _, err := NewAuthorizer(nil); err == nil {
		t.Fatal("nil CA must be rejected")
	}
}
