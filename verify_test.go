package netsec

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"errors"
	"math/big"
	"testing"
)

func TestVerifySHA256PKCS1v15Valid(t *testing.T) {
	priv := mustGenerateKey(t, 1024)
	digest := sha256.Sum256([]byte("valid message"))
	sig := signDigest(t, priv, digest[:])

	if err := VerifySHA256PKCS1v15(&priv.PublicKey, digest[:], sig); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if err := VerifyPKCS1v15(&priv.PublicKey, crypto.SHA256, digest[:], sig); err != nil {
		t.Fatalf("valid signature rejected by crypto/rsa-compatible form: %v", err)
	}
}

func TestVerifyRejectsTrailingGarbage(t *testing.T) {
	priv := mustGenerateKey(t, 2048)
	digest := sha256.Sum256([]byte("message with trailing garbage"))

	em := make([]byte, priv.PublicKey.Size())
	em[0] = 0x00
	em[1] = 0x01
	garbageLen := 180
	psLen := len(em) - 3 - len(sha256DigestInfoPrefix) - sha256.Size - garbageLen
	for i := 2; i < 2+psLen; i++ {
		em[i] = 0xff
	}
	em[2+psLen] = 0x00
	copy(em[3+psLen:], sha256DigestInfoPrefix)
	copy(em[3+psLen+len(sha256DigestInfoPrefix):], digest[:])
	for i := len(em) - garbageLen; i < len(em); i++ {
		em[i] = 0xa5
	}

	sig := rawPrivateOperation(t, priv, em)
	if err := VerifySHA256PKCS1v15(&priv.PublicKey, digest[:], sig); !errors.Is(err, ErrVerification) {
		t.Fatalf("signature with trailing garbage returned %v, want ErrVerification", err)
	}
}

func TestVerifyRejectsCubeRootForgeryAtExponentThree(t *testing.T) {
	priv := mustGenerateKey(t, 2048)
	digest := sha256.Sum256([]byte("cube-root forgery"))
	pub := &rsa.PublicKey{N: priv.N, E: 3}

	em := make([]byte, pub.Size())
	em[0] = 0x00
	em[1] = 0x01
	garbageLen := 180
	psLen := len(em) - 3 - len(sha256DigestInfoPrefix) - sha256.Size - garbageLen
	for i := 2; i < 2+psLen; i++ {
		em[i] = 0xff
	}
	em[2+psLen] = 0x00
	copy(em[3+psLen:], sha256DigestInfoPrefix)
	copy(em[3+psLen+len(sha256DigestInfoPrefix):], digest[:])

	root := cubeRootCeil(new(big.Int).SetBytes(em))
	sig := make([]byte, pub.Size())
	root.FillBytes(sig)

	forged := new(big.Int).Exp(root, big.NewInt(3), pub.N)
	forgedEM := make([]byte, pub.Size())
	forged.FillBytes(forgedEM)

	if !equalThroughDigest(forgedEM, em, 3+psLen+len(sha256DigestInfoPrefix)+sha256.Size) {
		t.Fatal("test forgery did not preserve the parsed DigestInfo prefix")
	}
	if err := verifyEncodedMessage(len(em), digest[:], forgedEM); !errors.Is(err, ErrVerification) {
		t.Fatalf("strict parser accepted cube-root forgery: %v", err)
	}
	if err := VerifySHA256PKCS1v15(pub, digest[:], sig); !errors.Is(err, ErrInvalidPublicKey) {
		t.Fatalf("e=3 public key returned %v, want ErrInvalidPublicKey", err)
	}
}

func TestVerifyRejectsShortDigestAndShortExponent(t *testing.T) {
	priv := mustGenerateKey(t, 1024)

	if err := VerifySHA256PKCS1v15(&priv.PublicKey, make([]byte, sha256.Size-1), make([]byte, priv.PublicKey.Size())); !errors.Is(err, ErrInvalidDigest) {
		t.Fatalf("short digest returned %v, want ErrInvalidDigest", err)
	}
	if err := VerifySHA256PKCS1v15(&rsa.PublicKey{N: priv.N, E: 65537 - 1}, make([]byte, sha256.Size), make([]byte, priv.PublicKey.Size())); !errors.Is(err, ErrInvalidPublicKey) {
		t.Fatalf("low exponent returned %v, want ErrInvalidPublicKey", err)
	}
}

func mustGenerateKey(t *testing.T, bits int) *rsa.PrivateKey {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return priv
}

func signDigest(t *testing.T, priv *rsa.PrivateKey, digest []byte) []byte {
	t.Helper()
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, digest)
	if err != nil {
		t.Fatalf("sign digest: %v", err)
	}
	return sig
}

func rawPrivateOperation(t *testing.T, priv *rsa.PrivateKey, em []byte) []byte {
	t.Helper()
	m := new(big.Int).SetBytes(em)
	s := new(big.Int).Exp(m, priv.D, priv.N)
	sig := make([]byte, priv.PublicKey.Size())
	s.FillBytes(sig)
	return sig
}

func cubeRootCeil(n *big.Int) *big.Int {
	if n.Sign() == 0 {
		return new(big.Int)
	}
	low := new(big.Int)
	high := new(big.Int).Lsh(big.NewInt(1), uint(n.BitLen()/3+1))
	for low.Cmp(high) < 0 {
		mid := new(big.Int).Add(low, high)
		mid.Rsh(mid, 1)
		cube := new(big.Int).Exp(mid, big.NewInt(3), nil)
		if cube.Cmp(n) < 0 {
			low = new(big.Int).Add(mid, big.NewInt(1))
		} else {
			high = mid
		}
	}
	return low
}

func equalThroughDigest(a, b []byte, end int) bool {
	if len(a) < end || len(b) < end {
		return false
	}
	for i := 0; i < end; i++ {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
