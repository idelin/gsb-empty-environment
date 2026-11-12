package netsec

import (
	"errors"
	"strings"
	"testing"
)

func TestStructuralEdgeCases(t *testing.T) {
	header := b64url([]byte(`{"alg":"dir","enc":"A256GCM"}`))
	build := func(h string) string {
		return strings.Join([]string{h, "", "c29tZWl2Mgo", "c29tZWN0", "c29tZXRhdw"}, ".")
	}

	cases := map[string]error{
		// Truncated / invalid JSON.
		build(b64url([]byte(`{"alg":"dir"`))):                         ErrMalformedHeader,
		build(b64url([]byte(`{"alg":`))):                              ErrMalformedHeader,
		build(b64url([]byte(``))):                                     ErrMalformedHeader,
		build(b64url([]byte(`{"alg":"dir","enc":"A256GCM"}`)) + "@@"): ErrMalformedToken,
		// Non-string object key.
		build(b64url([]byte(`{"alg":"dir",42:"x"}`))): ErrMalformedHeader,
		// Non-object header value.
		build(b64url([]byte(`123`))):  ErrMalformedHeader,
		build(b64url([]byte(`null`))): ErrMalformedHeader,
		// Bad base64url in mandatory parts.
		strings.Join([]string{header, "", "iv!", "c29tZWN0", "c29tZXRhdw"}, "."):            ErrMalformedToken,
		strings.Join([]string{header, "", "c29tZWl2Mgo", "ct!", "c29tZXRhdw"}, "."):         ErrMalformedToken,
		strings.Join([]string{header, "ek!", "c29tZWl2Mgo", "c29tZWN0", "c29tZXRhdw"}, "."): ErrMalformedToken,
		// enc present but not a string.
		build(b64url([]byte(`{"alg":"dir","enc":256}`))): ErrMalformedHeader,
	}
	for token, want := range cases {
		if err := ValidateCompactJWE(token); !errors.Is(err, want) {
			t.Errorf("token %q: want %v, got %v", token, want, err)
		}
	}
}

func TestEPKStructureEdgeCases(t *testing.T) {
	one := make([]byte, 32)
	one[31] = 1
	coord := b64url(one)

	cases := []map[string]any{
		// epk is a scalar, not an object.
		{"kty-scalar": "EC"},
		nil,
		// Coordinates that are not valid base64url.
		{"kty": "EC", "crv": "P-256", "x": "!!!", "y": coord},
		{"kty": "EC", "crv": "P-256", "x": coord, "y": "???"},
		// Coordinates longer than 32 bytes.
		{"kty": "EC", "crv": "P-256", "x": b64url(make([]byte, 33)), "y": coord},
	}
	for i, epk := range cases {
		header := map[string]any{"alg": "ECDH-ES", "enc": "A256GCM", "epk": epk}
		if err := ValidateCompactJWE(dummyToken(t, header)); !errors.Is(err, ErrEPK) {
			t.Errorf("case %d: want ErrEPK, got %v", i, err)
		}
	}
}

func TestMatrixKeyLengthPolarity(t *testing.T) {
	// Explicit polarity spot checks for the wrap-length rule.
	accept := []struct{ alg, enc string }{
		{"A128KW", "A128GCM"},
		{"A256KW", "A256GCM"},
		{"ECDH-ES+A192KW", "A192GCM"},
		{"RSA-OAEP", "A128CBC-HS256"},
		{"dir", "A256GCM"},
	}
	reject := []struct{ alg, enc string }{
		{"A128KW", "A256GCM"},
		{"A256KW", "A256CBC-HS512"},
		{"A256KW", "A128GCM"},
		{"A192KW", "A128CBC-HS256"},
		{"ECDH-ES+A256KW", "A128GCM"},
		{"ECDH-ES+A128KW", "A192GCM"},
	}
	for _, c := range accept {
		h := map[string]any{"alg": c.alg, "enc": c.enc}
		if isECDHES(c.alg) {
			h["epk"] = validEPKJWK(t)
		}
		if err := ValidateCompactJWE(dummyToken(t, h)); err != nil {
			t.Errorf("%s/%s: want accept, got %v", c.alg, c.enc, err)
		}
	}
	for _, c := range reject {
		h := map[string]any{"alg": c.alg, "enc": c.enc}
		if isECDHES(c.alg) {
			h["epk"] = validEPKJWK(t)
		}
		if err := ValidateCompactJWE(dummyToken(t, h)); !errors.Is(err, ErrAlgorithmMismatch) {
			t.Errorf("%s/%s: want mismatch, got %v", c.alg, c.enc, err)
		}
	}
}
