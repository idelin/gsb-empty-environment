package netsec

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"testing"
)

func TestValidateSubscriptionKeys(t *testing.T) {
	valid := generateSubscriptionKeys(t)
	if err := ValidateSubscriptionKeys(valid); err != nil {
		t.Fatalf("valid keys rejected: %v", err)
	}

	badAuth := SubscriptionKeys{P256DH: valid.P256DH, Auth: make([]byte, 15)}
	if err := ValidateSubscriptionKeys(badAuth); err == nil {
		t.Fatal("15-byte auth should be rejected")
	}
	badPoint := SubscriptionKeys{P256DH: make([]byte, 65), Auth: valid.Auth}
	if err := ValidateSubscriptionKeys(badPoint); err == nil {
		t.Fatal("zero point should be rejected")
	}
}

func TestEncryptPayloadRoundTrip(t *testing.T) {
	// 用户代理侧密钥（ecdh 私钥 + 公钥点 + auth）。
	uaPrivate, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate UA private key: %v", err)
	}
	auth := make([]byte, authSecretLen)
	for i := range auth {
		auth[i] = byte(i + 1)
	}
	keys := SubscriptionKeys{P256DH: uaPrivate.PublicKey().Bytes(), Auth: auth}

	cases := [][]byte{
		[]byte("hello web push"),
		bytes.Repeat([]byte("payload-"), 200),
		{0x00, 0x01, 0x02, 0x03},
	}
	for _, plaintext := range cases {
		message, err := EncryptPayload(keys, plaintext)
		if err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		// 头部最小长度：salt(16) + rs(4) + idlen(1) + keyid(65)。
		if len(message) <= saltLen+4+1+uncompressedPointLen {
			t.Fatal("ciphertext missing")
		}
		decrypted := decryptAES128GCM(t, uaPrivate, auth, message)
		if !bytes.Equal(decrypted, plaintext) {
			t.Fatalf("round trip mismatch: got %q want %q", decrypted, plaintext)
		}
	}
}

func TestEncryptPayloadUsesFreshEphemeralAndSalt(t *testing.T) {
	keys := generateSubscriptionKeys(t)
	first, err := EncryptPayload(keys, []byte("same plaintext"))
	if err != nil {
		t.Fatalf("encrypt first: %v", err)
	}
	second, err := EncryptPayload(keys, []byte("same plaintext"))
	if err != nil {
		t.Fatalf("encrypt second: %v", err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("each message must use a fresh salt and ephemeral key")
	}
}
