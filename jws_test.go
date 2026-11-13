package netsec

import (
	"crypto"
	"crypto/hmac"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

var testSecret = []byte("super-secret-key")

var enc = base64.RawURLEncoding

// signRaw builds a compact token from arbitrary raw header/payload JSON,
// signing the signing input with HS256. This lets tests craft headers that a
// Go map cannot represent, such as one containing "alg" twice.
func signRaw(t *testing.T, rawHeader, rawPayload string, secret []byte) string {
	t.Helper()
	headerPart := enc.EncodeToString([]byte(rawHeader))
	payloadPart := enc.EncodeToString([]byte(rawPayload))
	signingInput := headerPart + "." + payloadPart
	mac := hmac.New(crypto.SHA256.New, secret)
	mac.Write([]byte(signingInput))
	signaturePart := enc.EncodeToString(mac.Sum(nil))
	return signingInput + "." + signaturePart
}

func TestVerifyRejectsHeaderWithDuplicateAlg(t *testing.T) {
	// "alg" appears twice: HS256 first, then "none". The later value must not
	// overwrite the earlier one, and first-wins is forbidden as well; the
	// token must be rejected outright even though its signature is valid.
	rawHeader := `{"alg":"HS256","typ":"JWT","alg":"none"}`
	rawPayload := `{"iss":"netsec","sub":"alice"}`
	token := signRaw(t, rawHeader, rawPayload, testSecret)

	_, _, err := VerifyCompact(token, testSecret)
	if err == nil {
		t.Fatal("expected duplicate-alg header to be rejected")
	}
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("expected ErrDuplicateKey, got %v", err)
	}
	if !strings.Contains(err.Error(), "protected header") {
		t.Fatalf("error should identify the protected header: %v", err)
	}
}

func TestVerifyRejectsHeaderWithOtherDuplicateKey(t *testing.T) {
	rawHeader := `{"alg":"HS256","kid":"k1","kid":"k2"}`
	token := signRaw(t, rawHeader, `{}`, testSecret)

	_, _, err := VerifyCompact(token, testSecret)
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("expected ErrDuplicateKey, got %v", err)
	}
}

func TestVerifyRejectsPayloadWithDuplicateKey(t *testing.T) {
	rawHeader := `{"alg":"HS256"}`
	rawPayload := `{"iss":"a","iss":"b"}`
	token := signRaw(t, rawHeader, rawPayload, testSecret)

	_, _, err := VerifyCompact(token, testSecret)
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("expected ErrDuplicateKey for payload, got %v", err)
	}
	if !strings.Contains(err.Error(), "payload") {
		t.Fatalf("error should identify the payload: %v", err)
	}
}

func TestVerifyAcceptsWellFormedToken(t *testing.T) {
	token, err := EncodeCompact(
		ProtectedHeader{Algorithm: "HS256", Type: "JWT"},
		Claims{Issuer: "netsec", Subject: "alice", Extra: map[string]any{"role": "admin"}},
		testSecret,
	)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	header, claims, err := VerifyCompact(token, testSecret)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if header.Algorithm != "HS256" || header.Type != "JWT" {
		t.Fatalf("unexpected header: %+v", header)
	}
	if claims.Issuer != "netsec" || claims.Subject != "alice" || claims.Extra["role"] != "admin" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}

func TestVerifyRejectsBadSignature(t *testing.T) {
	token := signRaw(t, `{"alg":"HS256"}`, `{}`, testSecret)
	_, _, err := VerifyCompact(token, []byte("wrong-secret"))
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("expected ErrBadSignature, got %v", err)
	}
}

func TestVerifyRejectsNoneAlgorithm(t *testing.T) {
	token := signRaw(t, `{"alg":"none"}`, `{}`, testSecret)
	_, _, err := VerifyCompact(token, testSecret)
	if !errors.Is(err, ErrNoneAlgorithm) {
		t.Fatalf("expected ErrNoneAlgorithm, got %v", err)
	}

}

func TestVerifyRejectsMissingAlgorithm(t *testing.T) {
	token := signRaw(t, `{"typ":"JWT"}`, `{}`, testSecret)
	_, _, err := VerifyCompact(token, testSecret)
	if !errors.Is(err, ErrEmptyAlgorithm) {
		t.Fatalf("expected ErrEmptyAlgorithm, got %v", err)
	}
}

func TestVerifyRejectsUnsupportedAlgorithm(t *testing.T) {
	token := signRaw(t, `{"alg":"RS256"}`, `{}`, testSecret)
	_, _, err := VerifyCompact(token, testSecret)
	if !errors.Is(err, ErrUnsupportedAlg) {
		t.Fatalf("expected ErrUnsupportedAlg, got %v", err)
	}
}

func TestVerifyRejectsMalformedToken(t *testing.T) {
	for _, token := range []string{"", "a.b", "a.b.c.d", "###.###.###"} {
		if _, _, err := VerifyCompact(token, testSecret); err == nil {
			t.Fatalf("expected malformed error for %q", token)
		}
	}
}
