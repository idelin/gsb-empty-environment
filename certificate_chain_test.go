package netsec

import (
	"crypto/x509"
	"reflect"
	"testing"
)

func TestSignatureSchemeForCertificate(t *testing.T) {
	tests := []struct {
		algorithm x509.SignatureAlgorithm
		want      SignatureScheme
	}{
		{x509.SHA1WithRSA, PKCS1WithSHA1},
		{x509.SHA256WithRSA, PKCS1WithSHA256},
		{x509.SHA384WithRSA, PKCS1WithSHA384},
		{x509.SHA512WithRSA, PKCS1WithSHA512},
		{x509.SHA256WithRSAPSS, PSSWithSHA256},
		{x509.SHA384WithRSAPSS, PSSWithSHA384},
		{x509.SHA512WithRSAPSS, PSSWithSHA512},
		{x509.ECDSAWithSHA256, ECDSAWithP256AndSHA256},
		{x509.ECDSAWithSHA384, ECDSAWithP384AndSHA384},
		{x509.ECDSAWithSHA512, ECDSAWithP521AndSHA512},
		{x509.PureEd25519, Ed25519},
	}
	for _, test := range tests {
		got, ok := SignatureSchemeForCertificate(&x509.Certificate{SignatureAlgorithm: test.algorithm})
		if !ok {
			t.Fatalf("SignatureSchemeForCertificate(%v) not supported", test.algorithm)
		}
		if got != test.want {
			t.Fatalf("SignatureSchemeForCertificate(%v) = %v, want %v", test.algorithm, got, test.want)
		}
	}

	if _, ok := SignatureSchemeForCertificate(&x509.Certificate{SignatureAlgorithm: x509.MD5WithRSA}); ok {
		t.Fatal("MD5 certificate unexpectedly mapped to a TLS signature scheme")
	}
	if _, ok := SignatureSchemeForCertificate(nil); ok {
		t.Fatal("nil certificate unexpectedly supported")
	}
}

func TestCertificateChainUsesCertificateExtensionList(t *testing.T) {
	weak := &x509.Certificate{SignatureAlgorithm: x509.SHA1WithRSA}
	strong := &x509.Certificate{SignatureAlgorithm: x509.SHA256WithRSAPSS}

	handshake := []SignatureScheme{PKCS1WithSHA1, PSSWithSHA256}
	cert := []SignatureScheme{PSSWithSHA256}

	if CertificateChainAllowed([]*x509.Certificate{weak}, handshake, cert, true) {
		t.Fatal("weak certificate chain allowed when signature_algorithms_cert excludes it")
	}
	if !CertificateChainAllowed([]*x509.Certificate{strong}, handshake, cert, true) {
		t.Fatal("strong certificate chain rejected despite appearing in signature_algorithms_cert")
	}
	if !CertificateChainAllowed([]*x509.Certificate{weak}, handshake, cert, false) {
		t.Fatal("fallback to signature_algorithms should permit the weak certificate scheme")
	}
	if CertificateChainAllowed([]*x509.Certificate{weak, strong}, handshake, cert, true) {
		t.Fatal("chain with one disallowed link was accepted")
	}
}

func TestFilterCertificateChain(t *testing.T) {
	weak := &x509.Certificate{SignatureAlgorithm: x509.SHA1WithRSA}
	strong := &x509.Certificate{SignatureAlgorithm: x509.SHA256WithRSAPSS}
	chain := []*x509.Certificate{weak, strong}

	got := FilterCertificateChain(chain, []SignatureScheme{PKCS1WithSHA1, PSSWithSHA256}, []SignatureScheme{PSSWithSHA256}, true)
	want := []*x509.Certificate{strong}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FilterCertificateChain() = %v, want only the strong certificate", got)
	}

	chains := [][]*x509.Certificate{chain, {strong}}
	gotChains := FilterCertificateChains(chains, []SignatureScheme{PKCS1WithSHA1, PSSWithSHA256}, []SignatureScheme{PSSWithSHA256}, true)
	if len(gotChains) != 1 || len(gotChains[0]) != 1 || gotChains[0][0] != strong {
		t.Fatalf("FilterCertificateChains() = %v, want only the strong chain", gotChains)
	}
}

func TestCertificateChainSchemes(t *testing.T) {
	chain := []SignatureScheme{PKCS1WithSHA1, PSSWithSHA256}
	handshake := []SignatureScheme{PKCS1WithSHA1, PSSWithSHA256}
	cert := []SignatureScheme{PSSWithSHA256}

	if CertificateChainSchemesAllowed(chain, handshake, cert, true) {
		t.Fatal("chain schemes should be rejected when one link is excluded")
	}
	if !CertificateChainSchemesAllowed(chain, handshake, cert, false) {
		t.Fatal("chain schemes should use signature_algorithms when the cert extension is absent")
	}

	got := FilterCertificateChainSchemes(chain, handshake, cert, true)
	if want := []SignatureScheme{PSSWithSHA256}; !reflect.DeepEqual(got, want) {
		t.Fatalf("FilterCertificateChainSchemes() = %v, want %v", got, want)
	}
}
