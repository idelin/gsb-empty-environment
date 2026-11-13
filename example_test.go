package netsec_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"

	"example.org/netsec/q0108"
)

type examplePrefixServer struct {
	secret []byte
}

// verify is the only capability exposed to the attack: message and tag.
func (server *examplePrefixServer) verify(message, tag []byte) bool {
	h := sha256.New()
	_, _ = h.Write(server.secret)
	_, _ = h.Write(message)
	return hmac.Equal(tag, h.Sum(nil))
}

func ExampleAttackSHA256PrefixMAC() {
	secret := []byte("demo-secret")
	server := &examplePrefixServer{secret: secret}
	message := []byte("user=alice")
	originalTag := sha256.Sum256(append(append([]byte(nil), secret...), message...))

	result, err := netsec.AttackSHA256PrefixMAC(message, originalTag[:], []byte("&role=admin"), server.verify)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Printf("found=%t secretLength=%d queries=%d accepted=%t\n",
		result.Found(),
		result.Witness.SecretLength,
		result.Queries,
		server.verify(result.Witness.ForgedMessage, result.Witness.ForgedTag))

	// Output:
	// found=true secretLength=11 queries=11 accepted=true
}

func ExampleHMACVerifier() {
	verifier := netsec.NewHMACVerifier([]byte("demo-key"))
	body := []byte("body")
	tag := verifier.Sign("POST", "/items", body)

	fmt.Println(verifier.Verify("POST", "/items", body, tag))
	fmt.Println(verifier.Verify("POST", "/items", append(body, '!'), tag))

	// Output:
	// true
	// false
}
