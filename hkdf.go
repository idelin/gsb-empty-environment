package netsec

import (
	"crypto/hmac"
	"errors"
	"fmt"
	"hash"
)

// HKDFExpand implements the HKDF-Expand step of RFC 5869. It is exported so
// callers can verify the key schedule independently of the state machine.
func HKDFExpand(h func() hash.Hash, secret, info []byte, length int) ([]byte, error) {
	if h == nil {
		return nil, errors.New("netsec: nil hash function")
	}
	if length < 0 {
		return nil, fmt.Errorf("netsec: negative HKDF length %d", length)
	}
	hashLen := h().Size()
	if hashLen <= 0 {
		return nil, errors.New("netsec: hash function has zero output size")
	}
	if length > 255*hashLen {
		return nil, fmt.Errorf("netsec: HKDF length %d exceeds 255*hashLen", length)
	}
	if length == 0 {
		return []byte{}, nil
	}

	n := (length + hashLen - 1) / hashLen
	out := make([]byte, 0, n*hashLen)
	var previous []byte
	for counter := 1; counter <= n; counter++ {
		mac := hmac.New(h, secret)
		_, _ = mac.Write(previous)
		_, _ = mac.Write(info)
		_, _ = mac.Write([]byte{byte(counter)})
		previous = mac.Sum(nil)
		out = append(out, previous...)
	}
	return out[:length], nil
}

// HKDFExpandLabel implements TLS 1.3's HKDF-Expand-Label (RFC 8446, section
// 7.1). context may be nil; label is prefixed with "tls13 " internally.
func HKDFExpandLabel(h func() hash.Hash, secret []byte, label string, context []byte, length int) ([]byte, error) {
	if length < 0 || length > 0xffff {
		return nil, fmt.Errorf("netsec: invalid HKDF-Expand-Label length %d", length)
	}
	label = "tls13 " + label
	if len(label) > 255 {
		return nil, fmt.Errorf("netsec: HKDF-Expand-Label label is %d bytes, want <= 255", len(label))
	}
	if len(context) > 255 {
		return nil, fmt.Errorf("netsec: HKDF-Expand-Label context is %d bytes, want <= 255", len(context))
	}

	info := make([]byte, 0, 2+1+len(label)+1+len(context))
	info = append(info, byte(length>>8), byte(length))
	info = append(info, byte(len(label)))
	info = append(info, label...)
	info = append(info, byte(len(context)))
	info = append(info, context...)
	return HKDFExpand(h, secret, info, length)
}

// NextTrafficSecret returns the successor of a TLS 1.3 traffic secret:
// HKDF-Expand-Label(secret, "traffic upd", "", Hash.Length).
func NextTrafficSecret(h func() hash.Hash, secret []byte) ([]byte, error) {
	if h == nil {
		return nil, errors.New("netsec: nil hash function")
	}
	return HKDFExpandLabel(h, secret, "traffic upd", nil, h().Size())
}
