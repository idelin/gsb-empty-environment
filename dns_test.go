package netsec_test

import (
	"crypto/x509"
	"errors"
	"testing"

	"example.org/netsec/q0253"
)

func TestMatchDNSNameConstraint(t *testing.T) {
	tests := []struct {
		name       string
		constraint string
		want       bool
		wantErr    bool
	}{
		{name: "anything.example", constraint: "", want: true},
		{name: "example.com", constraint: "example.com", want: true},
		{name: "a.example.com", constraint: "example.com", want: true},
		{name: "a.b.example.com", constraint: "example.com", want: true},
		{name: "ExAmPle.CoM", constraint: "example.com", want: true},
		{name: "a.example.com", constraint: "ExAmPle.CoM", want: true},
		{name: "example.com", constraint: ".example.com", want: true},
		{name: "a.example.com", constraint: ".example.com", want: true},
		{name: "a.b.example.com", constraint: ".example.com", want: true},
		{name: "ExAmPle.CoM", constraint: ".example.com", want: true},
		{name: ".example.com", constraint: "example.com", wantErr: true},
		{name: "notexample.com", constraint: "example.com", want: false},
		{name: "example.com.evil", constraint: "example.com", want: false},
		{name: "a.example.com", constraint: "example.com.evil", want: false},
		{name: "example.com", constraint: "example.com.", wantErr: true},
		{name: "example.com.", constraint: "example.com", wantErr: true},
		{name: "www..example.com", constraint: ".example.com", wantErr: true},
		{name: "example.com", constraint: ".", wantErr: true},
		{name: "example.com", constraint: "..example.com", wantErr: true},
		{name: "bad\x00name.example.com", constraint: "example.com", wantErr: true},
		{name: "bad\x00name.example.com", constraint: "", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name+"/"+test.constraint, func(t *testing.T) {
			got, err := netsec.MatchDNSNameConstraint(test.name, test.constraint)
			if test.wantErr {
				if err == nil {
					t.Fatalf("MatchDNSNameConstraint(%q, %q) returned no error", test.name, test.constraint)
				}
				return
			}
			if err != nil {
				t.Fatalf("MatchDNSNameConstraint(%q, %q) returned error: %v", test.name, test.constraint, err)
			}
			if got != test.want {
				t.Fatalf("MatchDNSNameConstraint(%q, %q) = %v, want %v", test.name, test.constraint, got, test.want)
			}
		})
	}
}

func TestLeadingDotIsEquivToBareConstraint(t *testing.T) {
	names := []string{"example.com", "a.example.com", "a.b.example.com", "other.example"}
	for _, name := range names {
		bare, bareErr := netsec.MatchDNSNameConstraint(name, "example.com")
		dotted, dottedErr := netsec.MatchDNSNameConstraint(name, ".example.com")
		if bareErr != nil || dottedErr != nil {
			t.Fatalf("name %q returned errors: %v, %v", name, bareErr, dottedErr)
		}
		if bare != dotted {
			t.Fatalf("name %q: bare constraint = %v, dotted constraint = %v", name, bare, dotted)
		}
	}
}

func TestMatchDomainConstraintAlias(t *testing.T) {
	got, err := netsec.MatchDomainConstraint("example.com", ".example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("MatchDomainConstraint(example.com, .example.com) = false, want true")
	}
}

func TestDNSNameMatchesConstraint(t *testing.T) {
	if !netsec.DNSNameMatchesConstraint("example.com", ".example.com") {
		t.Fatal("valid dotted constraint did not match")
	}
	if netsec.DNSNameMatchesConstraint("example.com", ".") {
		t.Fatal("invalid constraint matched")
	}
}

func TestCheckDNSNameConstraints(t *testing.T) {
	tests := []struct {
		name              string
		permitted         []string
		excluded          []string
		wantErr           error
		invalidConstraint bool
		invalidName       bool
	}{
		{name: "example.com", permitted: []string{".example.com"}},
		{name: "a.example.com", permitted: []string{".example.com"}},
		{name: "other.example", permitted: []string{".example.com"}, wantErr: netsec.ErrDNSNameNotPermitted},
		{name: "example.com", excluded: []string{".example.com"}, wantErr: netsec.ErrDNSNameExcluded},
		{name: "other.example", excluded: []string{".example.com"}},
		{name: "example.com", permitted: []string{".example.com"}, excluded: []string{"a.example.com"}},
		{name: "a.example.com", permitted: []string{".example.com"}, excluded: []string{"a.example.com"}, wantErr: netsec.ErrDNSNameExcluded},
		{name: "example.com", permitted: []string{"."}, wantErr: nil, invalidConstraint: true},
		{name: "example.com", excluded: []string{"..example.com"}, wantErr: nil, invalidConstraint: true},
		{name: "bad\x00name.example.com", wantErr: nil, invalidName: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := netsec.CheckDNSNameConstraints(test.name, test.permitted, test.excluded)
			if test.invalidConstraint || test.invalidName {
				if err == nil {
					t.Fatalf("CheckDNSNameConstraints(%q, %v, %v) returned no error", test.name, test.permitted, test.excluded)
				}
				return
			}
			if test.wantErr == nil {
				if err != nil {
					t.Fatalf("CheckDNSNameConstraints(%q, %v, %v) returned error: %v", test.name, test.permitted, test.excluded, err)
				}
				return
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("CheckDNSNameConstraints(%q, %v, %v) error = %v, want %v", test.name, test.permitted, test.excluded, err, test.wantErr)
			}
		})
	}
}

func TestCheckCertificateNameConstraints(t *testing.T) {
	cert := &x509.Certificate{
		PermittedDNSDomains: []string{".example.com"},
	}
	if err := netsec.CheckCertificateNameConstraints(cert, "example.com"); err != nil {
		t.Fatalf("base domain rejected: %v", err)
	}
	if err := netsec.CheckCertificateNameConstraints(cert, "a.example.com"); err != nil {
		t.Fatalf("subdomain rejected: %v", err)
	}
	if err := netsec.CheckCertificateNameConstraints(cert, "other.example"); !errors.Is(err, netsec.ErrDNSNameNotPermitted) {
		t.Fatalf("unrelated name error = %v, want ErrDNSNameNotPermitted", err)
	}
	if err := netsec.CheckCertificateNameConstraints(nil, "example.com"); err == nil {
		t.Fatal("nil certificate accepted")
	}
}
