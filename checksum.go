package netsec

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"fmt"
	"hash"
)

// DefaultChecksum implements the keyed-hash signature types used by the
// supported PAC signature algorithms.
//
// PAC signatures carry a Kerberos checksum type in their PAC_SIGNATURE_DATA.
// The following types are implemented:
//
//	7  hmac-md5 (RC4-HMAC profile, RFC 4757)
//	9  hmac-sha1
//	10 hmac-sha1-96
//	12 hmac-sha2-256
//	13 hmac-sha2-384
//	14 hmac-sha2-512
//	15 hmac-sha1-96-aes128
//	16 hmac-sha1-96-aes256
//	19 hmac-sha256-128-aes128
//	20 hmac-sha384-192-aes256
func DefaultChecksum(signatureType uint32, key, data []byte, usage uint32) ([]byte, error) {
	switch signatureType {
	case 7:
		return rc4HMACChecksum(key, data, usage)
	case 9:
		return hmacBytes(sha1.New, key, data), nil
	case 10:
		return truncate(hmacBytes(sha1.New, key, data), 12), nil
	case 12:
		return hmacBytes(sha256.New, key, data), nil
	case 13:
		return hmacBytes(sha512.New384, key, data), nil
	case 14:
		return hmacBytes(sha512.New, key, data), nil
	case 15:
		kc := deriveHMACSHA1AES128(key, checksumConstant(usage))
		return checksumWithDerivedKey(sha1.New, kc, data, 12), nil
	case 16:
		kc := deriveHMACSHA1AES256(key, checksumConstant(usage))
		return checksumWithDerivedKey(sha1.New, kc, data, 12), nil
	case 19:
		kc := deriveRFC8009Key(sha256.New, key, checksumConstant(usage), 128)
		return checksumWithDerivedKey(sha256.New, kc, data, 16), nil
	case 20:
		kc := deriveRFC8009Key(sha512.New384, key, checksumConstant(usage), 192)
		return checksumWithDerivedKey(sha512.New384, kc, data, 24), nil
	default:
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedChecksum, signatureType)
	}
}

// SignatureSize returns the required signature length for a supported
// checksum type.
func SignatureSize(signatureType uint32) (int, error) {
	switch signatureType {
	case 7:
		return md5.Size, nil
	case 9:
		return sha1.Size, nil
	case 10:
		return 12, nil
	case 12:
		return sha256.Size, nil
	case 13:
		return sha512.Size384, nil
	case 14:
		return sha512.Size, nil
	case 15, 16:
		return 12, nil
	case 19:
		return 16, nil
	case 20:
		return 24, nil
	default:
		return 0, fmt.Errorf("%w: %d", ErrUnsupportedChecksum, signatureType)
	}
}

func hmacBytes(newHash func() hash.Hash, key, data []byte) []byte {
	h := hmac.New(newHash, key)
	h.Write(data)
	return h.Sum(nil)
}

func truncate(in []byte, n int) []byte {
	if len(in) <= n {
		return in
	}
	return in[:n]
}

// rc4HMACChecksum implements KERB_CHECKSUM_HMAC_MD5 as described by RFC 4757:
//
//	Ksign = HMAC-MD5(K, "signaturekey\x00")
//	tmp   = MD5(usage || data)
//	sum   = HMAC-MD5(Ksign, tmp)
//
// RFC 4757 encodes the usage/message type as a little-endian four-byte value.
func rc4HMACChecksum(key, data []byte, usage uint32) ([]byte, error) {
	signKey := hmacBytes(md5.New, key, []byte("signaturekey\x00"))

	usageBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(usageBytes, usage)
	input := make([]byte, 0, len(usageBytes)+len(data))
	input = append(input, usageBytes...)
	input = append(input, data...)

	sum := md5.Sum(input)
	return hmacBytes(md5.New, signKey, sum[:]), nil
}
