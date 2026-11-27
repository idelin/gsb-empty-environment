package ctap2auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
)

// CTAP2 PIN/UV Auth Protocol Two derivation labels and parameters
// (FIDO CTAP §5.5.4 "PIN/UV Auth Protocol Two").
const (
	ProtocolVersion uint32 = 2
	hmacKeyInfo            = "CTAP2 HMAC key"
	aesKeyInfo             = "CTAP2 AES key"
	keySizeBytes           = 32
	ivSizeBytes            = aes.BlockSize // 16
	macSizeBytes           = sha256.Size   // full 32-byte HMAC, never truncated
)

// ProtocolTwoKeys holds the two independently derived symmetric keys. Using a
// single key for both confidentiality and integrity is explicitly avoided: the
// HMAC key and AES key come from separate HKDF info strings.
type ProtocolTwoKeys struct {
	HMAC []byte
	AES  []byte
}

// zeroSalt is the fixed 32-byte all-zero HKDF salt mandated by CTAP2.
var zeroSalt = make([]byte, keySizeBytes)

// DeriveKeys derives the Protocol Two HMAC and AES keys from the ECDH secret Z.
//
//	hmacKey = HKDF-SHA-256(Z, salt=0x00*32, info="CTAP2 HMAC key", L=32)
//	aesKey  = HKDF-SHA-256(Z, salt=0x00*32, info="CTAP2 AES key",  L=32)
func DeriveKeys(sharedSecret []byte) (*ProtocolTwoKeys, error) {
	if len(sharedSecret) != keySizeBytes {
		return nil, ErrInvalidSharedSecret
	}
	hmacKey, err := hkdfSHA256(sharedSecret, zeroSalt, []byte(hmacKeyInfo), keySizeBytes)
	if err != nil {
		return nil, err
	}
	aesKey, err := hkdfSHA256(sharedSecret, zeroSalt, []byte(aesKeyInfo), keySizeBytes)
	if err != nil {
		return nil, err
	}
	return &ProtocolTwoKeys{HMAC: hmacKey, AES: aesKey}, nil
}

// pkcs7Pad appends PKCS#7 padding (block size is always 16 for AES).
func pkcs7Pad(plaintext []byte) []byte {
	padLen := ivSizeBytes - len(plaintext)%ivSizeBytes
	out := make([]byte, len(plaintext)+padLen)
	copy(out, plaintext)
	for i := len(plaintext); i < len(out); i++ {
		out[i] = byte(padLen)
	}
	return out
}

// pkcs7Unpad validates and strips PKCS#7 padding, rejecting malformed padding
// with an error instead of leaking length/padding oracles.
func pkcs7Unpad(src []byte) ([]byte, error) {
	if len(src) == 0 || len(src)%ivSizeBytes != 0 {
		return nil, ErrInvalidCiphertext
	}
	padLen := int(src[len(src)-1])
	if padLen == 0 || padLen > ivSizeBytes || padLen > len(src) {
		return nil, ErrBadPadding
	}
	for i := len(src) - padLen; i < len(src); i++ {
		if src[i] != byte(padLen) {
			return nil, ErrBadPadding
		}
	}
	return src[:len(src)-padLen], nil
}

func isAllZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

// AES256CBCEncrypt performs AES-256-CBC with a freshly generated 16-byte random
// IV. The IV is prepended to the ciphertext: out = IV || ciphertext. A random
// IV is mandatory; an accidentally all-zero IV is rejected.
func AES256CBCEncrypt(aesKey, plaintext []byte) ([]byte, error) {
	if len(aesKey) != keySizeBytes {
		return nil, ErrInvalidKeyLength
	}
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, err
	}

	iv := make([]byte, ivSizeBytes)
	if _, err := rand.Read(iv); err != nil {
		return nil, err
	}
	if isAllZero(iv) {
		return nil, ErrInvalidIV
	}

	padded := pkcs7Pad(plaintext)
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)

	out := make([]byte, ivSizeBytes+len(ciphertext))
	copy(out[:ivSizeBytes], iv)
	copy(out[ivSizeBytes:], ciphertext)
	return out, nil
}

// AES256CBCDecrypt reverses AES256CBCEncrypt. It enforces a 16-byte IV and
// hard-rejects an all-zero IV (the CBC fixed-IV / ECB-equivalent weakness), then
// verifies block alignment and PKCS#7 padding.
func AES256CBCDecrypt(aesKey, ivAndCiphertext []byte) ([]byte, error) {
	if len(aesKey) != keySizeBytes {
		return nil, ErrInvalidKeyLength
	}
	if len(ivAndCiphertext) < ivSizeBytes {
		return nil, ErrInvalidCiphertext
	}
	iv := ivAndCiphertext[:ivSizeBytes]
	ciphertext := ivAndCiphertext[ivSizeBytes:]
	if len(ciphertext) == 0 || len(ciphertext)%ivSizeBytes != 0 {
		return nil, ErrInvalidCiphertext
	}
	if isAllZero(iv) {
		return nil, ErrInvalidIV
	}

	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, err
	}
	plaintextPadded := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintextPadded, ciphertext)
	return pkcs7Unpad(plaintextPadded)
}

// Authenticate computes the full 32-byte HMAC-SHA-256 over message using the
// Protocol Two HMAC key. Protocol Two never truncates the tag.
func Authenticate(hmacKey, message []byte) ([]byte, error) {
	if len(hmacKey) != keySizeBytes {
		return nil, ErrInvalidKeyLength
	}
	mac := hmac.New(sha256.New, hmacKey)
	mac.Write(message)
	return mac.Sum(nil), nil
}

// VerifyAuth verifies a pinUvAuthParam. It hard-fails on a 16-byte tag, which is
// the truncated HMAC of PIN/UV Auth Protocol One, so a downgrade cannot be
// smuggled through the Protocol Two verifier. Only the full 32-byte tag is
// accepted and compared in constant time.
func VerifyAuth(hmacKey, message, authParam []byte) (bool, error) {
	switch {
	case len(authParam) == 16:
		return false, ErrProtocolOneDowngrade
	case len(authParam) != macSizeBytes:
		return false, ErrTruncatedMAC
	}
	expected, err := Authenticate(hmacKey, message)
	if err != nil {
		return false, err
	}
	return hmac.Equal(expected, authParam), nil
}
