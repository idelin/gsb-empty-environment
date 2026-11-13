package netsec_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"example.org/netsec/q0666"
)

type certFixture struct {
	der         []byte
	publicKey   ed25519.PublicKey
	fingerprint string
}

func newCertFixture(t *testing.T, commonName string) certFixture {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	fingerprint, err := netsec.CertificateFingerprint(der)
	if err != nil {
		t.Fatalf("fingerprint certificate: %v", err)
	}
	return certFixture{der: der, publicKey: publicKey, fingerprint: fingerprint}
}

func makeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + "."
}

func boundClaims(fingerprint string) map[string]any {
	return map[string]any{
		"sub": "client",
		"cnf": map[string]any{"x5t#S256": fingerprint},
	}
}

func requireError(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("got error %v, want errors.Is(..., %v)", err, target)
	}
}

func TestCertificateFingerprint(t *testing.T) {
	fixture := newCertFixture(t, "fingerprint")
	sum := sha256.Sum256(fixture.der)
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if fixture.fingerprint != want {
		t.Fatalf("fingerprint = %q, want %q", fixture.fingerprint, want)
	}
	if strings.ContainsAny(fixture.fingerprint, "=/+") {
		t.Fatalf("fingerprint is not unpadded base64url: %q", fixture.fingerprint)
	}
}

func TestValidateAccessToken(t *testing.T) {
	trusted := newCertFixture(t, "trusted-client")
	attacker := newCertFixture(t, "attacker")

	t.Run("valid JWT on matching connection", func(t *testing.T) {
		jwt := makeJWT(t, boundClaims(trusted.fingerprint))
		err := netsec.ValidateAccessTokenJWT(jwt, netsec.NewConnection(trusted.der), netsec.ClientRegistration{})
		if err != nil {
			t.Fatalf("valid token rejected: %v", err)
		}
	})

	t.Run("valid introspection response", func(t *testing.T) {
		response := fmt.Sprintf(`{"active":true,"sub":"client","cnf":{"x5t#S256":%q}}`, trusted.fingerprint)
		err := netsec.ValidateAccessTokenIntrospection([]byte(response), netsec.NewConnection(trusted.der), netsec.ClientRegistration{})
		if err != nil {
			t.Fatalf("valid introspection response rejected: %v", err)
		}
	})

	t.Run("certificate swap is rejected", func(t *testing.T) {
		claims := boundClaims(trusted.fingerprint)
		err := netsec.ValidateAccessToken(claims, netsec.NewConnection(attacker.der), netsec.ClientRegistration{})
		requireError(t, err, netsec.ErrFingerprintMismatch)
	})

	t.Run("cnf without client certificate is rejected", func(t *testing.T) {
		err := netsec.ValidateAccessToken(boundClaims(trusted.fingerprint), netsec.Connection{}, netsec.ClientRegistration{})
		requireError(t, err, netsec.ErrCnfWithoutClientCertificate)
	})

	t.Run("client requiring binding needs cnf", func(t *testing.T) {
		err := netsec.ValidateAccessToken(
			map[string]any{"sub": "client"},
			netsec.NewConnection(trusted.der),
			netsec.ClientRegistration{RequireCertificateBinding: true},
		)
		requireError(t, err, netsec.ErrCertificateBindingRequired)
	})

	t.Run("unbound client may receive unbound token", func(t *testing.T) {
		err := netsec.ValidateAccessToken(map[string]any{"sub": "client"}, netsec.Connection{}, netsec.ClientRegistration{})
		if err != nil {
			t.Fatalf("unbound token rejected for unbound client: %v", err)
		}
	})

	t.Run("cnf must be an object", func(t *testing.T) {
		err := netsec.ValidateAccessToken(map[string]any{"cnf": "not-an-object"}, netsec.NewConnection(trusted.der), netsec.ClientRegistration{})
		requireError(t, err, netsec.ErrMalformedCnf)
	})

	t.Run("empty binding member is rejected", func(t *testing.T) {
		claims := map[string]any{"cnf": map[string]any{"x5t#S256": ""}}
		err := netsec.ValidateAccessToken(claims, netsec.NewConnection(trusted.der), netsec.ClientRegistration{})
		requireError(t, err, netsec.ErrInvalidFingerprint)
	})
}

func TestValidateAccessTokenRequiresBothJKTAndX5T(t *testing.T) {
	trusted := newCertFixture(t, "trusted-client")
	other := newCertFixture(t, "other-client")
	jkt, err := netsec.JWKThumbprint(trusted.publicKey)
	if err != nil {
		t.Fatalf("compute jkt: %v", err)
	}

	both := func(x5t, thumbprint string) map[string]any {
		return map[string]any{
			"cnf": map[string]any{
				"x5t#S256": x5t,
				"jkt":      thumbprint,
			},
		}
	}

	t.Run("both match", func(t *testing.T) {
		err := netsec.ValidateAccessToken(both(trusted.fingerprint, jkt), netsec.NewConnection(trusted.der), netsec.ClientRegistration{RequireCertificateBinding: true})
		if err != nil {
			t.Fatalf("both bindings should match: %v", err)
		}
	})

	t.Run("x5t mismatch despite valid jkt", func(t *testing.T) {
		err := netsec.ValidateAccessToken(both(other.fingerprint, jkt), netsec.NewConnection(trusted.der), netsec.ClientRegistration{})
		requireError(t, err, netsec.ErrFingerprintMismatch)
	})

	t.Run("jkt mismatch despite valid x5t", func(t *testing.T) {
		otherJKT, err := netsec.JWKThumbprint(other.publicKey)
		if err != nil {
			t.Fatalf("compute other jkt: %v", err)
		}
		err = netsec.ValidateAccessToken(both(trusted.fingerprint, otherJKT), netsec.NewConnection(trusted.der), netsec.ClientRegistration{})
		requireError(t, err, netsec.ErrJKTMismatch)
	})

	t.Run("explicit proof key thumbprint is honored", func(t *testing.T) {
		conn := netsec.Connection{ClientCertDER: trusted.der, JWKThumbprint: jkt}
		err := netsec.ValidateAccessToken(both(trusted.fingerprint, jkt), conn, netsec.ClientRegistration{})
		if err != nil {
			t.Fatalf("explicit matching jkt rejected: %v", err)
		}
	})

	t.Run("jkt-only token does not satisfy certificate-binding policy", func(t *testing.T) {
		claims := map[string]any{"cnf": map[string]any{"jkt": jkt}}
		err := netsec.ValidateAccessToken(claims, netsec.NewConnection(trusted.der), netsec.ClientRegistration{RequireCertificateBinding: true})
		requireError(t, err, netsec.ErrCertificateBindingRequired)
		if err := netsec.RequireCertificateBinding(claims); !errors.Is(err, netsec.ErrCertificateBindingRequired) {
			t.Fatalf("RequireCertificateBinding accepted jkt-only cnf: %v", err)
		}
	})
}

func TestValidateRefreshBinding(t *testing.T) {
	oldCert := newCertFixture(t, "old-certificate")
	newCert := newCertFixture(t, "new-certificate")
	oldRecord := netsec.RefreshTokenRecord{CertificateFingerprint: oldCert.fingerprint}

	t.Run("same certificate keeps binding", func(t *testing.T) {
		cnf, err := netsec.ValidateRefreshBinding(oldRecord, netsec.NewConnection(oldCert.der), nil)
		if err != nil {
			t.Fatalf("same-certificate refresh rejected: %v", err)
		}
		if cnf.X5TS256 != oldCert.fingerprint {
			t.Fatalf("new cnf = %q, want %q", cnf.X5TS256, oldCert.fingerprint)
		}
	})

	t.Run("bound refresh without certificate is rejected", func(t *testing.T) {
		_, err := netsec.ValidateRefreshBinding(oldRecord, netsec.Connection{}, nil)
		requireError(t, err, netsec.ErrNoClientCertificate)
	})

	t.Run("unbound refresh stays unbound without certificate", func(t *testing.T) {
		cnf, err := netsec.ValidateRefreshBinding(netsec.RefreshTokenRecord{}, netsec.Connection{}, nil)
		if err != nil {
			t.Fatalf("unbound refresh rejected: %v", err)
		}
		if cnf.Present() {
			t.Fatalf("unbound refresh produced binding %+v", cnf)
		}
	})

	t.Run("unbound refresh becomes bound with certificate", func(t *testing.T) {
		cnf, err := netsec.ValidateRefreshBinding(netsec.RefreshTokenRecord{}, netsec.NewConnection(newCert.der), nil)
		if err != nil {
			t.Fatalf("unbound refresh rejected: %v", err)
		}
		if cnf.X5TS256 != newCert.fingerprint {
			t.Fatalf("new cnf = %q, want %q", cnf.X5TS256, newCert.fingerprint)
		}
	})

	t.Run("certificate change without rotation grant is rejected", func(t *testing.T) {
		_, err := netsec.ValidateRefreshBinding(oldRecord, netsec.NewConnection(newCert.der), nil)
		requireError(t, err, netsec.ErrRotationNotGranted)
	})

	t.Run("certificate change with reversed grant is rejected", func(t *testing.T) {
		grants := netsec.RotationTable{}
		grants.Add(newCert.fingerprint, oldCert.fingerprint)
		_, err := netsec.ValidateRefreshBinding(oldRecord, netsec.NewConnection(newCert.der), grants)
		requireError(t, err, netsec.ErrRotationNotGranted)
	})

	t.Run("certificate change with exact grant is allowed", func(t *testing.T) {
		grants := netsec.RotationTable{}
		grants.Add(oldCert.fingerprint, newCert.fingerprint)
		cnf, err := netsec.ValidateRefreshBinding(oldRecord, netsec.NewConnection(newCert.der), grants)
		if err != nil {
			t.Fatalf("granted rotation rejected: %v", err)
		}
		if cnf.X5TS256 != newCert.fingerprint {
			t.Fatalf("new cnf = %q, want new certificate %q", cnf.X5TS256, newCert.fingerprint)
		}
	})

	t.Run("malformed recorded fingerprint is rejected", func(t *testing.T) {
		record := netsec.RefreshTokenRecord{CertificateFingerprint: oldCert.fingerprint + "="}
		_, err := netsec.ValidateRefreshBinding(record, netsec.NewConnection(oldCert.der), nil)
		requireError(t, err, netsec.ErrInvalidFingerprint)
	})
}

func TestAuthenticateTokenEndpointCertificate(t *testing.T) {
	client := newCertFixture(t, "mtls-client")
	meta := netsec.OAuthMetadata{
		TokenEndpoint: "https://as.example.com/token",
		MTLEndpointAliases: map[string]string{
			"token_endpoint":         "https://mtls.example.com/token",
			"introspection_endpoint": "https://mtls.example.com/introspect",
		},
	}

	t.Run("mTLS token alias accepts certificate", func(t *testing.T) {
		auth, err := netsec.AuthenticateTokenEndpointCertificate(meta, "https://mtls.example.com/token?grant_type=refresh_token", netsec.NewConnection(client.der))
		if err != nil {
			t.Fatalf("mTLS alias rejected: %v", err)
		}
		if !auth.Authenticated || auth.Fingerprint != client.fingerprint {
			t.Fatalf("unexpected auth result: %+v", auth)
		}
	})

	t.Run("regular token endpoint must not authenticate certificate", func(t *testing.T) {
		_, err := netsec.AuthenticateTokenEndpointCertificate(meta, "https://as.example.com/token", netsec.NewConnection(client.der))
		requireError(t, err, netsec.ErrCertificateAuthNotAllowed)
	})

	t.Run("other mTLS aliases do not accept token endpoint certificate auth", func(t *testing.T) {
		_, err := netsec.AuthenticateTokenEndpointCertificate(meta, "https://mtls.example.com/introspect", netsec.NewConnection(client.der))
		requireError(t, err, netsec.ErrCertificateAuthNotAllowed)
	})

	t.Run("wrong host or path is rejected", func(t *testing.T) {
		_, err := netsec.AuthenticateTokenEndpointCertificate(meta, "https://mtls.example.com/other", netsec.NewConnection(client.der))
		requireError(t, err, netsec.ErrCertificateAuthNotAllowed)
	})

	t.Run("missing certificate is reported", func(t *testing.T) {
		_, err := netsec.AuthenticateTokenEndpointCertificate(meta, "https://mtls.example.com/token", netsec.Connection{})
		requireError(t, err, netsec.ErrNoClientCertificate)
	})

	t.Run("metadata without token alias never accepts certificate auth", func(t *testing.T) {
		noAlias := netsec.OAuthMetadata{TokenEndpoint: "https://as.example.com/token"}
		_, err := netsec.AuthenticateTokenEndpointCertificate(noAlias, "https://as.example.com/token", netsec.NewConnection(client.der))
		requireError(t, err, netsec.ErrCertificateAuthNotAllowed)
	})
}

func TestFingerprintFormatValidation(t *testing.T) {
	fixture := newCertFixture(t, "format")
	valid := fixture.fingerprint

	tests := []struct {
		name  string
		value string
	}{
		{name: "padded", value: valid + "="},
		{name: "padding in middle", value: valid[:10] + "=" + valid[11:]},
		{name: "plus character", value: "+" + valid[1:]},
		{name: "slash character", value: "/" + valid[1:]},
		{name: "whitespace", value: valid[:20] + " " + valid[21:]},
		{name: "too short digest", value: base64.RawURLEncoding.EncodeToString([]byte("short"))},
		{name: "empty", value: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := netsec.ValidateX5TS256(test.value); !errors.Is(err, netsec.ErrInvalidFingerprint) {
				t.Fatalf("ValidateX5TS256(%q) = %v, want ErrInvalidFingerprint", test.value, err)
			}
		})
	}

	t.Run("valid value passes", func(t *testing.T) {
		if err := netsec.ValidateX5TS256(valid); err != nil {
			t.Fatalf("valid fingerprint rejected: %v", err)
		}
	})

	t.Run("padded token claim is rejected", func(t *testing.T) {
		err := netsec.ValidateAccessToken(boundClaims(valid+"="), netsec.NewConnection(fixture.der), netsec.ClientRegistration{})
		requireError(t, err, netsec.ErrInvalidFingerprint)
	})

	t.Run("standard base64 characters in token claim are rejected", func(t *testing.T) {
		value := "+" + valid[1:]
		err := netsec.ValidateAccessToken(boundClaims(value), netsec.NewConnection(fixture.der), netsec.ClientRegistration{})
		requireError(t, err, netsec.ErrInvalidFingerprint)
	})

	t.Run("standard padded base64 is rejected", func(t *testing.T) {
		value := base64.URLEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
		if err := netsec.ValidateX5TS256(value); !errors.Is(err, netsec.ErrInvalidFingerprint) {
			t.Fatalf("padded base64 accepted: %v", err)
		}
	})
}

func TestSameFingerprint(t *testing.T) {
	left := newCertFixture(t, "left")
	right := newCertFixture(t, "right")

	same, err := netsec.SameFingerprint(left.fingerprint, left.fingerprint)
	if err != nil || !same {
		t.Fatalf("same fingerprints: same=%v err=%v", same, err)
	}
	same, err = netsec.SameFingerprint(left.fingerprint, right.fingerprint)
	if err != nil {
		t.Fatalf("different fingerprints: %v", err)
	}
	if same {
		t.Fatal("different fingerprints compared equal")
	}
}

func TestJWKThumbprintSupportedKeyTypes(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ECDSA key: %v", err)
	}
	ed25519Public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate Ed25519 key: %v", err)
	}

	tests := []struct {
		name string
		key  crypto.PublicKey
	}{
		{name: "RSA", key: &rsaKey.PublicKey},
		{name: "ECDSA", key: &ecdsaKey.PublicKey},
		{name: "Ed25519", key: ed25519Public},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			thumbprint, err := netsec.JWKThumbprint(test.key)
			if err != nil {
				t.Fatalf("JWKThumbprint: %v", err)
			}
			if err := netsec.ValidateX5TS256(thumbprint); err != nil {
				t.Fatalf("thumbprint is not a valid binding value: %v", err)
			}
		})
	}

	if _, err := netsec.JWKThumbprint(struct{}{}); !errors.Is(err, netsec.ErrJKTUnavailable) {
		t.Fatalf("unsupported key type: got %v, want ErrJKTUnavailable", err)
	}
}

func TestParseJWTClaimsRejectsMalformedInput(t *testing.T) {
	tests := []string{
		"not-a-jwt",
		"one.two",
		"header..signature",
		"header.bm90LWpzb24.signature",
	}
	for _, input := range tests {
		if _, err := netsec.ParseJWTClaims(input); err == nil {
			t.Fatalf("ParseJWTClaims(%q) unexpectedly succeeded", input)
		}
	}
}
