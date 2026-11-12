package netsec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// buildDirJWE produces a genuine, decryptable dir + A256GCM compact JWE and
// returns it together with the shared CEK.
func buildDirJWE(t *testing.T, plaintext []byte) (string, []byte) {
	t.Helper()
	cek := make([]byte, 32)
	if _, err := rand.Read(cek); err != nil {
		t.Fatalf("cek: %v", err)
	}
	protected := map[string]string{"alg": "dir", "enc": "A256GCM"}
	protectedRaw, err := json.Marshal(protected)
	if err != nil {
		t.Fatalf("protected header: %v", err)
	}

	block, err := aes.NewCipher(cek)
	if err != nil {
		t.Fatalf("aes: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	iv := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(iv); err != nil {
		t.Fatalf("iv: %v", err)
	}
	aad := []byte(b64url(protectedRaw))
	sealed := gcm.Seal(nil, iv, plaintext, aad)
	ciphertext, tag := sealed[:len(sealed)-gcm.Overhead()], sealed[len(sealed)-gcm.Overhead():]

	token := strings.Join([]string{
		b64url(protectedRaw),
		"", // empty JWE Encrypted Key for dir
		b64url(iv),
		b64url(ciphertext),
		b64url(tag),
	}, ".")
	return token, cek
}

// decrypt models the gateway decryption entry point: policy first, key use
// second. It records whether any cryptographic material was touched.
func decrypt(t *testing.T, token string, cek []byte) ([]byte, bool, error) {
	t.Helper()
	cryptoTouched := false
	if err := ValidateCompactJWE(token); err != nil {
		return nil, cryptoTouched, err
	}

	cryptoTouched = true
	parts := strings.Split(token, ".")
	protectedRaw, _ := base64.RawURLEncoding.DecodeString(parts[0])
	iv, _ := base64.RawURLEncoding.DecodeString(parts[2])
	ciphertext, _ := base64.RawURLEncoding.DecodeString(parts[3])
	tag, _ := base64.RawURLEncoding.DecodeString(parts[4])

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, cryptoTouched, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, cryptoTouched, err
	}
	plaintext, err := gcm.Open(nil, iv, append(ciphertext, tag...),
		[]byte(b64url(protectedRaw)))
	if err != nil {
		return nil, cryptoTouched, err
	}
	return plaintext, cryptoTouched, nil
}

func TestValidCiphertextRoundTrip(t *testing.T) {
	want := []byte("gateway-payload-12345")
	token, cek := buildDirJWE(t, want)

	if err := ValidateCompactJWE(token); err != nil {
		t.Fatalf("valid JWE rejected by pre-decryption checks: %v", err)
	}
	got, touched, err := decrypt(t, token, cek)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !touched {
		t.Fatal("decryption path should run for a valid token")
	}
	if string(got) != string(want) {
		t.Fatalf("plaintext mismatch: got %q want %q", got, want)
	}
}

// TestRejectionBeforeDecryption mutates only attacker-controlled parts of a
// real ciphertext and proves every policy failure is raised before any
// cryptographic operation.
func TestRejectionBeforeDecryption(t *testing.T) {
	validToken, cek := buildDirJWE(t, []byte("secret"))
	validParts := strings.Split(validToken, ".")

	replaceProtected := func(raw string) string {
		return strings.Join(append([]string{b64url([]byte(raw))}, validParts[1:]...), ".")
	}

	cases := map[string]struct {
		token string
		want  error
	}{
		"weak alg": {
			replaceProtected(`{"alg":"RSA1_5","enc":"A256GCM"}`),
			ErrUnsupportedAlg,
		},
		"weak enc": {
			replaceProtected(`{"alg":"dir","enc":"A512GCM"}`),
			ErrUnsupportedEnc,
		},
		"length mismatch": {
			// A256KW wraps 32 bytes but A128GCM needs a 16-byte CEK.
			replaceProtected(`{"alg":"A256KW","enc":"A128GCM"}`),
			ErrAlgorithmMismatch,
		},
		"zip bomb header": {
			replaceProtected(`{"alg":"dir","enc":"A256GCM","zip":"DEF"}`),
			ErrCompression,
		},
		"duplicate alg": {
			replaceProtected(`{"alg":"dir","alg":"dir","enc":"A256GCM"}`),
			ErrDuplicateHeader,
		},
		"off-curve epk": {
			replaceProtected(`{"alg":"ECDH-ES","enc":"A256GCM","epk":` +
				`{"kty":"EC","crv":"P-256",` +
				`"x":"AQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",` +
				`"y":"AQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}}`),
			ErrEPK,
		},
		"not five parts": {
			strings.Join(validParts[:4], "."),
			ErrMalformedToken,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, touched, err := decrypt(t, tc.token, cek)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			if touched {
				t.Fatalf("%s: crypto path executed before policy rejection", name)
			}
		})
	}
}

// TestCorruptCiphertextPassesHeaderPolicy shows structural ciphertext damage
// is left to the authenticated decryption layer, while header attacks are
// stopped earlier.
func TestCorruptCiphertextPassesHeaderPolicy(t *testing.T) {
	validToken, cek := buildDirJWE(t, []byte("secret"))
	parts := strings.Split(validToken, ".")
	parts[4] = b64url([]byte("tampered-tag!!"))
	corrupt := strings.Join(parts, ".")

	if err := ValidateCompactJWE(corrupt); err != nil {
		t.Fatalf("header policy should pass for corrupted tag: %v", err)
	}
	_, touched, err := decrypt(t, corrupt, cek)
	if !touched {
		t.Fatal("corrupt tag should reach authenticated decryption")
	}
	if err == nil {
		t.Fatal("GCM tag verification must fail for a tampered tag")
	}
}
