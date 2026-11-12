package netsec

import (
	"crypto/rand"
	"crypto/rsa"
	"math/big"
	"testing"
)

// rawEncrypt performs the textbook RSA public operation on an arbitrary
// k-byte encoded message, including blocks with invalid PKCS #1 v1.5 padding.
func rawEncrypt(t *testing.T, pub *rsa.PublicKey, em []byte) []byte {
	t.Helper()
	k := pub.Size()
	if len(em) != k {
		t.Fatalf("encoded message length = %d, want %d", len(em), k)
	}
	m := new(big.Int).SetBytes(em)
	if m.Cmp(pub.N) >= 0 {
		t.Fatal("encoded message must be smaller than the modulus")
	}
	c := new(big.Int).Exp(m, big.NewInt(int64(pub.E)), pub.N)
	out := make([]byte, k)
	cb := c.Bytes()
	copy(out[k-len(cb):], cb)
	return out
}

// validEM builds a PKCS #1 v1.5 block carrying msg:
// 0x00 || 0x02 || PS(non-zero) || 0x00 || msg.
func validEM(k int, msg []byte) []byte {
	em := make([]byte, k)
	em[1] = 0x02
	sep := k - len(msg) - 1
	for i := 2; i < sep; i++ {
		em[i] = 0xAB
	}
	em[sep] = 0x00
	copy(em[sep+1:], msg)
	return em
}

func randomSecret(t *testing.T) []byte {
	t.Helper()
	secret := make([]byte, PreMasterSecretLen)
	if _, err := rand.Read(secret); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return secret
}

func TestDecryptKeyTransport_ValidCiphertext(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pms := randomSecret(t)
	ct := rawEncrypt(t, &priv.PublicKey, validEM(priv.Size(), pms))

	got, err := DecryptKeyTransport(priv, ct)
	if err != nil {
		t.Fatalf("DecryptKeyTransport returned error: %v", err)
	}
	if len(got) != PreMasterSecretLen {
		t.Fatalf("result length = %d, want %d", len(got), PreMasterSecretLen)
	}
	if string(got[:]) != string(pms) {
		t.Fatal("valid ciphertext did not recover the premaster secret")
	}
}

func TestDecryptKeyTransport_RepeatedRequestsAreStable(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pms := randomSecret(t)
	validCT := rawEncrypt(t, &priv.PublicKey, validEM(priv.Size(), pms))

	badEM := validEM(priv.Size(), pms)
	badEM[1] = 0x03 // break the 0x02 type byte
	invalidCT := rawEncrypt(t, &priv.PublicKey, badEM)
	truncated := validCT[:len(validCT)-7]

	first, err := DecryptKeyTransport(priv, validCT)
	if err != nil {
		t.Fatalf("valid path error: %v", err)
	}
	for i := 0; i < 8; i++ {
		got, err := DecryptKeyTransport(priv, validCT)
		if err != nil || got != first {
			t.Fatalf("valid ciphertext not stable on repetition %d", i)
		}
	}

	firstBad, err := DecryptKeyTransport(priv, invalidCT)
	if err != nil {
		t.Fatalf("invalid path error: %v", err)
	}
	for i := 0; i < 8; i++ {
		got, err := DecryptKeyTransport(priv, invalidCT)
		if err != nil || got != firstBad {
			t.Fatalf("invalid ciphertext not stable on repetition %d", i)
		}
	}

	firstTrunc, err := DecryptKeyTransport(priv, truncated)
	if err != nil {
		t.Fatalf("truncated path error: %v", err)
	}
	for i := 0; i < 8; i++ {
		got, err := DecryptKeyTransport(priv, truncated)
		if err != nil || got != firstTrunc {
			t.Fatalf("truncated ciphertext not stable on repetition %d", i)
		}
	}
}

func TestDecryptKeyTransport_TruncatedAndOversizedCiphertext(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pms := randomSecret(t)
	validCT := rawEncrypt(t, &priv.PublicKey, validEM(priv.Size(), pms))

	cases := map[string][]byte{
		"one byte short":     validCT[:len(validCT)-1],
		"half block":         validCT[:len(validCT)/2],
		"single byte":        validCT[:1],
		"empty":              {},
		"nil":                nil,
		"one byte too long":  append(append([]byte{}, validCT...), 0x01),
		"value over modulus": priv.N.Bytes(),
	}

	for name, ct := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := DecryptKeyTransport(priv, ct)
			if err != nil {
				t.Fatalf("got error %v, want implicit rejection with nil error", err)
			}
			if len(got) != PreMasterSecretLen {
				t.Fatalf("length = %d, want %d", len(got), PreMasterSecretLen)
			}
			if string(got[:]) == string(pms) {
				t.Fatal("malformed ciphertext unexpectedly returned the real secret")
			}
			again, err := DecryptKeyTransport(priv, ct)
			if err != nil || again != got {
				t.Fatal("fallback value is not deterministic")
			}
		})
	}
}

func TestDecryptKeyTransport_WrongPrefix(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pms := randomSecret(t)

	for _, mutate := range []func([]byte){
		func(em []byte) { em[0] = 0x01 }, // leading byte must be 0x00
		func(em []byte) { em[1] = 0x01 }, // block type must be 0x02
		func(em []byte) { em[0], em[1] = 0x00, 0x01 },
		func(em []byte) { em[0], em[1] = 0x02, 0x02 },
	} {
		em := validEM(priv.Size(), pms)
		mutate(em)
		ct := rawEncrypt(t, &priv.PublicKey, em)

		got, err := DecryptKeyTransport(priv, ct)
		if err != nil {
			t.Fatalf("got error %v, want nil error on bad prefix", err)
		}
		if string(got[:]) == string(pms) {
			t.Fatal("bad prefix unexpectedly returned the real secret")
		}
		again, _ := DecryptKeyTransport(priv, ct)
		if again != got {
			t.Fatal("fallback value is not deterministic")
		}
	}
}

func TestDecryptKeyTransport_ZeroBytePositions(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	k := priv.Size()
	pms := randomSecret(t)

	type blockCase struct {
		name   string
		em     []byte
		wantOK bool
	}

	valid := validEM(k, pms)

	// Separator too early: PS shorter than 8 bytes, and every byte after the
	// separator is non-zero so no later zero can be picked.
	earlyPS7 := make([]byte, k)
	earlyPS7[1] = 0x02
	for i := 2; i < 9; i++ {
		earlyPS7[i] = 0xCD
	}
	for i := 10; i < k; i++ {
		earlyPS7[i] = 0xEF
	}

	// A zero inside the padding string terminates PS prematurely.
	zeroInPS := validEM(k, pms)
	zeroInPS[5] = 0x00

	// No zero separator anywhere after the block type byte.
	noSeparator := make([]byte, k)
	noSeparator[0] = 0x00
	noSeparator[1] = 0x02
	for i := 2; i < k; i++ {
		noSeparator[i] = 0x77
	}

	// A valid envelope whose embedded message has the wrong length (16 bytes).
	shortMsg := make([]byte, 16)
	for i := range shortMsg {
		shortMsg[i] = byte(i + 1)
	}
	wrongLength := validEM(k, shortMsg)

	cases := []blockCase{
		{name: "separator after seven PS bytes", em: earlyPS7, wantOK: false},
		{name: "zero byte inside PS", em: zeroInPS, wantOK: false},
		{name: "missing zero separator", em: noSeparator, wantOK: false},
		{name: "valid envelope wrong message length", em: wrongLength, wantOK: false},
		{name: "valid envelope at expected position", em: valid, wantOK: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ct := rawEncrypt(t, &priv.PublicKey, tc.em)
			got, err := DecryptKeyTransport(priv, ct)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != PreMasterSecretLen {
				t.Fatalf("length = %d, want %d", len(got), PreMasterSecretLen)
			}
			recoveredReal := string(got[:]) == string(pms)
			if recoveredReal != tc.wantOK {
				t.Fatalf("real secret leaked = %v, want %v", recoveredReal, tc.wantOK)
			}
			again, _ := DecryptKeyTransport(priv, ct)
			if again != got {
				t.Fatal("result is not deterministic across repeated requests")
			}
		})
	}
}

func TestDecryptKeyTransport_ErrorsAreIndistinguishable(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pms := randomSecret(t)
	validCT := rawEncrypt(t, &priv.PublicKey, validEM(priv.Size(), pms))

	// Valid and every class of invalid input share the same error object: nil.
	inputs := [][]byte{
		validCT,
		validCT[:1],
		{},
		nil,
		append(append([]byte{}, validCT...), 0xFF),
		priv.N.Bytes(),
	}
	for _, ct := range inputs {
		if _, err := DecryptKeyTransport(priv, ct); err != nil {
			t.Fatalf("ciphertext-dependent error leaked: %v", err)
		}
	}

	// Only key-misuse errors surface, and they all share one sentinel object.
	badKey := &rsa.PrivateKey{}
	var zero [PreMasterSecretLen]byte
	for _, key := range []*rsa.PrivateKey{nil, badKey} {
		got, err := DecryptKeyTransport(key, validCT)
		if err != ErrKeyTransport {
			t.Fatalf("err = %v, want ErrKeyTransport", err)
		}
		if got != zero {
			t.Fatal("key-misuse path must return the zero value")
		}
	}
}

func TestDecryptKeyTransport_FallbackKeying(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	badEM := validEM(priv.Size(), randomSecret(t))
	badEM[1] = 0x04
	ctA := rawEncrypt(t, &priv.PublicKey, badEM)

	badEM2 := validEM(priv.Size(), randomSecret(t))
	badEM2[1] = 0x05
	ctB := rawEncrypt(t, &priv.PublicKey, badEM2)

	a, err := DecryptKeyTransport(priv, ctA)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, _ := DecryptKeyTransport(priv, ctB)
	if a == b {
		t.Fatal("different ciphertexts produced the same fallback value")
	}

	// The same ciphertext under a different private key must diverge.
	c, err := DecryptKeyTransport(other, ctA)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == a {
		t.Fatal("fallback value is not bound to the private key")
	}

	allSame := true
	for i := 1; i < len(a); i++ {
		if a[i] != a[0] {
			allSame = false
		}
	}
	if allSame {
		t.Fatal("fallback value looks non-random")
	}
}

func TestPKCS1v15Unpad(t *testing.T) {
	k := 64
	ok := validEM(k, make([]byte, 16))
	valid, index := pkcs1v15Unpad(ok)
	if valid != 1 || index != k-16 {
		t.Fatalf("valid block: valid=%d index=%d, want 1/%d", valid, index, k-16)
	}

	invalid := validEM(k, make([]byte, 16))
	invalid[1] = 0x03
	if valid, _ := pkcs1v15Unpad(invalid); valid != 0 {
		t.Fatalf("corrupted type byte: valid=%d, want 0", valid)
	}

	shortPS := make([]byte, k)
	shortPS[1] = 0x02
	for i := 2; i < 9; i++ {
		shortPS[i] = 0x01
	}
	if valid, _ := pkcs1v15Unpad(shortPS); valid != 0 {
		t.Fatalf("seven-byte PS: valid=%d, want 0", valid)
	}
}
