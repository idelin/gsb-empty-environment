package hsm

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"
)

// Mechanism names a PKCS#11 mechanism supported by the model.
type Mechanism string

const (
	AESKeyWrap Mechanism = "CKM_AES_KEY_WRAP"
	AESCBC     Mechanism = "CKM_AES_CBC_PAD"
	AESGCM     Mechanism = "CKM_AES_GCM"
	HKDFSHA256 Mechanism = "CKM_HKDF_SHA256"

	// Short aliases for callers that prefer mechanism names rather than
	// CKM constants.
	AESKW           = AESKeyWrap
	MechanismAESKW  = AESKeyWrap
	MechanismAESCBC = AESCBC
	MechanismAESGCM = AESGCM
)

// ParseMechanism accepts both CKM_* names and common short spellings.
func ParseMechanism(value string) (Mechanism, error) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case string(AESKeyWrap), "AES-KW", "AESKW", "AES_KEY_WRAP":
		return AESKeyWrap, nil
	case string(AESCBC), "AES-CBC", "AESCBC", "AES_CBC":
		return AESCBC, nil
	case string(AESGCM), "AES-GCM", "AESGCM", "AES_GCM":
		return AESGCM, nil
	case string(HKDFSHA256), "HKDF-SHA256", "HKDF", "HKDF_SHA256":
		return HKDFSHA256, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedMechanism, value)
	}
}

// IsAES reports whether m is one of the modelled AES mechanisms.
func (m Mechanism) IsAES() bool {
	return m == AESKeyWrap || m == AESCBC || m == AESGCM
}

func (m Mechanism) String() string { return string(m) }

var keyWrapIV = [8]byte{0xa6, 0xa6, 0xa6, 0xa6, 0xa6, 0xa6, 0xa6, 0xa6}

// aesKeyWrap implements RFC 3394 AES Key Wrap without padding.
func aesKeyWrap(key, plaintext []byte) ([]byte, error) {
	if len(plaintext) < 16 || len(plaintext)%8 != 0 {
		return nil, fmt.Errorf("%w: AES-KW input must be at least 16 bytes and a multiple of 8", ErrCrypto)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCrypto, err)
	}
	n := len(plaintext) / 8
	a := keyWrapIV
	r := make([][]byte, n+1)
	for i := 1; i <= n; i++ {
		r[i] = append([]byte(nil), plaintext[(i-1)*8:i*8]...)
	}
	buf := make([]byte, 16)
	for j := 0; j < 6; j++ {
		for i := 1; i <= n; i++ {
			copy(buf[:8], a[:])
			copy(buf[8:], r[i])
			block.Encrypt(buf, buf)
			copy(a[:], buf[:8])
			binary.BigEndian.PutUint64(buf[:8], binary.BigEndian.Uint64(a[:])^uint64(n*j+i))
			copy(a[:], buf[:8])
			copy(r[i], buf[8:])
		}
	}
	out := make([]byte, 8+len(plaintext))
	copy(out[:8], a[:])
	for i := 1; i <= n; i++ {
		copy(out[i*8:], r[i])
	}
	return out, nil
}

// aesKeyUnwrap implements RFC 3394 AES Key Unwrap and authenticates the IV.
func aesKeyUnwrap(key, ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < 24 || len(ciphertext)%8 != 0 {
		return nil, fmt.Errorf("%w: AES-KW ciphertext must be at least 24 bytes and a multiple of 8", ErrCrypto)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCrypto, err)
	}
	n := len(ciphertext)/8 - 1
	a := append([]byte(nil), ciphertext[:8]...)
	r := make([][]byte, n+1)
	for i := 1; i <= n; i++ {
		r[i] = append([]byte(nil), ciphertext[i*8:(i+1)*8]...)
	}
	buf := make([]byte, 16)
	for j := 5; j >= 0; j-- {
		for i := n; i >= 1; i-- {
			t := uint64(n*j + i)
			copy(buf[:8], a)
			binary.BigEndian.PutUint64(buf[:8], binary.BigEndian.Uint64(buf[:8])^t)
			copy(buf[8:], r[i])
			block.Decrypt(buf, buf)
			copy(a, buf[:8])
			copy(r[i], buf[8:])
		}
	}
	if !hmac.Equal(a, keyWrapIV[:]) {
		return nil, fmt.Errorf("%w: AES-KW integrity check failed", ErrCrypto)
	}
	out := make([]byte, 0, n*8)
	for i := 1; i <= n; i++ {
		out = append(out, r[i]...)
	}
	return out, nil
}

func aesCBCEncrypt(key, plaintext, iv []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCrypto, err)
	}
	if len(iv) != aes.BlockSize {
		return nil, fmt.Errorf("%w: AES-CBC IV must be %d bytes", ErrCrypto, aes.BlockSize)
	}
	pad := aes.BlockSize - len(plaintext)%aes.BlockSize
	data := make([]byte, len(plaintext)+pad)
	copy(data, plaintext)
	for i := len(plaintext); i < len(data); i++ {
		data[i] = byte(pad)
	}
	out := make([]byte, aes.BlockSize+len(data))
	copy(out, iv)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out[aes.BlockSize:], data)
	return out, nil
}

func aesCBCDecrypt(key, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCrypto, err)
	}
	if len(ciphertext) < 2*aes.BlockSize || (len(ciphertext)-aes.BlockSize)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("%w: invalid AES-CBC ciphertext length", ErrCrypto)
	}
	iv := ciphertext[:aes.BlockSize]
	data := append([]byte(nil), ciphertext[aes.BlockSize:]...)
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(data, data)
	pad := int(data[len(data)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(data) {
		return nil, fmt.Errorf("%w: invalid AES-CBC padding", ErrCrypto)
	}
	if !bytes.Equal(data[len(data)-pad:], bytes.Repeat([]byte{byte(pad)}, pad)) {
		return nil, fmt.Errorf("%w: invalid AES-CBC padding", ErrCrypto)
	}
	return data[:len(data)-pad], nil
}

func aesGCMEncrypt(key, plaintext, nonce []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCrypto, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCrypto, err)
	}
	if len(nonce) != aead.NonceSize() {
		return nil, fmt.Errorf("%w: AES-GCM nonce must be %d bytes", ErrCrypto, aead.NonceSize())
	}
	out := make([]byte, len(nonce), len(nonce)+len(plaintext)+aead.Overhead())
	copy(out, nonce)
	out = aead.Seal(out, nonce, plaintext, nil)
	return out, nil
}

func aesGCMDecrypt(key, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCrypto, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCrypto, err)
	}
	if len(ciphertext) < aead.NonceSize()+aead.Overhead() {
		return nil, fmt.Errorf("%w: invalid AES-GCM ciphertext length", ErrCrypto)
	}
	nonce := ciphertext[:aead.NonceSize()]
	plaintext, err := aead.Open(nil, nonce, ciphertext[aead.NonceSize():], nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCrypto, err)
	}
	return plaintext, nil
}

func encryptWithMechanism(mech Mechanism, key, plaintext, iv []byte) ([]byte, error) {
	switch mech {
	case AESKeyWrap:
		return aesKeyWrap(key, plaintext)
	case AESCBC:
		return aesCBCEncrypt(key, plaintext, iv)
	case AESGCM:
		return aesGCMEncrypt(key, plaintext, iv)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedMechanism, mech)
	}
}

func decryptWithMechanism(mech Mechanism, key, ciphertext []byte) ([]byte, error) {
	switch mech {
	case AESKeyWrap:
		return aesKeyUnwrap(key, ciphertext)
	case AESCBC:
		return aesCBCDecrypt(key, ciphertext)
	case AESGCM:
		return aesGCMDecrypt(key, ciphertext)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedMechanism, mech)
	}
}

func hkdfSHA256(secret, salt, info []byte, length int) ([]byte, error) {
	if length <= 0 {
		return nil, fmt.Errorf("%w: invalid derived key length %d", ErrCrypto, length)
	}
	if length > 255*sha256.Size {
		return nil, fmt.Errorf("%w: derived key length %d is too large", ErrCrypto, length)
	}
	if len(salt) == 0 {
		salt = make([]byte, sha256.Size)
	}
	extract := hmac.New(sha256.New, salt)
	_, _ = extract.Write(secret)
	prk := extract.Sum(nil)

	out := make([]byte, 0, length)
	var previous []byte
	for counter := byte(1); len(out) < length; counter++ {
		expand := hmac.New(sha256.New, prk)
		_, _ = expand.Write(previous)
		_, _ = expand.Write(info)
		_, _ = expand.Write([]byte{counter})
		previous = expand.Sum(nil)
		out = append(out, previous...)
	}
	return out[:length], nil
}
