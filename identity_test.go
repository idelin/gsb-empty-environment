package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"math/big"
	"net/url"
	"strings"
	"testing"
	"time"
)

const (
	testIssuer   = "https://token.actions.githubusercontent.com"
	acmeRepo     = "https://github.com/acme/app"
	acmeSigner   = acmeRepo + "/.github/workflows/release.yml@refs/tags/v1.2.3"
	acmeDigest   = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	acmeRef      = "refs/tags/v1.2.3"
	acmeTrigger  = "push"
	evilRepo     = "https://github.com/evil/app"
	evilSigner   = evilRepo + "/.github/workflows/release.yml@refs/tags/v1.2.3"
	evilDigest   = "sha256:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	branchRef    = "refs/heads/main"
	branchSigner = acmeRepo + "/.github/workflows/release.yml@refs/heads/main"
)

func compliantExtensions() []pkix.Extension {
	return []pkix.Extension{
		derUTF8Extension(OIDIssuer, testIssuer),
		rawExtension(OIDBuildSignerURI, acmeSigner),
		rawExtension(OIDSourceRepositoryURI, acmeRepo),
		rawExtension(OIDSourceRepositoryDigest, acmeDigest),
		rawExtension(OIDSourceRepositoryRef, acmeRef),
		rawExtension(OIDBuildTrigger, acmeTrigger),
	}
}

func compliantPolicy() Policy {
	return Policy{
		Issuer:                 testIssuer,
		BuildSignerURI:         acmeSigner,
		SourceRepositoryURI:    acmeRepo,
		SourceRepositoryDigest: acmeDigest,
		SourceRepositoryRef:    acmeRef,
		BuildTrigger:           acmeTrigger,
		RequireTagRef:          true,
	}
}

func rawExtension(oid asn1.ObjectIdentifier, value string) pkix.Extension {
	return pkix.Extension{Id: oid, Value: []byte(value)}
}

func derUTF8Extension(oid asn1.ObjectIdentifier, value string) pkix.Extension {
	encoded, err := asn1.MarshalWithParams(value, "utf8")
	if err != nil {
		panic(err)
	}
	return pkix.Extension{Id: oid, Value: encoded}
}

func mustCertificate(t *testing.T, extensions []pkix.Extension, uriSANs ...string) *x509.Certificate {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	parsedURIs := make([]*url.URL, 0, len(uriSANs))
	for _, raw := range uriSANs {
		parsed, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse SAN %q: %v", raw, err)
		}
		parsedURIs = append(parsedURIs, parsed)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		t.Fatalf("serial: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "fulcio-test-workload"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		URIs:                  parsedURIs,
		ExtraExtensions:       extensions,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return cert
}

func withoutExtension(extensions []pkix.Extension, oid asn1.ObjectIdentifier) []pkix.Extension {
	out := make([]pkix.Extension, 0, len(extensions))
	for _, extension := range extensions {
		if !extension.Id.Equal(oid) {
			out = append(out, extension)
		}
	}
	return out
}

func TestVerifyIdentityAcceptsCompliantCertificate(t *testing.T) {
	t.Parallel()

	cert := mustCertificate(t, compliantExtensions(), acmeSigner)
	identity, err := VerifyIdentity(cert, compliantPolicy())
	if err != nil {
		t.Fatalf("VerifyIdentity: %v", err)
	}

	want := Identity{
		Issuer:                 testIssuer,
		BuildSignerURI:         acmeSigner,
		SourceRepositoryURI:    acmeRepo,
		SourceRepositoryDigest: acmeDigest,
		SourceRepositoryRef:    acmeRef,
		BuildTrigger:           acmeTrigger,
	}
	if identity != want {
		t.Fatalf("identity = %#v, want %#v", identity, want)
	}
}

func TestVerifyIdentityRejectsSameWorkflowInAnotherRepository(t *testing.T) {
	t.Parallel()

	// The certificate is internally consistent and its SAN names the same
	// workflow file as the trusted certificate; only the repository differs.
	extensions := []pkix.Extension{
		derUTF8Extension(OIDIssuer, testIssuer),
		rawExtension(OIDBuildSignerURI, evilSigner),
		rawExtension(OIDSourceRepositoryURI, evilRepo),
		rawExtension(OIDSourceRepositoryDigest, evilDigest),
		rawExtension(OIDSourceRepositoryRef, acmeRef),
		rawExtension(OIDBuildTrigger, acmeTrigger),
	}
	cert := mustCertificate(t, extensions, evilSigner)

	if _, err := ExtractIdentity(cert); err != nil {
		t.Fatalf("attacker certificate should be structurally valid: %v", err)
	}

	_, err := VerifyIdentity(cert, compliantPolicy())
	if !errors.Is(err, ErrPolicyMismatch) {
		t.Fatalf("VerifyIdentity error = %v, want ErrPolicyMismatch", err)
	}
	if !strings.Contains(err.Error(), string(FieldBuildSignerURI)) {
		t.Fatalf("error %q should identify the mismatched build signer", err)
	}

	// Repository binding also stands on its own, independent of the signer
	// URI check.
	_, err = VerifyIdentity(cert, Policy{SourceRepositoryURI: acmeRepo})
	if !errors.Is(err, ErrPolicyMismatch) {
		t.Fatalf("repository-only policy error = %v, want ErrPolicyMismatch", err)
	}
	if !strings.Contains(err.Error(), string(FieldSourceRepositoryURI)) {
		t.Fatalf("error %q should identify the mismatched repository", err)
	}
}

func TestVerifyIdentityRejectsLegacyIssuerConflict(t *testing.T) {
	t.Parallel()

	extensions := append(compliantExtensions(),
		rawExtension(OIDIssuerLegacy, "https://accounts.google.com"))
	cert := mustCertificate(t, extensions, acmeSigner)

	_, err := ExtractIdentity(cert)
	if !errors.Is(err, ErrLegacyIssuerMismatch) {
		t.Fatalf("ExtractIdentity error = %v, want ErrLegacyIssuerMismatch", err)
	}
}

func TestVerifyIdentityAcceptsConsistentLegacyIssuer(t *testing.T) {
	t.Parallel()

	t.Run("both OIDs agree", func(t *testing.T) {
		extensions := append(compliantExtensions(), rawExtension(OIDIssuerLegacy, testIssuer))
		cert := mustCertificate(t, extensions, acmeSigner)

		identity, err := VerifyIdentity(cert, compliantPolicy())
		if err != nil {
			t.Fatalf("VerifyIdentity: %v", err)
		}
		if identity.Issuer != testIssuer {
			t.Fatalf("issuer = %q, want %q", identity.Issuer, testIssuer)
		}
	})

	t.Run("legacy OID alone is a fallback", func(t *testing.T) {
		extensions := withoutExtension(compliantExtensions(), OIDIssuer)
		extensions = append(extensions, rawExtension(OIDIssuerLegacy, testIssuer))
		cert := mustCertificate(t, extensions, acmeSigner)

		identity, err := VerifyIdentity(cert, compliantPolicy())
		if err != nil {
			t.Fatalf("VerifyIdentity: %v", err)
		}
		if identity.Issuer != testIssuer {
			t.Fatalf("issuer = %q, want %q", identity.Issuer, testIssuer)
		}
	})
}

func TestVerifyIdentityRejectsDuplicateIdentityExtension(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		extra pkix.Extension
	}{
		{name: "issuer", extra: derUTF8Extension(OIDIssuer, testIssuer)},
		{name: "signer", extra: rawExtension(OIDBuildSignerURI, acmeSigner)},
		{name: "repository", extra: rawExtension(OIDSourceRepositoryURI, acmeRepo)},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			extensions := append(compliantExtensions(), test.extra)
			// Go's x509 parser rejects duplicate extensions itself; construct
			// the parsed representation directly to exercise this package's
			// independent duplicate check.
			cert := &x509.Certificate{Extensions: extensions}

			_, err := VerifyIdentity(cert, compliantPolicy())
			if !errors.Is(err, ErrDuplicateExtension) {
				t.Fatalf("VerifyIdentity error = %v, want ErrDuplicateExtension", err)
			}
			if !strings.Contains(err.Error(), test.extra.Id.String()) {
				t.Fatalf("error %q should name duplicate OID %s", err, test.extra.Id)
			}
		})
	}
}

func TestVerifyIdentityRejectsSANMismatch(t *testing.T) {
	t.Parallel()

	// The .1.9 claim names the trusted repository but the SAN names another
	// repository running the same workflow file.
	cert := mustCertificate(t, compliantExtensions(), evilSigner)

	_, err := ExtractIdentity(cert)
	if !errors.Is(err, ErrSANMismatch) {
		t.Fatalf("ExtractIdentity error = %v, want ErrSANMismatch", err)
	}
}

func TestVerifyIdentityRejectsMissingRequiredExtension(t *testing.T) {
	t.Parallel()

	extensions := withoutExtension(compliantExtensions(), OIDSourceRepositoryDigest)
	cert := mustCertificate(t, extensions, acmeSigner)

	_, err := VerifyIdentity(cert, compliantPolicy())
	if !errors.Is(err, ErrMissingExtension) {
		t.Fatalf("VerifyIdentity error = %v, want ErrMissingExtension", err)
	}
	if !strings.Contains(err.Error(), string(FieldSourceRepositoryDigest)) {
		t.Fatalf("error %q should identify the missing digest", err)
	}
}

func TestVerifyIdentityRequiresPresenceWithoutValueConstraint(t *testing.T) {
	t.Parallel()

	extensions := withoutExtension(compliantExtensions(), OIDBuildTrigger)
	cert := mustCertificate(t, extensions, acmeSigner)
	policy := Policy{
		Issuer:  testIssuer,
		Require: []Field{FieldBuildTrigger},
	}

	_, err := VerifyIdentity(cert, policy)
	if !errors.Is(err, ErrMissingExtension) {
		t.Fatalf("VerifyIdentity error = %v, want ErrMissingExtension", err)
	}
}

func TestVerifyIdentityRejectsBranchWhenTagRequired(t *testing.T) {
	t.Parallel()

	extensions := []pkix.Extension{
		derUTF8Extension(OIDIssuer, testIssuer),
		rawExtension(OIDBuildSignerURI, branchSigner),
		rawExtension(OIDSourceRepositoryURI, acmeRepo),
		rawExtension(OIDSourceRepositoryDigest, acmeDigest),
		rawExtension(OIDSourceRepositoryRef, branchRef),
		rawExtension(OIDBuildTrigger, acmeTrigger),
	}
	cert := mustCertificate(t, extensions, branchSigner)
	policy := Policy{
		Issuer:                 testIssuer,
		BuildSignerURI:         branchSigner,
		SourceRepositoryURI:    acmeRepo,
		SourceRepositoryDigest: acmeDigest,
		BuildTrigger:           acmeTrigger,
		RequireTagRef:          true,
	}

	_, err := VerifyIdentity(cert, policy)
	if !errors.Is(err, ErrRefNotTag) {
		t.Fatalf("VerifyIdentity error = %v, want ErrRefNotTag", err)
	}
}

func TestExtractIdentityRejectsMalformedIssuerEncoding(t *testing.T) {
	t.Parallel()

	rawIssuer := derUTF8Extension(OIDIssuer, testIssuer).Value
	tests := []struct {
		name  string
		value []byte
	}{
		{name: "raw bytes", value: []byte(testIssuer)},
		{
			name: "printable string",
			value: func() []byte {
				encoded, err := asn1.MarshalWithParams(testIssuer, "printable")
				if err != nil {
					t.Fatalf("encode printable string: %v", err)
				}
				return encoded
			}(),
		},
		{name: "trailing data", value: append(append([]byte(nil), rawIssuer...), 0x00)},
		{
			name: "empty UTF8String",
			value: func() []byte {
				encoded, err := asn1.MarshalWithParams("", "utf8")
				if err != nil {
					t.Fatalf("encode empty string: %v", err)
				}
				return encoded
			}(),
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			extensions := compliantExtensions()
			for i := range extensions {
				if extensions[i].Id.Equal(OIDIssuer) {
					extensions[i] = pkix.Extension{Id: OIDIssuer, Value: test.value}
				}
			}
			cert := mustCertificate(t, extensions, acmeSigner)

			_, err := ExtractIdentity(cert)
			if !errors.Is(err, ErrMalformedExtension) {
				t.Fatalf("ExtractIdentity error = %v, want ErrMalformedExtension", err)
			}
		})
	}
}

func TestExtractIdentityRejectsEmptyRawClaim(t *testing.T) {
	t.Parallel()

	extensions := compliantExtensions()
	for i := range extensions {
		if extensions[i].Id.Equal(OIDBuildTrigger) {
			extensions[i] = pkix.Extension{Id: OIDBuildTrigger, Value: nil}
		}
	}
	cert := mustCertificate(t, extensions, acmeSigner)

	_, err := ExtractIdentity(cert)
	if !errors.Is(err, ErrMalformedExtension) {
		t.Fatalf("ExtractIdentity error = %v, want ErrMalformedExtension", err)
	}
}

func TestExtractIdentityRejectsAnyMismatchedURISAN(t *testing.T) {
	t.Parallel()

	// One SAN agrees with .1.9 and another does not. Fulcio issues a single
	// signer SAN, so ambiguous SAN sets are rejected rather than partially
	// trusted.
	cert := mustCertificate(t, compliantExtensions(), acmeSigner, evilSigner)

	_, err := ExtractIdentity(cert)
	if !errors.Is(err, ErrSANMismatch) {
		t.Fatalf("ExtractIdentity error = %v, want ErrSANMismatch", err)
	}
}

func TestExtractIdentityRejectsMissingURISAN(t *testing.T) {
	t.Parallel()

	cert := mustCertificate(t, compliantExtensions())

	_, err := ExtractIdentity(cert)
	if !errors.Is(err, ErrSANMismatch) {
		t.Fatalf("ExtractIdentity error = %v, want ErrSANMismatch", err)
	}
}

func TestVerifyIdentityRejectsEmptyOrInvalidPolicy(t *testing.T) {
	t.Parallel()

	cert := mustCertificate(t, compliantExtensions(), acmeSigner)

	if _, err := VerifyIdentity(cert, Policy{}); !errors.Is(err, ErrEmptyPolicy) {
		t.Fatalf("empty policy error = %v, want ErrEmptyPolicy", err)
	}
	if _, err := VerifyIdentity(cert, Policy{Require: []Field{"not-a-field"}}); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("invalid field error = %v, want ErrInvalidPolicy", err)
	}
	if _, err := VerifyIdentity(cert, Policy{
		Issuer:              testIssuer,
		SourceRepositoryRef: branchRef,
		RequireTagRef:       true,
	}); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("branch expectation with tag policy error = %v, want ErrInvalidPolicy", err)
	}
	if _, err := VerifyIdentity(nil, compliantPolicy()); !errors.Is(err, ErrNilCertificate) {
		t.Fatalf("nil certificate error = %v, want ErrNilCertificate", err)
	}
}

func TestIsTagRef(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ref  string
		want bool
	}{
		{"refs/tags/v1.2.3", true},
		{"refs/tags/release/candidate", true},
		{"refs/heads/main", false},
		{"refs/tags/", false},
		{"", false},
		{"v1.2.3", false},
	}
	for _, test := range tests {
		if got := IsTagRef(test.ref); got != test.want {
			t.Errorf("IsTagRef(%q) = %t, want %t", test.ref, got, test.want)
		}
	}
}
