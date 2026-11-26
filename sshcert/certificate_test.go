package sshcert

import (
	"errors"
	"net"
	"testing"
)

// sourceAddressValue wraps the comma-separated spec in the inner SSH string
// required by the critical option encoding.
func sourceAddressValue(spec string) []byte {
	var w wireWriter
	w.string([]byte(spec))
	return w.buf
}

// stringValue wraps a plain string as an SSH string payload.
func stringValue(s string) []byte {
	var w wireWriter
	w.string([]byte(s))
	return w.buf
}

func TestParseCertificateAllFields(t *testing.T) {
	ca, leaf := generateTestKey(t), generateTestKey(t)
	spec := defaultCertSpec(ca, leaf)
	spec.serial = 0x0102030405060708
	spec.criticalOptions = []CriticalOption{
		{Name: CriticalSourceAddress, Value: sourceAddressValue("10.0.0.0/8,192.168.1.5")},
		{Name: CriticalForceCommand, Value: stringValue("/usr/bin/id")},
		{Name: CriticalVerifyRequired, Value: []byte{}},
	}
	raw := buildCert(t, spec)

	c, err := ParseCertificate(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(c.Nonce) != 32 {
		t.Fatalf("nonce not decoded: %x", c.Nonce)
	}
	if !bytesEqual(c.PublicKey, leaf.pub) {
		t.Fatal("leaf public key mismatch")
	}
	if c.Serial != spec.serial || c.Type != CertTypeUser || c.KeyID != spec.keyID {
		t.Fatal("scalar field mismatch")
	}
	if len(c.ValidPrincipals) != 2 || c.ValidPrincipals[0] != "alice" {
		t.Fatalf("principals mismatch: %v", c.ValidPrincipals)
	}
	if c.ValidAfter != 1000 || c.ValidBefore != 2000 {
		t.Fatal("validity window mismatch")
	}
	if len(c.CriticalOptions) != 3 || len(c.Extensions) != 1 {
		t.Fatalf("map length mismatch: %+v / %+v", c.CriticalOptions, c.Extensions)
	}
	if !bytesEqual(c.SignatureKey, ca.sshBlob) {
		t.Fatal("signature key blob mismatch")
	}
	if err := c.VerifySignature(ca.sshBlob); err != nil {
		t.Fatalf("signature should verify: %v", err)
	}
}

func TestRejectNonEd25519Certificate(t *testing.T) {
	var w wireWriter
	w.string([]byte("rsa-sha2-256-cert-v01@openssh.com"))
	if _, err := ParseCertificate(w.stringPrefixed()); !errors.Is(err, ErrNotEd25519Cert) {
		t.Fatalf("want ErrNotEd25519Cert, got %v", err)
	}
}

func TestRejectTruncatedAndTrailing(t *testing.T) {
	ca, leaf := generateTestKey(t), generateTestKey(t)
	raw := buildCert(t, defaultCertSpec(ca, leaf))

	for _, cut := range []int{4, 10, len(raw) - 1, len(raw) - 5} {
		if _, err := ParseCertificate(raw[:cut]); !errors.Is(err, ErrTruncated) {
			t.Fatalf("cut=%d want ErrTruncated, got %v", cut, err)
		}
	}

	trailing := append(append([]byte(nil), raw...), 0xAA)
	if _, err := ParseCertificate(trailing); !errors.Is(err, ErrTrailingData) {
		t.Fatalf("want ErrTrailingData, got %v", err)
	}

	// A declared length overrunning the buffer must be rejected.
	evil := append([]byte(nil), raw...)
	evil[0] = 0x7f
	if _, err := ParseCertificate(evil); err == nil {
		t.Fatal("over-long declared length accepted")
	}
}

func TestRejectBadCertType(t *testing.T) {
	ca, leaf := generateTestKey(t), generateTestKey(t)
	spec := defaultCertSpec(ca, leaf)
	spec.certType = 99
	if _, err := ParseCertificate(buildCert(t, spec)); !errors.Is(err, ErrBadCertType) {
		t.Fatalf("want ErrBadCertType, got %v", err)
	}
}

func TestRejectDuplicateMapEntries(t *testing.T) {
	ca, leaf := generateTestKey(t), generateTestKey(t)

	// Hand-build a critical-options blob containing the same name twice;
	// the regular cert builder would silently collapse nothing but cannot
	// express a duplicate through the struct slice.
	var crit wireWriter
	crit.string([]byte(CriticalSourceAddress))
	crit.string(sourceAddressValue("10.0.0.0/8"))
	crit.string([]byte(CriticalSourceAddress))
	crit.string(sourceAddressValue("10.0.0.0/8"))

	spec := defaultCertSpec(ca, leaf)
	spec.criticalOptions = []CriticalOption{
		{Name: CriticalSourceAddress, Value: sourceAddressValue("10.0.0.0/8")},
	}
	dup := certBodyWithCriticalBlob(t, spec, crit.buf)
	if _, err := ParseCertificate(dup); !errors.Is(err, ErrDuplicateMapEntry) {
		t.Fatalf("want ErrDuplicateMapEntry, got %v", err)
	}
}

func TestSignatureVerificationFailures(t *testing.T) {
	ca, otherCA, leaf := generateTestKey(t), generateTestKey(t), generateTestKey(t)

	raw := buildCert(t, defaultCertSpec(ca, leaf))
	c, err := ParseCertificate(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.VerifySignature(otherCA.sshBlob); !errors.Is(err, ErrCAKeyMismatch) {
		t.Fatalf("want ErrCAKeyMismatch, got %v", err)
	}

	bad := buildCertBadSignature(t, defaultCertSpec(ca, leaf))
	badCert, err := ParseCertificate(bad)
	if err != nil {
		t.Fatal(err)
	}
	if err := badCert.VerifySignature(ca.sshBlob); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("want ErrSignatureInvalid, got %v", err)
	}

	// Flip a byte inside the signed body and confirm verification fails.
	tampered := append([]byte(nil), raw...)
	tampered[len(tampered)-40] ^= 0x01
	tamperedCert, perr := ParseCertificate(tampered)
	if perr == nil {
		if err := tamperedCert.VerifySignature(ca.sshBlob); err == nil {
			t.Fatal("tampered certificate verified")
		}
	}
}

func TestUnknownCriticalOptionRejected(t *testing.T) {
	ca, leaf := generateTestKey(t), generateTestKey(t)
	spec := defaultCertSpec(ca, leaf)
	spec.criticalOptions = []CriticalOption{
		{Name: "permit-pty", Value: []byte{}},
	}
	c, err := ParseCertificate(buildCert(t, spec))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Verify(c, VerifyOptions{
		ExpectedType:    CertTypeUser,
		CAPublicKeyBlob: ca.sshBlob,
		Principal:       "alice",
		Now:             1500,
	})
	if !errors.Is(err, ErrUnknownCriticalOption) {
		t.Fatalf("want ErrUnknownCriticalOption, got %v", err)
	}

	if _, err := Verify(c, VerifyOptions{
		ExpectedType:           CertTypeUser,
		CAPublicKeyBlob:        ca.sshBlob,
		Principal:              "alice",
		Now:                    1500,
		AllowedCriticalOptions: []string{"permit-pty"},
	}); err != nil {
		t.Fatalf("opt-in critical option rejected: %v", err)
	}
}

func TestLifetimeEnforcement(t *testing.T) {
	ca, leaf := generateTestKey(t), generateTestKey(t)
	c, err := ParseCertificate(buildCert(t, defaultCertSpec(ca, leaf)))
	if err != nil {
		t.Fatal(err)
	}
	base := func(now int64) VerifyOptions {
		return VerifyOptions{
			ExpectedType:    CertTypeUser,
			CAPublicKeyBlob: ca.sshBlob,
			Principal:       "alice",
			Now:             now,
		}
	}

	if _, err := Verify(c, base(1000)); err != nil {
		t.Fatalf("lower boundary should pass: %v", err)
	}
	if _, err := Verify(c, base(1999)); err != nil {
		t.Fatalf("inside window should pass: %v", err)
	}
	if _, err := Verify(c, base(999)); !errors.Is(err, ErrCertNotYetValid) {
		t.Fatalf("want ErrCertNotYetValid, got %v", err)
	}
	if _, err := Verify(c, base(2000)); !errors.Is(err, ErrCertExpired) {
		t.Fatalf("want ErrCertExpired, got %v", err)
	}

	spec := defaultCertSpec(ca, leaf)
	spec.validAfter, spec.validBefore = 0, 0
	open, err := ParseCertificate(buildCert(t, spec))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(open, base(1<<40)); err != nil {
		t.Fatalf("unbounded cert rejected: %v", err)
	}
}

func TestPrincipalEnforcement(t *testing.T) {
	ca, leaf := generateTestKey(t), generateTestKey(t)
	c, err := ParseCertificate(buildCert(t, defaultCertSpec(ca, leaf)))
	if err != nil {
		t.Fatal(err)
	}
	base := func(principal string, wildcard bool) VerifyOptions {
		return VerifyOptions{
			ExpectedType:           CertTypeUser,
			CAPublicKeyBlob:        ca.sshBlob,
			Principal:              principal,
			AllowWildcardPrincipal: wildcard,
			Now:                    1500,
		}
	}

	if _, err := Verify(c, base("alice", false)); err != nil {
		t.Fatalf("listed principal rejected: %v", err)
	}
	if _, err := Verify(c, base("bob", false)); !errors.Is(err, ErrPrincipalNotAllowed) {
		t.Fatalf("want ErrPrincipalNotAllowed, got %v", err)
	}

	spec := defaultCertSpec(ca, leaf)
	spec.principals = nil
	empty, err := ParseCertificate(buildCert(t, spec))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(empty, base("", false)); !errors.Is(err, ErrPrincipalNotAllowed) {
		t.Fatalf("empty list wildcard should be denied by default: %v", err)
	}
	if _, err := Verify(empty, base("", true)); err != nil {
		t.Fatalf("explicit wildcard opt-in failed: %v", err)
	}
}

func TestCertTypeMismatch(t *testing.T) {
	ca, leaf := generateTestKey(t), generateTestKey(t)
	c, _ := ParseCertificate(buildCert(t, defaultCertSpec(ca, leaf)))
	_, err := Verify(c, VerifyOptions{
		ExpectedType:    CertTypeHost,
		CAPublicKeyBlob: ca.sshBlob,
		Principal:       "alice",
		Now:             1500,
	})
	if !errors.Is(err, ErrCertTypeMismatch) {
		t.Fatalf("want ErrCertTypeMismatch, got %v", err)
	}
}

func TestSourceAddressEnforcement(t *testing.T) {
	ca, leaf := generateTestKey(t), generateTestKey(t)
	spec := defaultCertSpec(ca, leaf)
	spec.criticalOptions = []CriticalOption{
		{Name: CriticalSourceAddress, Value: sourceAddressValue("10.0.0.0/8,fd00::/16")},
	}
	c, err := ParseCertificate(buildCert(t, spec))
	if err != nil {
		t.Fatal(err)
	}
	base := func(ip net.IP) VerifyOptions {
		return VerifyOptions{
			ExpectedType:    CertTypeUser,
			CAPublicKeyBlob: ca.sshBlob,
			Principal:       "alice",
			Now:             1500,
			RemoteIP:        ip,
		}
	}

	if _, err := Verify(c, base(net.ParseIP("10.9.9.9"))); err != nil {
		t.Fatalf("IPv4 in CIDR rejected: %v", err)
	}
	if _, err := Verify(c, base(net.ParseIP("fd00::1234"))); err != nil {
		t.Fatalf("IPv6 in CIDR rejected: %v", err)
	}
	if _, err := Verify(c, base(net.ParseIP("192.168.0.1"))); !errors.Is(err, ErrSourceAddressDenied) {
		t.Fatalf("want ErrSourceAddressDenied, got %v", err)
	}
	if _, err := Verify(c, base(nil)); !errors.Is(err, ErrSourceAddressRequired) {
		t.Fatalf("want ErrSourceAddressRequired, got %v", err)
	}

	literalSpec := defaultCertSpec(ca, leaf)
	literalSpec.criticalOptions = []CriticalOption{
		{Name: CriticalSourceAddress, Value: sourceAddressValue("172.16.0.7")},
	}
	literalCert, _ := ParseCertificate(buildCert(t, literalSpec))
	if _, err := Verify(literalCert, base(net.ParseIP("172.16.0.7"))); err != nil {
		t.Fatalf("bare IP match failed: %v", err)
	}
	if _, err := Verify(literalCert, base(net.ParseIP("172.16.0.8"))); !errors.Is(err, ErrSourceAddressDenied) {
		t.Fatalf("adjacent IP should be denied: %v", err)
	}

	// A malformed CIDR entry must reject the certificate rather than be
	// skipped.
	badSpec := defaultCertSpec(ca, leaf)
	badSpec.criticalOptions = []CriticalOption{
		{Name: CriticalSourceAddress, Value: sourceAddressValue("not-an-ip/99")},
	}
	badCert, _ := ParseCertificate(buildCert(t, badSpec))
	if _, err := Verify(badCert, base(net.ParseIP("10.0.0.1"))); !errors.Is(err, ErrBadSourceAddress) {
		t.Fatalf("want ErrBadSourceAddress, got %v", err)
	}
}

func TestForceCommandReturned(t *testing.T) {
	ca, leaf := generateTestKey(t), generateTestKey(t)
	spec := defaultCertSpec(ca, leaf)
	spec.criticalOptions = []CriticalOption{
		{Name: CriticalForceCommand, Value: stringValue("/sbin/uptime --pretty")},
	}
	c, err := ParseCertificate(buildCert(t, spec))
	if err != nil {
		t.Fatal(err)
	}
	res, err := Verify(c, VerifyOptions{
		ExpectedType:    CertTypeUser,
		CAPublicKeyBlob: ca.sshBlob,
		Principal:       "alice",
		Now:             1500,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.ForceCommandPresent || res.ForceCommand != "/sbin/uptime --pretty" {
		t.Fatalf("force command not surfaced: %+v", res)
	}

	plain, _ := ParseCertificate(buildCert(t, defaultCertSpec(ca, leaf)))
	res, err = Verify(plain, VerifyOptions{
		ExpectedType:    CertTypeUser,
		CAPublicKeyBlob: ca.sshBlob,
		Principal:       "alice",
		Now:             1500,
	})
	if err != nil || res.ForceCommandPresent {
		t.Fatalf("unexpected force command on plain cert: %+v %v", res, err)
	}

	// A force-command option whose payload is not a single SSH string must
	// reject the certificate instead of silently dropping the pinned command.
	malformedSpec := defaultCertSpec(ca, leaf)
	malformedSpec.criticalOptions = []CriticalOption{
		{Name: CriticalForceCommand, Value: []byte{0x00, 0x00}},
	}
	malformed, err := ParseCertificate(buildCert(t, malformedSpec))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(malformed, VerifyOptions{
		ExpectedType:    CertTypeUser,
		CAPublicKeyBlob: ca.sshBlob,
		Principal:       "alice",
		Now:             1500,
	}); !errors.Is(err, ErrBadForceCommand) {
		t.Fatalf("want ErrBadForceCommand, got %v", err)
	}
}
