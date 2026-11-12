package netsec

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
)

var (
	// ErrPTKDerivation is returned when the inputs to PTK derivation are
	// incomplete or malformed.
	ErrPTKDerivation = errors.New("netsec: PTK derivation failed")

	// ErrMICVerification is returned when an EAPOL-Key MIC does not match.
	ErrMICVerification = errors.New("netsec: EAPOL-Key MIC verification failed")

	// ErrKeyDataDecryption is returned when encrypted Key Data cannot be
	// unwrapped with the KEK.
	ErrKeyDataDecryption = errors.New("netsec: EAPOL-Key data decryption failed")
)

// PTK is the 48-byte pairwise transient key for CCMP (RSN, AES-128-CMAC or
// HMAC-SHA1 MIC). KCK is used for EAPOL MICs, KEK wraps Key Data, and TK
// protects data frames.
type PTK struct {
	KCK []byte
	KEK []byte
	TK  []byte

	// Key is the complete 48-byte PTK in the order KCK || KEK || TK.
	Key []byte
}

// DerivePTK derives the 48-byte pairwise transient key using the
// IEEE 802.11 PRF-384 construction:
//
//	PTK = PRF-384(PMK, "Pairwise key expansion",
//	              min(AA,SPA) || max(AA,SPA) ||
//	              min(ANonce,SNonce) || max(ANonce,SNonce))
func DerivePTK(pmk, anonce, snonce []byte, aa, spa net.HardwareAddr) (*PTK, error) {
	if len(pmk) < 16 {
		return nil, fmt.Errorf("%w: PMK must be at least 16 bytes", ErrPTKDerivation)
	}
	if len(anonce) != 32 || len(snonce) != 32 {
		return nil, fmt.Errorf("%w: ANonce and SNonce must be 32 bytes", ErrPTKDerivation)
	}
	if len(aa) != 6 || len(spa) != 6 {
		return nil, fmt.Errorf("%w: addresses must be 6 bytes", ErrPTKDerivation)
	}

	addr1, addr2 := []byte(aa), []byte(spa)
	if bytes.Compare(addr1, addr2) > 0 {
		addr1, addr2 = addr2, addr1
	}
	nonce1, nonce2 := append([]byte(nil), anonce...), append([]byte(nil), snonce...)
	if bytes.Compare(nonce1, nonce2) > 0 {
		nonce1, nonce2 = nonce2, nonce1
	}

	data := make([]byte, 0, 12+64)
	data = append(data, addr1...)
	data = append(data, addr2...)
	data = append(data, nonce1...)
	data = append(data, nonce2...)

	key := PRF384(pmk, "Pairwise key expansion", data)
	return &PTK{
		KCK: append([]byte(nil), key[0:16]...),
		KEK: append([]byte(nil), key[16:32]...),
		TK:  append([]byte(nil), key[32:48]...),
		Key: append([]byte(nil), key...),
	}, nil
}

// PRF384 implements the 802.11 PRF with a 384-bit output. The label is
// encoded as an ASCII string followed by a NUL byte, matching the reference
// WPA supplicant implementation. The counter appended to each HMAC input is
// the one-byte value i.
func PRF384(key []byte, label string, data []byte) []byte {
	const outLen = 48
	out := make([]byte, outLen)

	labelLen := len(label) + 1
	buf := make([]byte, labelLen+len(data)+1)
	copy(buf, label)
	// buf[labelLen-1] is the NUL terminator and remains zero.
	copy(buf[labelLen:], data)

	for i := 0; i*20 < outLen; i++ {
		buf[len(buf)-1] = byte(i)
		mac := hmac.New(sha1.New, key)
		_, _ = mac.Write(buf)
		sum := mac.Sum(nil)
		copy(out[i*20:], sum)
	}
	return out
}

// ComputeEAPOLKeyMIC computes the Key MIC over the EAPOL-Key frame with its
// MIC field replaced by zeroes. The algorithm is selected by the key
// descriptor version:
//
//	version 1: HMAC-MD5 (WPA/TKIP)
//	version 2: HMAC-SHA1-128 (RSN)
//	version 3: AES-128-CMAC (RSN)
func ComputeEAPOLKeyMIC(frame, kck []byte) ([]byte, error) {
	if len(frame) < EAPOLKeyHeaderLen {
		return nil, fmt.Errorf("%w: frame too short for MIC", ErrInvalidEAPOLKeyFrame)
	}
	if frame[1] != EAPOLTypeKey {
		return nil, fmt.Errorf("%w: not an EAPOL-Key frame", ErrInvalidEAPOLKeyFrame)
	}

	version := int(binary.BigEndian.Uint16(frame[5:7]) & KeyInfoVersionMask)
	body := make([]byte, len(frame))
	copy(body, frame)
	for i := EAPOLKeyMICOffset; i < EAPOLKeyMICOffset+16; i++ {
		body[i] = 0
	}

	var mic []byte
	switch version {
	case KeyDescriptorVersionHMACMD5RC4:
		if len(kck) < 16 {
			return nil, fmt.Errorf("%w: KCK too short", ErrMICVerification)
		}
		mac := hmac.New(md5.New, kck)
		_, _ = mac.Write(body)
		mic = mac.Sum(nil)
	case KeyDescriptorVersionHMACSHA1AES:
		if len(kck) < 16 {
			return nil, fmt.Errorf("%w: KCK too short", ErrMICVerification)
		}
		mac := hmac.New(sha1.New, kck)
		_, _ = mac.Write(body)
		mic = mac.Sum(nil)[:16]
	case KeyDescriptorVersionAES128CMAC:
		cmac, err := AES128CMAC(kck, body)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrMICVerification, err)
		}
		mic = cmac
	default:
		return nil, fmt.Errorf("%w: unsupported descriptor version %d", ErrMICVerification, version)
	}

	if len(mic) < 16 {
		return nil, fmt.Errorf("%w: MIC algorithm produced short output", ErrMICVerification)
	}
	return append([]byte(nil), mic[:16]...), nil
}

// VerifyEAPOLKeyMIC returns nil only when the frame's MIC is valid under kck.
// The comparison is constant-time.
func VerifyEAPOLKeyMIC(frame, kck []byte) error {
	expected, err := ComputeEAPOLKeyMIC(frame, kck)
	if err != nil {
		return err
	}
	if len(frame) < EAPOLKeyMICOffset+16 {
		return fmt.Errorf("%w: missing MIC field", ErrMICVerification)
	}
	if subtle.ConstantTimeCompare(expected, frame[EAPOLKeyMICOffset:EAPOLKeyMICOffset+16]) != 1 {
		return ErrMICVerification
	}
	return nil
}

// ComputeMIC is an alias for ComputeEAPOLKeyMIC.
func ComputeMIC(frame, kck []byte) ([]byte, error) {
	return ComputeEAPOLKeyMIC(frame, kck)
}

// VerifyMIC is an alias for VerifyEAPOLKeyMIC.
func VerifyMIC(frame, kck []byte) error {
	return VerifyEAPOLKeyMIC(frame, kck)
}

// AES128CMAC computes RFC 4493 AES-CMAC over msg using key.
func AES128CMAC(key, msg []byte) ([]byte, error) {
	if len(key) != 16 {
		return nil, fmt.Errorf("netsec: AES-CMAC key must be 16 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	k1 := cmacSubkey(block, make([]byte, aes.BlockSize))
	k2 := cmacDouble(k1)

	n := (len(msg) + aes.BlockSize - 1) / aes.BlockSize
	if n == 0 {
		n = 1
	}

	last := make([]byte, aes.BlockSize)
	if len(msg)%aes.BlockSize == 0 && len(msg) > 0 {
		copy(last, msg[(n-1)*aes.BlockSize:])
		xorBytes(last, k1)
	} else {
		if len(msg) > 0 {
			copy(last, msg[(n-1)*aes.BlockSize:])
		}
		last[len(msg)%aes.BlockSize] = 0x80
		xorBytes(last, k2)
	}

	x := make([]byte, aes.BlockSize)
	for i := 0; i < n-1; i++ {
		for j := 0; j < aes.BlockSize; j++ {
			x[j] ^= msg[i*aes.BlockSize+j]
		}
		block.Encrypt(x, x)
	}
	for j := 0; j < aes.BlockSize; j++ {
		x[j] ^= last[j]
	}
	block.Encrypt(x, x)
	return x, nil
}

func cmacSubkey(block cipher.Block, zero []byte) []byte {
	out := make([]byte, aes.BlockSize)
	block.Encrypt(out, zero)
	return cmacDouble(out)
}

func cmacDouble(in []byte) []byte {
	out := make([]byte, len(in))
	carry := byte(0)
	for i := len(in) - 1; i >= 0; i-- {
		out[i] = in[i]<<1 | carry
		carry = in[i] >> 7
	}
	if carry != 0 {
		out[len(out)-1] ^= 0x87
	}
	return out
}

func xorBytes(dst, src []byte) {
	for i := 0; i < len(dst) && i < len(src); i++ {
		dst[i] ^= src[i]
	}
}

const aesKeyWrapIV uint64 = 0xA6A6A6A6A6A6A6A6

// AESKeyWrap implements RFC 3394 AES key wrap using a 128-bit KEK.
func AESKeyWrap(kek, plaintext []byte) ([]byte, error) {
	if len(kek) != 16 {
		return nil, fmt.Errorf("%w: KEK must be 16 bytes", ErrKeyDataDecryption)
	}
	if len(plaintext) < 16 || len(plaintext)%8 != 0 {
		return nil, fmt.Errorf("%w: key data must be a multiple of 8 bytes and at least 16", ErrKeyDataDecryption)
	}
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, err
	}

	n := len(plaintext) / 8
	a := make([]byte, 8)
	binary.BigEndian.PutUint64(a, aesKeyWrapIV)
	r := append([]byte(nil), plaintext...)
	buf := make([]byte, 16)

	for j := 0; j < 6; j++ {
		for i := 1; i <= n; i++ {
			copy(buf[:8], a)
			copy(buf[8:], r[(i-1)*8:i*8])
			block.Encrypt(buf, buf)
			binary.BigEndian.PutUint64(a, binary.BigEndian.Uint64(buf[:8])^uint64(n*j+i))
			copy(r[(i-1)*8:i*8], buf[8:])
		}
	}

	out := make([]byte, 8+n*8)
	copy(out[:8], a)
	copy(out[8:], r)
	return out, nil
}

// AESKeyUnwrap implements RFC 3394 AES key unwrap using a 128-bit KEK.
func AESKeyUnwrap(kek, ciphertext []byte) ([]byte, error) {
	if len(kek) != 16 {
		return nil, fmt.Errorf("%w: KEK must be 16 bytes", ErrKeyDataDecryption)
	}
	if len(ciphertext) < 24 || len(ciphertext)%8 != 0 {
		return nil, fmt.Errorf("%w: wrapped key data must be a multiple of 8 bytes and at least 24", ErrKeyDataDecryption)
	}
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, err
	}

	n := (len(ciphertext) - 8) / 8
	a := append([]byte(nil), ciphertext[:8]...)
	r := append([]byte(nil), ciphertext[8:]...)
	buf := make([]byte, 16)

	for j := 5; j >= 0; j-- {
		for i := n; i >= 1; i-- {
			copy(buf[:8], a)
			binary.BigEndian.PutUint64(buf[:8], binary.BigEndian.Uint64(a)^uint64(n*j+i))
			copy(buf[8:], r[(i-1)*8:i*8])
			block.Decrypt(buf, buf)
			copy(a, buf[:8])
			copy(r[(i-1)*8:i*8], buf[8:])
		}
	}

	if binary.BigEndian.Uint64(a) != aesKeyWrapIV {
		return nil, fmt.Errorf("%w: integrity check failed", ErrKeyDataDecryption)
	}
	return r, nil
}

// DecryptKeyData unwraps encrypted Key Data when the Encrypted Key Data bit
// is set. It must only be called after the EAPOL-Key MIC has been verified.
func DecryptKeyData(frame *EAPOLKeyFrame, kek []byte) ([]byte, error) {
	if frame == nil {
		return nil, fmt.Errorf("%w: nil frame", ErrInvalidEAPOLKeyFrame)
	}
	if frame.KeyInfo&KeyInfoEncryptedKeyData == 0 {
		return append([]byte(nil), frame.KeyData...), nil
	}
	if len(frame.KeyData) == 0 {
		return nil, nil
	}
	plaintext, err := AESKeyUnwrap(kek, frame.KeyData)
	if err != nil {
		return nil, err
	}
	return plaintext, nil
}

// UnwrapKeyData is an alias for AESKeyUnwrap.
func UnwrapKeyData(kek, ciphertext []byte) ([]byte, error) {
	return AESKeyUnwrap(kek, ciphertext)
}
