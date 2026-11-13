package netsec

import (
	"errors"
	"reflect"
	"testing"
)

func TestApplicableCertificateSchemesUsesCertExtensionWhenPresent(t *testing.T) {
	// rsa_pkcs1_sha1 is deliberately weak and appears only in the
	// signature_algorithms list. It must not leak into certificate selection
	// when signature_algorithms_cert is present.
	weak := PKCS1WithSHA1
	strong := PSSWithSHA256
	handshake := []SignatureScheme{weak, strong}
	cert := []SignatureScheme{strong}

	policy := NewCertificateSignatureAlgorithms(handshake, cert, true)

	got := policy.ApplicableCertificateSchemes()
	want := []SignatureScheme{strong}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ApplicableCertificateSchemes() = %v, want %v", got, want)
	}
	if policy.AcceptsCertificateScheme(weak) {
		t.Fatal("weak scheme from signature_algorithms accepted when signature_algorithms_cert is present")
	}
	if !policy.AcceptsCertificateScheme(strong) {
		t.Fatal("scheme present in signature_algorithms_cert was rejected")
	}
}

func TestFallbackToSignatureAlgorithmsWhenCertExtensionAbsent(t *testing.T) {
	weak := PKCS1WithSHA1
	strong := PSSWithSHA256
	handshake := []SignatureScheme{weak, strong}

	policy := NewCertificateSignatureAlgorithms(handshake, []SignatureScheme{strong}, false)

	got := policy.ApplicableCertificateSchemes()
	want := []SignatureScheme{weak, strong}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback list = %v, want %v", got, want)
	}
	if !policy.AcceptsCertificateScheme(weak) {
		t.Fatal("scheme in signature_algorithms rejected when signature_algorithms_cert is absent")
	}
}

func TestCertExtensionPresenceIsExplicit(t *testing.T) {
	strong := PSSWithSHA256
	handshake := []SignatureScheme{strong}

	// nil certAlgs with an explicit true means the extension was present but
	// empty. It must not silently fall back to signature_algorithms.
	presentEmpty := NewCertificateSignatureAlgorithms(handshake, nil, true)
	if got := presentEmpty.ApplicableCertificateSchemes(); len(got) != 0 {
		t.Fatalf("present empty extension produced %v, want empty list", got)
	}
	if presentEmpty.AcceptsCertificateScheme(strong) {
		t.Fatal("scheme accepted from signature_algorithms despite present empty signature_algorithms_cert")
	}

	// A nil list with no explicit presence flag is the absent-extension case.
	absent := NewCertificateSignatureAlgorithms(handshake, nil)
	if got := absent.ApplicableCertificateSchemes(); !reflect.DeepEqual(got, handshake) {
		t.Fatalf("absent extension fallback = %v, want %v", got, handshake)
	}
}

func TestSchemeOutsideBothListsIsRejected(t *testing.T) {
	handshake := []SignatureScheme{PSSWithSHA256}
	cert := []SignatureScheme{PSSWithSHA384}

	if AcceptsCertificateSignatureScheme(Ed25519, handshake, cert, true) {
		t.Fatal("scheme in neither extension list was accepted")
	}
	if _, ok := SelectCertificateSignatureScheme([]SignatureScheme{Ed25519}, handshake, cert, true); ok {
		t.Fatal("selected scheme that occurs in neither list")
	}
}

func TestSchemeOnlyInHandshakeListRejectedForCertificate(t *testing.T) {
	weak := ECDSAWithSHA1
	handshake := []SignatureScheme{weak, PSSWithSHA256}
	cert := []SignatureScheme{PSSWithSHA256}

	selected, ok := SelectCertificateSignatureScheme([]SignatureScheme{weak}, handshake, cert, true)
	if ok {
		t.Fatalf("selected weak handshake-only scheme %v for certificate", selected)
	}

	filtered := FilterCertificateSignatureSchemes([]SignatureScheme{weak, PSSWithSHA256}, handshake, cert, true)
	if want := []SignatureScheme{PSSWithSHA256}; !reflect.DeepEqual(filtered, want) {
		t.Fatalf("filtered schemes = %v, want %v", filtered, want)
	}
}

func TestSelectionUsesPeerPreferenceAndLocalSupport(t *testing.T) {
	handshake := []SignatureScheme{ECDSAWithP256AndSHA256, PSSWithSHA256, Ed25519}
	cert := []SignatureScheme{Ed25519, PSSWithSHA256, ECDSAWithP256AndSHA256}

	got, ok := SelectCertificateSignatureScheme(
		[]SignatureScheme{ECDSAWithP256AndSHA256, PSSWithSHA256, Ed25519},
		handshake,
		cert,
		true,
	)
	if !ok {
		t.Fatal("expected a mutually supported scheme")
	}
	if got != Ed25519 {
		t.Fatalf("selected %v, want first peer-preferred local scheme Ed25519", got)
	}

	if _, ok := SelectCertificateSignatureScheme([]SignatureScheme{ECDSAWithP521AndSHA512}, handshake, cert, true); ok {
		t.Fatal("selected scheme that is not locally supported")
	}
}

func TestCertificateScopeClassification(t *testing.T) {
	if !PKCS1WithSHA1.IsCertificateSignatureScheme() {
		t.Fatal("rsa_pkcs1_sha1 should be usable for certificate signatures")
	}
	if PKCS1WithSHA1.IsHandshakeSignatureScheme() {
		t.Fatal("rsa_pkcs1_sha1 must not be usable for TLS 1.3 handshake signatures")
	}
	if !PSSWithSHA256.IsCertificateSignatureScheme() || !PSSWithSHA256.IsHandshakeSignatureScheme() {
		t.Fatal("rsa_pss_rsae_sha256 should be usable in both scopes")
	}
	if !CertificateScopeSchemes([]SignatureScheme{PKCS1WithSHA1})[0].IsCertificateSignatureScheme() {
		t.Fatal("certificate scope filter removed a certificate scheme")
	}
}

func TestSignatureSchemeListEncoding(t *testing.T) {
	schemes := []SignatureScheme{PSSWithSHA256, ECDSAWithP256AndSHA256, Ed25519}
	encoded := MarshalSignatureSchemeList(schemes)
	decoded, err := ParseSignatureSchemeList(encoded)
	if err != nil {
		t.Fatalf("ParseSignatureSchemeList() error = %v", err)
	}
	if !reflect.DeepEqual(decoded, schemes) {
		t.Fatalf("round trip = %v, want %v", decoded, schemes)
	}
	if _, err := ParseSignatureSchemeList([]byte{0}); !errors.Is(err, ErrMalformedSignatureSchemeList) {
		t.Fatalf("odd-length list error = %v, want ErrMalformedSignatureSchemeList", err)
	}
}

func TestNegotiateReturnsErrorWhenNoSchemeMatches(t *testing.T) {
	_, err := NegotiateCertificateSignatureScheme(
		[]SignatureScheme{Ed25519},
		[]SignatureScheme{Ed25519},
		[]SignatureScheme{PSSWithSHA256},
		true,
	)
	if !errors.Is(err, ErrNoSuitableCertificateSignatureScheme) {
		t.Fatalf("NegotiateCertificateSignatureScheme() error = %v, want ErrNoSuitableCertificateSignatureScheme", err)
	}
}
