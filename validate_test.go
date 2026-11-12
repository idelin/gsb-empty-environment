package netsec

import (
	"crypto/ecdh"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"
)

// RFC 7518 CEK / wrapped-key sizes, declared independently of the production
// maps so the matrix encodes the policy rather than echoing the implementation.
var (
	testCEKSize = map[string]int{
		"A128GCM": 16, "A192GCM": 24, "A256GCM": 32,
		"A128CBC-HS256": 32, "A192CBC-HS384": 48, "A256CBC-HS512": 64,
	}
	testWrapSize = map[string]int{
		"A128KW": 16, "A192KW": 24, "A256KW": 32,
		"ECDH-ES+A128KW": 16, "ECDH-ES+A192KW": 24, "ECDH-ES+A256KW": 32,
	}
	matrixAlgs = []string{
		"dir", "ECDH-ES",
		"ECDH-ES+A128KW", "ECDH-ES+A192KW", "ECDH-ES+A256KW",
		"A128KW", "A192KW", "A256KW", "RSA-OAEP", "RSA-OAEP-256",
	}
	matrixEncs = []string{
		"A128GCM", "A192GCM", "A256GCM",
		"A128CBC-HS256", "A192CBC-HS384", "A256CBC-HS512",
	}
)

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func validEPKJWK(t *testing.T) map[string]any {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate P-256 key: %v", err)
	}
	raw := key.PublicKey().Bytes() // 0x04 || X || Y, 65 bytes
	return map[string]any{
		"kty": "EC",
		"crv": "P-256",
		"x":   b64url(raw[1:33]),
		"y":   b64url(raw[33:65]),
	}
}

// dummyToken builds a structurally complete compact token around a header.
func dummyToken(t *testing.T, header map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	encryptedKey := "QUJD" // "ABC"
	if header["alg"] == "dir" || header["alg"] == "ECDH-ES" {
		encryptedKey = ""
	}
	return strings.Join([]string{
		b64url(raw), encryptedKey, "c29tZWl2Mgo", "c29tZWN0", "c29tZXRhdw",
	}, ".")
}

func tokenWithRawHeader(t *testing.T, rawHeader, encryptedKey string) string {
	t.Helper()
	return strings.Join([]string{
		b64url([]byte(rawHeader)), encryptedKey, "c29tZWl2Mgo", "c29tZWN0", "c29tZXRhdw",
	}, ".")
}

// TestAlgorithmMatrix exercises every allowed alg x enc combination and
// independently computes the expected accept / mismatch outcome.
func TestAlgorithmMatrix(t *testing.T) {
	for _, alg := range matrixAlgs {
		for _, enc := range matrixEncs {
			header := map[string]any{"alg": alg, "enc": enc}
			if isECDHES(alg) {
				header["epk"] = validEPKJWK(t)
			}
			err := ValidateCompactJWE(dummyToken(t, header))

			want, isWrap := testWrapSize[alg]
			if isWrap && want != testCEKSize[enc] {
				if !errors.Is(err, ErrAlgorithmMismatch) {
					t.Errorf("alg=%s enc=%s: want ErrAlgorithmMismatch, got %v",
						alg, enc, err)
				}
				continue
			}
			if err != nil {
				t.Errorf("alg=%s enc=%s: want acceptance, got %v", alg, enc, err)
			}
		}
	}
}

func TestDisallowedAlgAndEnc(t *testing.T) {
	badAlgs := []string{
		"none", "RSA1_5", "PBES2-HS256+A128KW", "A256GCMKW",
		"ECDH-ES+A512KW", "dir ",
	}
	for _, alg := range badAlgs {
		header := map[string]any{"alg": alg, "enc": "A256GCM"}
		if err := ValidateCompactJWE(dummyToken(t, header)); !errors.Is(err, ErrUnsupportedAlg) {
			t.Errorf("alg=%q: want ErrUnsupportedAlg, got %v", alg, err)
		}
	}
	if err := ValidateCompactJWE(dummyToken(t,
		map[string]any{"alg": "", "enc": "A256GCM"})); !errors.Is(err, ErrMalformedHeader) {
		t.Errorf("empty alg: want ErrMalformedHeader, got %v", err)
	}

	badEncs := []string{"none", "A128CBC+HS256", "A512GCM", "XCBC"}
	for _, enc := range badEncs {
		header := map[string]any{"alg": "dir", "enc": enc}
		if err := ValidateCompactJWE(dummyToken(t, header)); !errors.Is(err, ErrUnsupportedEnc) {
			t.Errorf("enc=%q: want ErrUnsupportedEnc, got %v", enc, err)
		}
	}
	if err := ValidateCompactJWE(dummyToken(t,
		map[string]any{"alg": "dir", "enc": ""})); !errors.Is(err, ErrMalformedHeader) {
		t.Errorf("empty enc: want ErrMalformedHeader, got %v", err)
	}
}

func TestOffCurveAndMalformedEPK(t *testing.T) {
	p256P := new(big.Int).Sub(elliptic.P256().Params().P, big.NewInt(1))
	coord32 := func(i *big.Int) string {
		b := make([]byte, 32)
		i.FillBytes(b)
		return b64url(b)
	}
	one := make([]byte, 32)
	one[31] = 1

	offCurve := []map[string]any{
		// (1, 1) is not on P-256.
		{"kty": "EC", "crv": "P-256", "x": b64url(one), "y": b64url(one)},
		// x = p-1, y = 1 cannot satisfy the curve equation.
		{"kty": "EC", "crv": "P-256", "x": coord32(p256P), "y": b64url(one)},
		// Point at infinity encoded as (0, 0).
		{"kty": "EC", "crv": "P-256", "x": b64url(make([]byte, 32)), "y": b64url(make([]byte, 32))},
		// Wrong curve declaration.
		{"kty": "EC", "crv": "P-384", "x": b64url(one), "y": b64url(one)},
		// Wrong key type.
		{"kty": "RSA", "crv": "P-256", "x": b64url(one), "y": b64url(one)},
		// Truncated coordinate (31 bytes).
		{"kty": "EC", "crv": "P-256", "x": b64url(one[1:]), "y": b64url(one)},
		// Missing y.
		{"kty": "EC", "crv": "P-256", "x": b64url(one)},
		// Private component smuggled in.
		{"kty": "EC", "crv": "P-256", "x": b64url(one), "y": b64url(one), "d": b64url(one)},
		// Padded base64 is not valid base64url.
		{"kty": "EC", "crv": "P-256", "x": base64.StdEncoding.EncodeToString(one), "y": b64url(one)},
	}
	for i, epk := range offCurve {
		header := map[string]any{"alg": "ECDH-ES", "enc": "A256GCM", "epk": epk}
		if err := ValidateCompactJWE(dummyToken(t, header)); !errors.Is(err, ErrEPK) {
			t.Errorf("case %d: want ErrEPK, got %v", i, err)
		}
	}

	// ECDH-ES without an epk, and an epk attached to a non-ECDH alg.
	missing := map[string]any{"alg": "ECDH-ES+A256KW", "enc": "A256GCM"}
	if err := ValidateCompactJWE(dummyToken(t, missing)); !errors.Is(err, ErrEPK) {
		t.Errorf("missing epk: want ErrEPK, got %v", err)
	}
	stray := map[string]any{"alg": "dir", "enc": "A256GCM", "epk": validEPKJWK(t)}
	if err := ValidateCompactJWE(dummyToken(t, stray)); !errors.Is(err, ErrEPK) {
		t.Errorf("stray epk: want ErrEPK, got %v", err)
	}

	// A genuinely generated point must be accepted.
	good := map[string]any{"alg": "ECDH-ES", "enc": "A128GCM", "epk": validEPKJWK(t)}
	if err := ValidateCompactJWE(dummyToken(t, good)); err != nil {
		t.Errorf("valid epk rejected: %v", err)
	}
}

func TestZipAlwaysRejected(t *testing.T) {
	cases := []string{
		`{"alg":"dir","enc":"A256GCM","zip":"DEF"}`,
		`{"zip":"DEF","alg":"dir","enc":"A256GCM"}`,
		`{"alg":"dir","enc":"A256GCM","zip":""}`,
		// zip takes precedence even when another field is also bad.
		`{"alg":"RSA1_5","enc":"A256GCM","zip":"DEF"}`,
	}
	for i, raw := range cases {
		if err := ValidateCompactJWE(tokenWithRawHeader(t, raw, "")); !errors.Is(err, ErrCompression) {
			t.Errorf("case %d: want ErrCompression, got %v", i, err)
		}
	}
}

func TestDuplicateHeaderRejected(t *testing.T) {
	epk := validEPKJWK(t)
	epkRaw, _ := json.Marshal(epk)
	nestedDup := `{"alg":"ECDH-ES","enc":"A256GCM","epk":` +
		`{"kty":"EC","kty":"EC","crv":"P-256",` +
		`"x":"` + epk["x"].(string) + `","y":"` + epk["y"].(string) + `"}}`
	arrayDup := `{"alg":"ECDH-ES","enc":"A256GCM","epk":` + string(epkRaw) +
		`,"crit":[{"a":1},{"a":2,"a":3}]}`

	cases := []string{
		`{"alg":"dir","alg":"dir","enc":"A256GCM"}`,
		`{"alg":"dir","enc":"A256GCM","enc":"A256GCM"}`,
		nestedDup,
		arrayDup,
	}
	for i, raw := range cases {
		if err := ValidateCompactJWE(tokenWithRawHeader(t, raw, "")); !errors.Is(err, ErrDuplicateHeader) {
			t.Errorf("case %d: want ErrDuplicateHeader, got %v", i, err)
		}
	}
}

func TestMalformedTokens(t *testing.T) {
	header := b64url([]byte(`{"alg":"dir","enc":"A256GCM"}`))
	full := strings.Join(
		[]string{header, "", "c29tZWl2Mgo", "c29tZWN0", "c29tZXRhdw"}, ".")

	replaceHeader := func(raw string) string {
		return strings.Replace(full, header, b64url([]byte(raw)), 1)
	}

	cases := map[string]error{
		"":                        ErrMalformedToken,
		"a.b.c":                   ErrMalformedToken,
		"a.b.c.d.e.f":             ErrMalformedToken,
		"not base64!.b.c.d.e":     ErrMalformedToken,
		replaceHeader(`not json`): ErrMalformedHeader,
		replaceHeader(`[1,2,3]`):  ErrMalformedHeader,
		replaceHeader(`"scalar"`): ErrMalformedHeader,
		replaceHeader(`{"alg":"dir","enc":"A256GCM"} trailing`): ErrMalformedHeader,
		replaceHeader(`{"alg":"dir"}`):                          ErrMalformedHeader,
		replaceHeader(`{"alg":42,"enc":"A256GCM"}`):             ErrMalformedHeader,
		// Empty mandatory trailing parts.
		strings.Join([]string{header, "", "c29tZWl2Mgo", "c29tZWN0", ""}, "."): ErrMalformedToken,
		strings.Join([]string{header, "", "", "c29tZWN0", "c29tZXRhdw"}, "."):  ErrMalformedToken,
		// Non-empty encrypted key required for key-wrap algs.
		strings.Join([]string{
			b64url([]byte(`{"alg":"A256KW","enc":"A256GCM"}`)),
			"", "c29tZWl2Mgo", "c29tZWN0", "c29tZXRhdw",
		}, "."): ErrMalformedToken,
	}
	for token, want := range cases {
		if err := ValidateCompactJWE(token); !errors.Is(err, want) {
			t.Errorf("token %q: want %v, got %v", token, want, err)
		}
	}
}
