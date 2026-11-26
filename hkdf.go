package tls13

import (
	"crypto"
	"crypto/hmac"
	"encoding/binary"
	"errors"
	"hash"
)

const tls13LabelPrefix = "tls13 "

// HKDFExtract is RFC 5869 HKDF-Extract. A nil salt is equivalent to a string
// of HashLen zero bytes.
func HKDFExtract(newHash func() hash.Hash, salt, inputKeyMaterial []byte) []byte {
	if salt == nil {
		salt = make([]byte, newHash().Size())
	}
	mac := hmac.New(newHash, salt)
	mac.Write(inputKeyMaterial)
	return mac.Sum(nil)
}

// HKDFExpand is RFC 5869 HKDF-Expand.
func HKDFExpand(newHash func() hash.Hash, pseudorandomKey, info []byte, length int) ([]byte, error) {
	hashLen := newHash().Size()
	if length < 0 || length > 255*hashLen {
		return nil, errors.New("tls13: HKDF output length is too long")
	}

	out := make([]byte, 0, length)
	var previous []byte
	var counter byte = 1
	for len(out) < length {
		mac := hmac.New(newHash, pseudorandomKey)
		mac.Write(previous)
		mac.Write(info)
		mac.Write([]byte{counter})
		previous = mac.Sum(nil)
		out = append(out, previous...)
		counter++
	}
	return out[:length], nil
}

// MustHKDFExpand is HKDFExpand for values whose lengths are fixed by a
// supported cipher suite.
func MustHKDFExpand(newHash func() hash.Hash, prk, info []byte, length int) []byte {
	out, err := HKDFExpand(newHash, prk, info, length)
	if err != nil {
		panic(err)
	}
	return out
}

// ExpandLabel implements TLS 1.3 HKDF-Expand-Label exactly. Labels and
// contexts are supplied without the protocol's "tls13 " prefix.
func ExpandLabel(newHash func() hash.Hash, secret []byte, label string, context []byte, length int) ([]byte, error) {
	fullLabel := tls13LabelPrefix + label
	if len(fullLabel) > 255 {
		return nil, errors.New("tls13: HKDF label is too long")
	}
	if len(context) > 255 {
		return nil, errors.New("tls13: HKDF context is too long")
	}
	if length > 0xffff {
		return nil, errors.New("tls13: HKDF output length is too long")
	}

	info := make([]byte, 0, 2+1+len(fullLabel)+1+len(context))
	info = binary.BigEndian.AppendUint16(info, uint16(length))
	info = append(info, byte(len(fullLabel)))
	info = append(info, fullLabel...)
	info = append(info, byte(len(context)))
	info = append(info, context...)
	return HKDFExpand(newHash, secret, info, length)
}

// HKDFExpandLabel is the RFC 8446 spelling of ExpandLabel. hash may be a
// CipherSuite, crypto.Hash, or func() hash.Hash. Invalid lengths are
// programming errors for the fixed TLS 1.3 derivations and panic.
func HKDFExpandLabel(hashOrSuite any, secret []byte, label string, context []byte, length int) []byte {
	newHash, _, err := resolveHash(hashOrSuite)
	if err != nil {
		panic(err)
	}
	out, err := ExpandLabel(newHash, secret, label, context, length)
	if err != nil {
		panic(err)
	}
	return out
}

// ExpandLabel expands using this cipher suite's hash.
func (c CipherSuite) ExpandLabel(secret []byte, label string, context []byte, length int) []byte {
	return HKDFExpandLabel(c.hashFunc(), secret, label, context, length)
}

// HKDFExpandLabelSuite expands using a cipher suite value.
func HKDFExpandLabelSuite(cs CipherSuite, secret []byte, label string, context []byte, length int) []byte {
	return cs.ExpandLabel(secret, label, context, length)
}

func mustExpandLabel(newHash func() hash.Hash, secret []byte, label string, context []byte, length int) []byte {
	out, err := ExpandLabel(newHash, secret, label, context, length)
	if err != nil {
		panic(err)
	}
	return out
}

// DeriveSecretChecked implements RFC 8446 Derive-Secret and reports invalid
// argument lengths. hashOrSuite may be a CipherSuite, crypto.Hash, or
// func() hash.Hash. transcriptHash must be Transcript-Hash(Messages), not the
// raw transcript messages.
func DeriveSecretChecked(hashOrSuite any, secret []byte, label string, transcriptHash []byte) ([]byte, error) {
	newHash, hashSize, err := resolveHash(hashOrSuite)
	if err != nil {
		return nil, err
	}
	if len(transcriptHash) != hashSize {
		return nil, errors.New("tls13: transcript hash length does not match cipher suite")
	}
	return ExpandLabel(newHash, secret, label, transcriptHash, hashSize)
}

// DeriveSecret is the RFC-shaped, infallible form of Derive-Secret. It panics
// if transcriptHash does not match the suite hash size.
func DeriveSecret(hashOrSuite any, secret []byte, label string, transcriptHash []byte) []byte {
	out, err := DeriveSecretChecked(hashOrSuite, secret, label, transcriptHash)
	if err != nil {
		panic(err)
	}
	return out
}

func resolveHash(hashOrSuite any) (func() hash.Hash, int, error) {
	switch value := hashOrSuite.(type) {
	case CipherSuite:
		return value.hashFunc(), value.HashSize(), nil
	case *CipherSuite:
		if value == nil {
			return nil, 0, errors.New("tls13: nil cipher suite")
		}
		return value.hashFunc(), value.HashSize(), nil
	case func() hash.Hash:
		return value, value().Size(), nil
	case crypto.Hash:
		if !value.Available() {
			return nil, 0, errors.New("tls13: hash function unavailable")
		}
		return value.New, value.Size(), nil
	default:
		return nil, 0, errors.New("tls13: expected CipherSuite, crypto.Hash, or func() hash.Hash")
	}
}

func mustDeriveSecret(cs CipherSuite, secret []byte, label string, transcriptHash []byte) []byte {
	return DeriveSecret(cs, secret, label, transcriptHash)
}

// DeriveSecretMessages hashes concatenated handshake messages and then calls
// DeriveSecret.
func DeriveSecretMessages(cs CipherSuite, secret []byte, label string, messages ...[]byte) ([]byte, error) {
	h := cs.NewHash()
	for _, message := range messages {
		h.Write(message)
	}
	return DeriveSecretChecked(cs, secret, label, h.Sum(nil))
}
