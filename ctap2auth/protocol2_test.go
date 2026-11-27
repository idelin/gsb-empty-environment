package ctap2auth

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"
)

const (
	goldenPlatformScalar = "c647c6e5b29e0e1a8f1d4c2a0b3e5f7a9d1c2e3f4a5b6c7d8e9f0a1b2c3d4e5f"
	goldenAuthnScalar    = "1f2e3d4c5b6a79887766554433221100ffeeddccbbaa99887766554433221100"
	goldenPlatformPub    = "04bb98e710d4586c632143d0bac9909428d460329eae59bc5905c49afe931884985a36e4833533228759a3a50bc133f931159137a9a175d9e8246407da34984e3d"
	goldenAuthnPub       = "042c13f9b070e430aa8315fd7941b5dfb47f93a6fa9fb586699bd79748edfb0f4f2a56f33ee99f43d434bd39366b1ca4957cf324c569f518352c3241f7d8cc7ccf"
	goldenZ              = "232d760818ab991f0a6898dd3ce774e181e61062a4e632e472ce0f38bf0516fa"
	goldenHMACKey        = "41ba2a77b945ac56ba9f1a2d75222167a6b762553de6cdaf7f3b2dd7bdaa8646"
	goldenAESKey         = "e06a442d15ae81f10d8ad0f4706ab54fdf6f1a79847df15625fb60eb9b46c818"
	goldenMessageMAC     = "a6bea3aef396e153ea99bed808cc587b9dad470d037a5dddfba2089d5c026062"
	goldenCBC            = "000102030405060708090a0b0c0d0e0f8a1429b01b46620713be42f67fb58298"
)

// Full dual-key derivation known-answer test against fixed P-256 scalars.
func TestDeriveKeys_GoldenVector(t *testing.T) {
	platform, err := NewPlatformKeyFromBytes(mustHex(t, goldenPlatformScalar))
	if err != nil {
		t.Fatalf("platform key: %v", err)
	}
	authnPub := mustHex(t, goldenAuthnPub)

	if got := hex.EncodeToString(platform.PublicKey()); got != goldenPlatformPub {
		t.Fatalf("platform pub mismatch\n got=%s\nwant=%s", got, goldenPlatformPub)
	}

	z, err := platform.SharedSecret(authnPub)
	if err != nil {
		t.Fatalf("ecdh: %v", err)
	}
	if hex.EncodeToString(z) != goldenZ {
		t.Fatalf("shared secret mismatch got=%x want=%s", z, goldenZ)
	}

	keys, err := DeriveKeys(z)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if hex.EncodeToString(keys.HMAC) != goldenHMACKey {
		t.Fatalf("hmacKey got=%x want=%s", keys.HMAC, goldenHMACKey)
	}
	if hex.EncodeToString(keys.AES) != goldenAESKey {
		t.Fatalf("aesKey got=%x want=%s", keys.AES, goldenAESKey)
	}
	if bytes.Equal(keys.HMAC, keys.AES) {
		t.Fatal("HMAC and AES keys must be independently derived and distinct")
	}

	mac, err := Authenticate(keys.HMAC, []byte("ctap2 golden vector message"))
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if hex.EncodeToString(mac) != goldenMessageMAC {
		t.Fatalf("mac got=%x want=%s", mac, goldenMessageMAC)
	}
	if len(mac) != macSizeBytes {
		t.Fatalf("Protocol Two MAC must be 32 bytes, got %d", len(mac))
	}
}

func TestECDH_SymmetricAndPointValidation(t *testing.T) {
	platform, _ := NewPlatformKeyFromBytes(mustHex(t, goldenPlatformScalar))
	authn, err := NewPlatformKeyFromBytes(mustHex(t, goldenAuthnScalar))
	if err != nil {
		t.Fatal(err)
	}

	z1, err := platform.SharedSecret(authn.PublicKey())
	if err != nil {
		t.Fatalf("ecdh A->B: %v", err)
	}
	z2, err := authn.SharedSecret(platform.PublicKey())
	if err != nil {
		t.Fatalf("ecdh B->A: %v", err)
	}
	if !bytes.Equal(z1, z2) {
		t.Fatal("ECDH shared secrets must be symmetric")
	}

	bad := [][]byte{
		nil,
		make([]byte, 65),                  // all-zero "point"
		mustHex(t, "05"+"00"),             // wrong prefix / size
		mustHex(t, "04"+repeat("11", 64)), // non-curve point
		mustHex(t, "04"+repeat("ff", 64)), // coordinates beyond field
		mustHex(t, "04"+"0000000000000000000000000000000000000000000000000000000000000000"+"0000000000000000000000000000000000000000000000000000000000000000"), // (0,0)
	}
	for i, point := range bad {
		if err := ValidatePublicPoint(point); err == nil {
			t.Fatalf("case %d: expected invalid point rejection", i)
		}
		if _, err := platform.SharedSecret(point); !errors.Is(err, ErrInvalidPoint) {
			t.Fatalf("case %d: ECDH error = %v, want ErrInvalidPoint", i, err)
		}
	}
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

// A low-order point on the P-256 curve must be rejected by crypto/ecdh.
func TestECDH_LowOrderPointRejected(t *testing.T) {
	platform, err := GeneratePlatformKey()
	if err != nil {
		t.Fatal(err)
	}
	// SEC1 encoding of the small-order point (0,0) is invalid; use a documented
	// invalid curve point to prove the validation gate is armed.
	nonCurve := mustHex(t, "04"+repeat("ab", 64))
	if _, err := platform.SharedSecret(nonCurve); !errors.Is(err, ErrInvalidPoint) {
		t.Fatalf("low-order/non-curve point not rejected: %v", err)
	}
}

func TestVerifyAuth_ProtocolOneDowngradeRejected(t *testing.T) {
	keys := &ProtocolTwoKeys{HMAC: mustHex(t, goldenHMACKey), AES: mustHex(t, goldenAESKey)}
	msg := []byte("clientDataHash")

	full, err := Authenticate(keys.HMAC, msg)
	if err != nil {
		t.Fatal(err)
	}
	truncated := full[:16] // Protocol One pins the tag to the first 16 bytes.

	ok, err := VerifyAuth(keys.HMAC, msg, truncated)
	if ok || !errors.Is(err, ErrProtocolOneDowngrade) {
		t.Fatalf("16-byte tag must be a hard downgrade error, got ok=%v err=%v", ok, err)
	}

	ok, err = VerifyAuth(keys.HMAC, msg, full)
	if !ok || err != nil {
		t.Fatalf("full 32-byte tag must verify, got ok=%v err=%v", ok, err)
	}

	tampered := append([]byte{}, full...)
	tampered[0] ^= 0xff
	if ok, _ := VerifyAuth(keys.HMAC, msg, tampered); ok {
		t.Fatal("tampered tag must not verify")
	}
}

func TestAESCBC_RandomIVAndZeroIVRejection(t *testing.T) {
	key := mustHex(t, goldenAESKey)
	plaintext := []byte("1234")

	blob, err := AES256CBCEncrypt(key, plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if len(blob) != ivSizeBytes+ivSizeBytes { // IV + one padded block
		t.Fatalf("unexpected blob length %d", len(blob))
	}

	got, err := AES256CBCDecrypt(key, blob)
	if err != nil || !bytes.Equal(got, plaintext) {
		t.Fatalf("round trip got=%q err=%v", got, err)
	}

	// Fresh random IV per encryption: two ciphertexts of the same plaintext differ.
	blob2, _ := AES256CBCEncrypt(key, plaintext)
	if bytes.Equal(blob, blob2) {
		t.Fatal("IV must be random; identical ciphertext indicates fixed IV")
	}

	// Decrypting the fixed-IV golden vector is refused because its IV is zero-ish
	// randomness check fails only on all-zero; the golden IV here is non-zero and
	// must instead decrypt correctly as a known-answer check.
	golden := mustHex(t, goldenCBC)
	out, err := AES256CBCDecrypt(key, golden)
	if err != nil || !bytes.Equal(out, plaintext) {
		t.Fatalf("golden CBC decrypt got=%q err=%v", out, err)
	}

	zeroIV := append(make([]byte, ivSizeBytes), golden[ivSizeBytes:]...)
	if _, err := AES256CBCDecrypt(key, zeroIV); !errors.Is(err, ErrInvalidIV) {
		t.Fatalf("all-zero IV must be rejected, got err=%v", err)
	}
}

func TestAESCBC_BadPaddingAndKeyLength(t *testing.T) {
	key := mustHex(t, goldenAESKey)
	blob, _ := AES256CBCEncrypt(key, []byte("hello"))

	garbage := append([]byte{}, blob...)
	garbage[len(garbage)-1] ^= 0xff
	if _, err := AES256CBCDecrypt(key, garbage); !errors.Is(err, ErrBadPadding) {
		t.Fatalf("corrupt padding expected ErrBadPadding, got %v", err)
	}

	if _, err := AES256CBCEncrypt(make([]byte, 16), []byte("x")); !errors.Is(err, ErrInvalidKeyLength) {
		t.Fatalf("16-byte key must be rejected for AES-256")
	}
	if _, err := AES256CBCDecrypt(key, []byte{1, 2, 3}); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("truncated blob must be rejected")
	}
}
