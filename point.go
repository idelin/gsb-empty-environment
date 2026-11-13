package netsec

import (
	"crypto/ecdh"
	"errors"
)

// UncompressedPointPrefix is the single point format TLS 1.3 negotiates.
// See RFC 8446, section 4.2.8.2 and RFC 8422, section 5.1.2.
const UncompressedPointPrefix byte = 0x04

// ErrRejectedPointFormat is returned when the peer's key share does not use
// the uncompressed point format. Compressed (0x02, 0x03) and hybrid
// (0x06, 0x07) encodings, or any other prefix, must never be decompressed and
// fed into the key exchange.
var ErrRejectedPointFormat = errors.New("netsec: TLS 1.3 only accepts the uncompressed point format")

// DecodePoint performs the TLS 1.3 point-format adjudication for an ECDHE key
// share on a NIST curve (P-256, P-384 or P-521).
//
// It accepts only an uncompressed point (prefix 0x04) that lies on curve;
// compressed points, hybrid points, unknown prefixes, truncated encodings and
// off-curve points are rejected without being decompressed.
func DecodePoint(curve ecdh.Curve, data []byte) (*ecdh.PublicKey, error) {
	if len(data) == 0 || data[0] != UncompressedPointPrefix {
		return nil, ErrRejectedPointFormat
	}
	key, err := curve.NewPublicKey(data)
	if err != nil {
		return nil, err
	}
	return key, nil
}

// DecodeP256Point decodes a TLS 1.3 P-256 key share.
func DecodeP256Point(data []byte) (*ecdh.PublicKey, error) {
	return DecodePoint(ecdh.P256(), data)
}

// DecodeP384Point decodes a TLS 1.3 P-384 key share.
func DecodeP384Point(data []byte) (*ecdh.PublicKey, error) {
	return DecodePoint(ecdh.P384(), data)
}

// DecodeP521Point decodes a TLS 1.3 P-521 key share.
func DecodeP521Point(data []byte) (*ecdh.PublicKey, error) {
	return DecodePoint(ecdh.P521(), data)
}
