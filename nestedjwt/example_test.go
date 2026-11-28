package nestedjwt_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"formal.local/w28-5b20ffbc87/nestedjwt"
)

func Example() {
	// Gateway JWE decryption key and a registered client signing key.
	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}

	store := nestedjwt.MapKeyStore{
		DecryptionKeys: map[string]crypto.PrivateKey{"gateway-2026": serverKey},
		SigningKeys: map[string]crypto.PublicKey{
			"client-42": &clientKey.PublicKey,
		},
	}
	verifier := nestedjwt.New(store) // DefaultPolicy: nested JWE mandatory.

	now := time.Now().Unix()
	claims := map[string]any{
		"iss": "client-42",
		"sub": "operator-9",
		"aud": []string{"treasury-gateway"},
		"iat": now,
		"nbf": now,
		"exp": now + 120,
		"jti": "request-0001",
	}
	rawClaims, _ := json.Marshal(claims)

	// The client signs first, then encrypts under the gateway public key.
	jws := signJWS("ES256", "client-42", clientKey, rawClaims)
	opts := jweOptions{
		alg: "RSA-OAEP-256", enc: "A256GCM", kid: "gateway-2026", cty: "JWT",
	}
	token := encryptJWE(&serverKey.PublicKey, []byte(jws), opts)

	verified, err := verifier.Verify(token)
	if err != nil {
		panic(err)
	}
	fmt.Println("depth:", verified.NestingDepth)
	fmt.Println("iss:", verified.Claims.Issuer)
	fmt.Println("jti:", verified.Claims.JWTID)

	// Replaying the same compact token is blocked by the (iss, jti) store.
	if _, err = verifier.Verify(token); errors.Is(err, nestedjwt.ErrReplayed) {
		fmt.Println("replay blocked")
	}

	// Output:
	// depth: 2
	// iss: client-42
	// jti: request-0001
	// replay blocked
}
