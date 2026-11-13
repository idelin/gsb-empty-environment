package netsec_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	netsec "example.org/netsec/q0107"
)

const testSecret = "correct horse battery staple"

func TestVerifyNoneAlgorithmFails(t *testing.T) {
	for _, algorithm := range []string{"none", "NONE", "None"} {
		token := makeUnsignedToken(t, algorithm)

		_, err := netsec.Verify(token, []byte(testSecret), []string{"HS256"})
		if !errors.Is(err, netsec.ErrAlgorithmNotAllowed) {
			t.Fatalf("alg %q: expected ErrAlgorithmNotAllowed, got %v", algorithm, err)
		}
	}
}

func TestVerifyEmptyAlgorithmFails(t *testing.T) {
	token := makeUnsignedToken(t, "")

	_, err := netsec.Verify(token, []byte(testSecret), []string{"HS256"})
	if !errors.Is(err, netsec.ErrAlgorithmNotAllowed) {
		t.Fatalf("expected ErrAlgorithmNotAllowed, got %v", err)
	}
}

func TestVerifyMissingAlgorithmFails(t *testing.T) {
	token := makeToken(t,
		rawHeader(`{"typ":"JWT"}`),
		mustJSON(t, map[string]any{"sub": "alice"}),
		nil,
	)

	_, err := netsec.Verify(token, []byte(testSecret), []string{"HS256"})
	if !errors.Is(err, netsec.ErrAlgorithmNotAllowed) {
		t.Fatalf("expected ErrAlgorithmNotAllowed, got %v", err)
	}
}

func TestVerifyListedAlgorithmWithValidSignatureSucceeds(t *testing.T) {
	token := signHS256(t, map[string]any{
		"alg": "HS256",
		"typ": "JWT",
	}, map[string]any{
		"sub":  "alice",
		"role": "reader",
	})

	verifier, err := netsec.NewVerifier([]byte(testSecret), []string{"HS256"})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	claims, err := verifier.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims["sub"] != "alice" {
		t.Fatalf("sub = %v, want alice", claims["sub"])
	}
	if claims["role"] != "reader" {
		t.Fatalf("role = %v, want reader", claims["role"])
	}
}

func TestVerifyRejectsUnlistedAlgorithm(t *testing.T) {
	token := signHS256(t, map[string]any{
		"alg": "HS256",
		"typ": "JWT",
	}, map[string]any{"sub": "alice"})

	_, err := netsec.Verify(token, []byte(testSecret), []string{"HS512"})
	if !errors.Is(err, netsec.ErrAlgorithmNotAllowed) {
		t.Fatalf("expected ErrAlgorithmNotAllowed, got %v", err)
	}
}

func TestVerifyRejectsEmptySignature(t *testing.T) {
	header := mustJSON(t, map[string]any{"alg": "HS256", "typ": "JWT"})
	payload := mustJSON(t, map[string]any{"sub": "alice"})
	token := encodeSegment(header) + "." + encodeSegment(payload) + "."

	_, err := netsec.Verify(token, []byte(testSecret), []string{"HS256"})
	if !errors.Is(err, netsec.ErrSignatureInvalid) {
		t.Fatalf("expected ErrSignatureInvalid, got %v", err)
	}
}

func TestVerifyRejectsBadSignature(t *testing.T) {
	token := signHS256(t, map[string]any{"alg": "HS256"}, map[string]any{"sub": "alice"})
	parts := strings.Split(token, ".")
	last := parts[2][len(parts[2])-1]
	replacement := byte('A')
	if last == 'A' {
		replacement = 'B'
	}
	parts[2] = parts[2][:len(parts[2])-1] + string(replacement)
	token = strings.Join(parts, ".")

	_, err := netsec.Verify(token, []byte(testSecret), []string{"HS256"})
	if !errors.Is(err, netsec.ErrSignatureInvalid) {
		t.Fatalf("expected ErrSignatureInvalid, got %v", err)
	}
}

func TestNewVerifierRejectsUnsafeAllowlists(t *testing.T) {
	tests := []struct {
		name       string
		algorithms []string
	}{
		{name: "empty list"},
		{name: "none", algorithms: []string{"none"}},
		{name: "uppercase none", algorithms: []string{"NONE"}},
		{name: "empty algorithm", algorithms: []string{""}},
		{name: "unknown algorithm", algorithms: []string{"HS999"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := netsec.NewVerifier([]byte(testSecret), test.algorithms)
			if !errors.Is(err, netsec.ErrInvalidConfiguration) {
				t.Fatalf("expected ErrInvalidConfiguration, got %v", err)
			}
			if len(test.algorithms) != 0 &&
				(test.algorithms[0] == "" ||
					strings.EqualFold(test.algorithms[0], "none")) &&
				!errors.Is(err, netsec.ErrAlgorithmNotAllowed) {
				t.Fatalf("expected ErrAlgorithmNotAllowed, got %v", err)
			}
		})
	}
}

func TestVerifyRejectsDuplicateAlgorithmHeader(t *testing.T) {
	header := rawHeader(`{"alg":"none","alg":"HS256"}`)
	payload := mustJSON(t, map[string]any{"sub": "alice"})
	token := encodeSegment(header) + "." + encodeSegment(payload) + "."

	_, err := netsec.Verify(token, []byte(testSecret), []string{"HS256"})
	if !errors.Is(err, netsec.ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken for duplicate alg, got %v", err)
	}
}

func TestVerifyAlgorithmNameIsCaseSensitive(t *testing.T) {
	header := rawHeader(`{"alg":"hs256"}`)
	payload := mustJSON(t, map[string]any{"sub": "alice"})
	signingInput := encodeSegment(header) + "." + encodeSegment(payload)
	signature := hmacSHA256([]byte(testSecret), []byte(signingInput))

	_, err := netsec.Verify(signingInput+"."+encodeSegment(signature),
		[]byte(testSecret), []string{"HS256"})
	if !errors.Is(err, netsec.ErrAlgorithmNotAllowed) {
		t.Fatalf("expected ErrAlgorithmNotAllowed, got %v", err)
	}
}

func TestVerifyRejectsWrongKeyType(t *testing.T) {
	token := signHS256(t, map[string]any{"alg": "HS256"}, map[string]any{"sub": "alice"})

	_, err := netsec.Verify(token, "not-a-byte-slice", []string{"HS256"})
	if !errors.Is(err, netsec.ErrInvalidConfiguration) ||
		!errors.Is(err, netsec.ErrInvalidKey) {
		t.Fatalf("expected configuration/key error, got %v", err)
	}
}

func signHS256(t *testing.T, header, payload map[string]any) string {
	t.Helper()
	headerBytes := mustJSON(t, header)
	payloadBytes := mustJSON(t, payload)
	signingInput := encodeSegment(headerBytes) + "." + encodeSegment(payloadBytes)
	signature := hmacSHA256([]byte(testSecret), []byte(signingInput))
	return signingInput + "." + encodeSegment(signature)
}

func makeUnsignedToken(t *testing.T, algorithm string) string {
	t.Helper()
	header := mustJSON(t, map[string]any{"alg": algorithm, "typ": "JWT"})
	payload := mustJSON(t, map[string]any{"sub": "alice"})
	return makeToken(t, header, payload, nil)
}

func makeToken(t *testing.T, header, payload, signature []byte) string {
	t.Helper()
	token := encodeSegment(header) + "." + encodeSegment(payload)
	if signature != nil {
		token += "." + encodeSegment(signature)
	} else {
		token += "."
	}
	return token
}

func rawHeader(value string) []byte {
	return []byte(value)
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return encoded
}

func encodeSegment(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}
