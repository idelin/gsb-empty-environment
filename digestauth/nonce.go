package digestauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"time"
)

// Nonce wire layout (fixed size, base64url-encoded without padding):
//
//	version  1 byte
//	expiry   8 bytes, big-endian Unix nanoseconds
//	policy  16 bytes, keyed tag of the normalized Policy
//	random  16 bytes
//	mac     32 bytes, HMAC-SHA256 over all preceding bytes
const (
	nonceVersion byte = 0x01
	nonceLen          = 1 + 8 + 16 + 16 + 32
)

// canonicalPolicy serializes the policy fields that a nonce is bound to. TTL is
// deliberately excluded: the sealed expiry carries the validity period.
func canonicalPolicy(p Policy) []byte {
	buf := make([]byte, 0, len(p.Realm)+len(p.QOP)+len(p.Algorithm)+16)
	putField := func(field string) {
		var lenBuf [binary.MaxVarintLen64]byte
		n := binary.PutUvarint(lenBuf[:], uint64(len(field)))
		buf = append(buf, lenBuf[:n]...)
		buf = append(buf, field...)
	}
	putField(p.Realm)
	putField(p.QOP)
	putField(p.Algorithm)

	var numBuf [4]byte
	binary.BigEndian.PutUint32(numBuf[:], p.MaxCount)
	buf = append(buf, numBuf[:]...)
	var winBuf [4]byte
	binary.BigEndian.PutUint32(winBuf[:], uint32(p.Window))
	buf = append(buf, winBuf[:]...)
	return buf
}

// policyTag derives a keyed 16-byte tag of the normalized policy.
func policyTag(secret []byte, p Policy) [16]byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(canonicalPolicy(p.normalized()))
	sum := mac.Sum(nil)
	var tag [16]byte
	copy(tag[:], sum)
	return tag
}

func nonceMAC(secret, payload []byte) [32]byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	var out [32]byte
	copy(out[:], mac.Sum(nil))
	return out
}

// sealNonce builds and signs a nonce.
func sealNonce(secret []byte, tag [16]byte, expires time.Time) (string, error) {
	buf := make([]byte, 0, nonceLen)
	buf = append(buf, nonceVersion)

	var expBuf [8]byte
	binary.BigEndian.PutUint64(expBuf[:], uint64(expires.UnixNano()))
	buf = append(buf, expBuf[:]...)
	buf = append(buf, tag[:]...)

	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	buf = append(buf, random[:]...)

	mac := nonceMAC(secret, buf)
	buf = append(buf, mac[:]...)

	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// openNonce decodes and verifies a nonce, returning its sealed expiry and
// policy tag. Any tampering is reported as an error.
func openNonce(secret []byte, nonce string) (time.Time, [16]byte, error) {
	var tag [16]byte

	raw, err := base64.RawURLEncoding.DecodeString(nonce)
	if err != nil || len(raw) != nonceLen {
		return time.Time{}, tag, errors.New("digestauth: nonce has an invalid encoding")
	}

	payload, gotMAC := raw[:nonceLen-32], raw[nonceLen-32:]
	wantMAC := nonceMAC(secret, payload)
	if !hmac.Equal(gotMAC, wantMAC[:]) {
		return time.Time{}, tag, errors.New("digestauth: nonce authentication failed")
	}

	if raw[0] != nonceVersion {
		return time.Time{}, tag, errors.New("digestauth: unsupported nonce version")
	}

	expires := time.Unix(0, int64(binary.BigEndian.Uint64(raw[1:9])))
	copy(tag[:], raw[9:25])
	return expires, tag, nil
}
