package netsec

import (
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	kdfDomain        = "example.org/netsec/q0130/COSE_KDF/v1"
	keyDerivationLen = 32
)

// PartyInfo is optional structured party information. ID identifies the
// party and Nonce may carry a protocol-specific freshness value. The fields
// are encoded with explicit boundaries before the ordered party slot is
// placed into the KDF context.
type PartyInfo struct {
	ID    []byte
	Nonce []byte
}

// KDFContext mirrors the fields of a COSE KDF context. PartyUInfo is always
// the sender and PartyVInfo is always the recipient.
type KDFContext struct {
	AlgorithmID  int64
	PartyUInfo   []byte
	PartyVInfo   []byte
	SuppPubInfo  []byte
	SuppPrivInfo []byte
}

// DeriveKey derives a 32-byte content-encryption key from a shared secret.
// partyU must be the sender and partyV the recipient. The two values occupy
// distinct, length-delimited KDF fields, so exchanging them produces a
// different key.
func DeriveKey(sharedSecret []byte, partyU, partyV any) ([]byte, error) {
	if len(sharedSecret) == 0 {
		return nil, errors.New("netsec: empty shared secret")
	}
	u, err := partyBytes(partyU)
	if err != nil {
		return nil, fmt.Errorf("netsec: invalid partyU: %w", err)
	}
	v, err := partyBytes(partyV)
	if err != nil {
		return nil, fmt.Errorf("netsec: invalid partyV: %w", err)
	}
	info := partyContext(kdfDomain, u, v)
	return hkdf.Key(sha256.New, sharedSecret, nil, string(info), keyDerivationLen)
}

// DeriveKeyWithContext derives a 32-byte key from a COSE-style KDF context.
// PartyUInfo is the sender and PartyVInfo is the recipient; their order is
// significant.
func DeriveKeyWithContext(sharedSecret []byte, context KDFContext) ([]byte, error) {
	if len(sharedSecret) == 0 {
		return nil, errors.New("netsec: empty shared secret")
	}
	info := encodeKDFContext(context)
	return hkdf.Key(sha256.New, sharedSecret, nil, string(info), keyDerivationLen)
}

func deriveECDHKey(sharedSecret, partyU, partyV, senderPublic, recipientPublic []byte) ([]byte, error) {
	info := partyContext(kdfDomain+"/ECDH", partyU, partyV)
	info = appendField(info, []byte("sender-public"), senderPublic)
	info = appendField(info, []byte("recipient-public"), recipientPublic)
	return hkdf.Key(sha256.New, sharedSecret, nil, string(info), keyDerivationLen)
}

func partyContext(domain string, partyU, partyV []byte) []byte {
	info := appendField(nil, []byte("domain"), []byte(domain))
	info = appendField(info, []byte("U"), partyU)
	info = appendField(info, []byte("V"), partyV)
	return info
}

func encodeKDFContext(context KDFContext) []byte {
	info := appendField(nil, []byte("domain"), []byte(kdfDomain))
	info = appendUint64Field(info, []byte("algorithm"), uint64(context.AlgorithmID))
	info = appendField(info, []byte("U"), context.PartyUInfo)
	info = appendField(info, []byte("V"), context.PartyVInfo)
	info = appendField(info, []byte("suppPub"), context.SuppPubInfo)
	info = appendField(info, []byte("suppPriv"), context.SuppPrivInfo)
	return info
}

func appendUint64Field(dst, tag []byte, value uint64) []byte {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	dst = appendField(dst, tag, encoded[:])
	return dst
}

func appendField(dst, tag, value []byte) []byte {
	dst = appendLength(dst, tag)
	dst = appendLength(dst, value)
	return dst
}

func appendLength(dst, value []byte) []byte {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	dst = append(dst, length[:]...)
	return append(dst, value...)
}

func partyBytes(value any) ([]byte, error) {
	switch party := value.(type) {
	case nil:
		return nil, errors.New("missing party information")
	case string:
		return []byte(party), nil
	case []byte:
		return cloneBytes(party), nil
	case PartyInfo:
		return encodePartyInfo(party), nil
	case *PartyInfo:
		if party == nil {
			return nil, errors.New("nil party information")
		}
		return encodePartyInfo(*party), nil
	case fmt.Stringer:
		return []byte(party.String()), nil
	default:
		return nil, fmt.Errorf("unsupported party information type %T", value)
	}
}

func encodePartyInfo(party PartyInfo) []byte {
	info := appendField(nil, []byte("id"), party.ID)
	info = appendField(info, []byte("nonce"), party.Nonce)
	return info
}
