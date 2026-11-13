package netsec

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"net"
	"testing"
)

// DER-encoded NameConstraints extension values.
const (
	// SEQUENCE { [0] SEQUENCE {} } -- permittedSubtrees present but empty.
	emptyPermitted = "3002a000"
	// permittedSubtrees = { dNSName "example.com" }
	permittedExampleCom = "3011a00f300d820b6578616d706c652e636f6d"
	// excludedSubtrees = { dNSName "example.com" }
	excludedExampleCom = "3011a10f300d820b6578616d706c652e636f6d"
	// permittedSubtrees = {} (empty), excludedSubtrees = { dNSName "example.com" }
	emptyPermittedExcludedExampleCom = "3013a000a10f300d820b6578616d706c652e636f6d"
	// permittedSubtrees = { iPAddress 10.0.0.0/255.255.255.0 }
	permittedIP = "300ea00c300a87080a000000ffffff00"
	// permittedSubtrees = { rfc822Name "example.com" }
	permittedEmail = "3011a00f300d810b6578616d706c652e636f6d"
	// permittedSubtrees = { uniformResourceIdentifier "example.com" }
	permittedURI = "3011a00f300d860b6578616d706c652e636f6d"
)

func mustParse(t *testing.T, hexDER string) *NameConstraints {
	t.Helper()
	der, err := hex.DecodeString(hexDER)
	if err != nil {
		t.Fatalf("bad test DER: %v", err)
	}
	nc, err := Parse(der)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return nc
}

func TestEmptyPermittedSubtreesDeniesAll(t *testing.T) {
	nc := mustParse(t, emptyPermitted)
	if !nc.PermittedPresent {
		t.Fatal("PermittedPresent = false, want true for present-but-empty permittedSubtrees")
	}
	if len(nc.Permitted) != 0 {
		t.Fatalf("len(Permitted) = %d, want 0", len(nc.Permitted))
	}
	if err := nc.CheckDNSName("www.example.com"); !errors.Is(err, ErrNoPermittedName) {
		t.Errorf("CheckDNSName = %v, want ErrNoPermittedName", err)
	}
	if err := nc.CheckEmail("user@example.com"); !errors.Is(err, ErrNoPermittedName) {
		t.Errorf("CheckEmail = %v, want ErrNoPermittedName", err)
	}
	if err := nc.CheckURI("https://example.com/"); !errors.Is(err, ErrNoPermittedName) {
		t.Errorf("CheckURI = %v, want ErrNoPermittedName", err)
	}
	if err := nc.CheckIP(net.ParseIP("10.1.2.3")); !errors.Is(err, ErrNoPermittedName) {
		t.Errorf("CheckIP = %v, want ErrNoPermittedName", err)
	}
}

func TestAbsentPermittedSubtreesAllows(t *testing.T) {
	nc := mustParse(t, excludedExampleCom)
	if nc.PermittedPresent {
		t.Fatal("PermittedPresent = true, want false when permittedSubtrees is absent")
	}
	if err := nc.CheckDNSName("www.other.com"); err != nil {
		t.Errorf("CheckDNSName(www.other.com) = %v, want nil", err)
	}
	if err := nc.CheckIP(net.ParseIP("192.0.2.1")); err != nil {
		t.Errorf("CheckIP = %v, want nil", err)
	}
}

func TestExcludedSubtreesReject(t *testing.T) {
	nc := mustParse(t, excludedExampleCom)
	for _, name := range []string{"example.com", "www.example.com", "EXAMPLE.COM"} {
		if err := nc.CheckDNSName(name); !errors.Is(err, ErrExcludedName) {
			t.Errorf("CheckDNSName(%q) = %v, want ErrExcludedName", name, err)
		}
	}
}

func TestEmptyPermittedWithExclusions(t *testing.T) {
	nc := mustParse(t, emptyPermittedExcludedExampleCom)
	if err := nc.CheckDNSName("evil.example.com"); !errors.Is(err, ErrExcludedName) {
		t.Errorf("CheckDNSName(evil.example.com) = %v, want ErrExcludedName", err)
	}
	if err := nc.CheckDNSName("other.com"); !errors.Is(err, ErrNoPermittedName) {
		t.Errorf("CheckDNSName(other.com) = %v, want ErrNoPermittedName", err)
	}
}

func TestPermittedDNSSubtree(t *testing.T) {
	nc := mustParse(t, permittedExampleCom)
	for _, name := range []string{"example.com", "www.example.com", "WWW.EXAMPLE.COM", "a.b.example.com"} {
		if err := nc.CheckDNSName(name); err != nil {
			t.Errorf("CheckDNSName(%q) = %v, want nil", name, err)
		}
	}
	for _, name := range []string{"other.com", "notexample.com", "example.com.evil.com", "example"} {
		if err := nc.CheckDNSName(name); !errors.Is(err, ErrNoPermittedName) {
			t.Errorf("CheckDNSName(%q) = %v, want ErrNoPermittedName", name, err)
		}
	}
}

func TestPermittedIPSubtree(t *testing.T) {
	nc := mustParse(t, permittedIP)
	if err := nc.CheckIP(net.ParseIP("10.0.0.7")); err != nil {
		t.Errorf("CheckIP(10.0.0.7) = %v, want nil", err)
	}
	if err := nc.CheckIP(net.ParseIP("10.0.1.7")); !errors.Is(err, ErrNoPermittedName) {
		t.Errorf("CheckIP(10.0.1.7) = %v, want ErrNoPermittedName", err)
	}
}

func TestPermittedEmailSubtree(t *testing.T) {
	nc := mustParse(t, permittedEmail)
	for _, addr := range []string{"user@example.com", "user@sub.example.com", "USER@EXAMPLE.COM"} {
		if err := nc.CheckEmail(addr); err != nil {
			t.Errorf("CheckEmail(%q) = %v, want nil", addr, err)
		}
	}
	if err := nc.CheckEmail("user@other.com"); !errors.Is(err, ErrNoPermittedName) {
		t.Errorf("CheckEmail(user@other.com) = %v, want ErrNoPermittedName", err)
	}
}

func TestPermittedURISubtree(t *testing.T) {
	nc := mustParse(t, permittedURI)
	if err := nc.CheckURI("https://www.example.com/path?q=1"); err != nil {
		t.Errorf("CheckURI = %v, want nil", err)
	}
	if err := nc.CheckURI("https://other.com/"); !errors.Is(err, ErrNoPermittedName) {
		t.Errorf("CheckURI = %v, want ErrNoPermittedName", err)
	}
}

func TestFromCertificate(t *testing.T) {
	der, _ := hex.DecodeString(emptyPermitted)
	cert := &x509.Certificate{
		Extensions: []pkix.Extension{{Id: OIDNameConstraints, Critical: true, Value: der}},
	}
	nc, err := FromCertificate(cert)
	if err != nil {
		t.Fatalf("FromCertificate: %v", err)
	}
	if nc == nil || !nc.PermittedPresent {
		t.Fatalf("FromCertificate = %+v, want present-but-empty permittedSubtrees", nc)
	}

	plain := &x509.Certificate{}
	nc, err = FromCertificate(plain)
	if err != nil || nc != nil {
		t.Fatalf("FromCertificate(no extension) = %v, %v; want nil, nil", nc, err)
	}
}

func TestCheckCertificate(t *testing.T) {
	nc := mustParse(t, permittedExampleCom)
	good := &x509.Certificate{DNSNames: []string{"www.example.com"}}
	if err := nc.CheckCertificate(good); err != nil {
		t.Errorf("CheckCertificate(good) = %v, want nil", err)
	}
	bad := &x509.Certificate{DNSNames: []string{"www.example.com", "evil.com"}}
	if err := nc.CheckCertificate(bad); !errors.Is(err, ErrNoPermittedName) {
		t.Errorf("CheckCertificate(bad) = %v, want ErrNoPermittedName", err)
	}

	empty := mustParse(t, emptyPermitted)
	leaf := &x509.Certificate{DNSNames: []string{"www.example.com"}}
	if err := empty.CheckCertificate(leaf); !errors.Is(err, ErrNoPermittedName) {
		t.Errorf("CheckCertificate with empty permittedSubtrees = %v, want ErrNoPermittedName", err)
	}
}

func TestNilConstraintsAllowEverything(t *testing.T) {
	var nc *NameConstraints
	if err := nc.CheckDNSName("anything.example"); err != nil {
		t.Errorf("nil constraints CheckDNSName = %v, want nil", err)
	}
	if err := nc.CheckCertificate(&x509.Certificate{DNSNames: []string{"a.b.c"}}); err != nil {
		t.Errorf("nil constraints CheckCertificate = %v, want nil", err)
	}
}

func TestParseRejectsTrailingData(t *testing.T) {
	der, _ := hex.DecodeString(emptyPermitted)
	if _, err := Parse(append(der, 0x00)); err == nil {
		t.Fatal("Parse with trailing data = nil error, want error")
	}
}
