package nestedjwt_test

import (
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"testing"
	"time"

	"formal.local/w28-5b20ffbc87/nestedjwt"
)

var testB64 = base64.RawURLEncoding

const (
	serverKID = "server-key-1"
	clientKID = "client-key-1"
)

func mustRSAKey() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return key
}

func mustECKey() *ecdsa.PrivateKey {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	return key
}

func b64JSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return testB64.EncodeToString(raw)
}

func newKeyStore(t *testing.T, serverKey *rsa.PrivateKey, clientEC *ecdsa.PrivateKey, opts ...nestedjwt.Option) (*nestedjwt.Verifier, *rsa.PrivateKey, *ecdsa.PrivateKey) {
	t.Helper()
	store := nestedjwt.MapKeyStore{
		DecryptionKeys: map[string]crypto.PrivateKey{serverKID: serverKey},
		SigningKeys: map[string]crypto.PublicKey{
			clientKID: &clientEC.PublicKey,
		},
	}
	return nestedjwt.New(store, opts...), serverKey, clientEC
}

func newHarness(t *testing.T, opts ...nestedjwt.Option) (*nestedjwt.Verifier, *rsa.PrivateKey, *ecdsa.PrivateKey) {
	t.Helper()
	return newKeyStore(t, mustRSAKey(), mustECKey(), opts...)
}

// signJWS builds a compact JWS. rawPayload may be JSON claims or an arbitrary
// byte string such as another compact token.
func signJWS(alg, kid string, signingKey crypto.PrivateKey, rawPayload []byte) string {
	encodedHeader := b64JSON(map[string]string{"alg": alg, "kid": kid})
	encodedPayload := testB64.EncodeToString(rawPayload)
	signingInput := encodedHeader + "." + encodedPayload
	signature := signBytes(alg, signingKey, []byte(signingInput))
	return signingInput + "." + testB64.EncodeToString(signature)
}

// signJWSNoKID builds a JWS whose protected header deliberately omits kid.
func signJWSNoKID(alg string, signingKey crypto.PrivateKey, rawPayload []byte) string {
	encodedHeader := b64JSON(map[string]string{"alg": alg})
	encodedPayload := testB64.EncodeToString(rawPayload)
	signingInput := encodedHeader + "." + encodedPayload
	signature := signBytes(alg, signingKey, []byte(signingInput))
	return signingInput + "." + testB64.EncodeToString(signature)
}

// unsignedJWS builds an alg="none" compact serialization with an empty
// signature segment, per RFC 7515 appendix A.5.
func unsignedJWS(kid string, rawPayload []byte) string {
	encodedHeader := b64JSON(map[string]string{"alg": "none", "kid": kid})
	return encodedHeader + "." + testB64.EncodeToString(rawPayload) + "."
}

// hmacSignedJWS builds a symmetric HS256 compact serialization.
func hmacSignedJWS(alg, kid string, rawPayload []byte) string {
	encodedHeader := b64JSON(map[string]string{"alg": alg, "kid": kid})
	encodedPayload := testB64.EncodeToString(rawPayload)
	mac := hmac.New(sha256.New, []byte("shared-secret-must-not-be-accepted"))
	mac.Write([]byte(encodedHeader + "." + encodedPayload))
	return encodedHeader + "." + encodedPayload + "." + testB64.EncodeToString(mac.Sum(nil))
}

func signBytes(alg string, key crypto.PrivateKey, input []byte) []byte {
	digest := sha256.Sum256(input)
	switch alg {
	case "ES256":
		ecKey, ok := key.(*ecdsa.PrivateKey)
		if !ok {
			panic("ES256 requires *ecdsa.PrivateKey")
		}
		r, s, err := ecdsa.Sign(rand.Reader, ecKey, digest[:])
		if err != nil {
			panic(err)
		}
		der, err := asn1.Marshal(struct{ R, S *big.Int }{r, s})
		if err != nil {
			panic(err)
		}
		return der
	case "RS256":
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			panic("RS256 requires *rsa.PrivateKey")
		}
		signature, err := rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA256, digest[:])
		if err != nil {
			panic(err)
		}
		return signature
	case "PS256":
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			panic("PS256 requires *rsa.PrivateKey")
		}
		signature, err := rsa.SignPSS(rand.Reader, rsaKey, crypto.SHA256, digest[:], &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		if err != nil {
			panic(err)
		}
		return signature
	default:
		panic("unsupported test signing alg: " + alg)
	}
}

// jweOptions customizes an outer JWE envelope for negative tests.
type jweOptions struct {
	alg          string
	enc          string
	kid          string
	cty          string
	omitCTY      bool
	zip          string
	crit         []string
	extraHeaders map[string]any
}

func defaultJWEOptions(kid string) jweOptions {
	return jweOptions{
		alg: "RSA-OAEP-256",
		enc: "A256GCM",
		kid: kid,
		cty: "JWT",
	}
}

// encryptJWE wraps plaintext in a compact JWE using RSA-OAEP-256 + A256GCM.
func encryptJWE(recipient *rsa.PublicKey, plaintext []byte, opts jweOptions) string {
	header := map[string]any{
		"alg": opts.alg,
		"enc": opts.enc,
		"kid": opts.kid,
	}
	if !opts.omitCTY {
		header["cty"] = opts.cty
	}
	if opts.zip != "" {
		header["zip"] = opts.zip
	}
	if len(opts.crit) > 0 {
		header["crit"] = opts.crit
	}
	for key, value := range opts.extraHeaders {
		header[key] = value
	}
	encodedHeader := b64JSON(header)

	cek := make([]byte, 32)
	if _, err := rand.Read(cek); err != nil {
		panic(err)
	}
	encryptedKey, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, recipient, cek, nil)
	if err != nil {
		panic(err)
	}

	block, err := aes.NewCipher(cek)
	if err != nil {
		panic(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	iv := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(iv); err != nil {
		panic(err)
	}
	sealed := gcm.Seal(nil, iv, plaintext, []byte(encodedHeader))
	tagOffset := len(sealed) - gcm.Overhead()
	ciphertext, tag := sealed[:tagOffset], sealed[tagOffset:]

	return encodedHeader + "." +
		testB64.EncodeToString(encryptedKey) + "." +
		testB64.EncodeToString(iv) + "." +
		testB64.EncodeToString(ciphertext) + "." +
		testB64.EncodeToString(tag)
}

// buildNested signs claims with clientEC and encrypts the resulting JWS under
// the server's JWE public key.
func buildNested(serverKey *rsa.PrivateKey, clientEC *ecdsa.PrivateKey, claims map[string]any, opts jweOptions) string {
	rawPayload, err := json.Marshal(claims)
	if err != nil {
		panic(err)
	}
	jws := signJWS("ES256", clientKID, clientEC, rawPayload)
	return encryptJWE(&serverKey.PublicKey, []byte(jws), opts)
}

func sampleClaims(issuer, jti string) map[string]any {
	now := time.Now().Unix()
	return map[string]any{
		"iss":  issuer,
		"sub":  "user-7788",
		"aud":  []string{"gateway", "treasury"},
		"iat":  now,
		"nbf":  now - 10,
		"exp":  now + 300,
		"jti":  jti,
		"role": "payment-admin",
	}
}

func mustClaimsJSON(t *testing.T, claims map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	return raw
}
