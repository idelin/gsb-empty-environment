package netsec

import "math/big"

// DNSSEC security algorithm numbers as maintained by IANA in the
// "Domain Name System Security (DNSSEC) Algorithm Numbers" registry.
// Only the numbers needed by this package are named; callers may pass
// any other numeric identifier through the policy's disabled list.
const (
	AlgorithmRSAMD5       = 1  // [RFC3110] deprecated
	AlgorithmDH           = 2  // [RFC2539]
	AlgorithmDSA          = 3  // [RFC3755]
	AlgorithmRSASHA1      = 5  // [RFC3110]
	AlgorithmDSANSEC3SHA1 = 6  // [RFC5155]
	AlgorithmRSASHA1NSEC3 = 7  // [RFC5155]
	AlgorithmRSASHA256    = 8  // [RFC5702]
	AlgorithmRSASHA512    = 10 // [RFC5702]
	AlgorithmECCGOST      = 12 // [RFC5933]
	AlgorithmECDSAP256    = 13 // [RFC6605]
	AlgorithmECDSAP384    = 14 // [RFC6605]
	AlgorithmED25519      = 15 // [RFC8080]
	AlgorithmED448        = 16 // [RFC8080]
	AlgorithmINDIRECT     = 252
	AlgorithmPRIVATEDNS   = 253
	AlgorithmPRIVATEOID   = 254
)

// rsaAlgorithms is the set of algorithm numbers whose DNSKEY public key
// blob carries an RSA public key encoded according to RFC 3110 and is
// therefore subject to the minimum modulus length gate.
var rsaAlgorithms = map[uint8]bool{
	AlgorithmRSAMD5:       true,
	AlgorithmRSASHA1:      true,
	AlgorithmRSASHA1NSEC3: true,
	AlgorithmRSASHA256:    true,
	AlgorithmRSASHA512:    true,
}

// isRSAAlgorithm reports whether algorithm uses RSA key material.
func isRSAAlgorithm(algorithm uint8) bool {
	return rsaAlgorithms[algorithm]
}

// rsaModulusBits parses the RSA public key blob carried in a DNSKEY RDATA
// and returns the length of the modulus in bits. The blob is the public
// key portion (base64-decoded) and is encoded as described in RFC 3110
// section 2: an exponent length prefix, the exponent, then the modulus.
//
// It returns ok == false when the blob is truncated or otherwise cannot
// contain a well-formed RSA key. Such keys are treated as unusable rather
// than crashing the validator on attacker-controlled data.
func rsaModulusBits(publicKey []byte) (bits int, ok bool) {
	if len(publicKey) == 0 {
		return 0, false
	}

	pos := 0
	exponentLen := int(publicKey[0])
	pos++

	if exponentLen == 0 {
		// A zero first byte means the exponent length is the following
		// two bytes in network byte order.
		if len(publicKey) < 3 {
			return 0, false
		}
		exponentLen = int(publicKey[1])<<8 | int(publicKey[2])
		pos = 3
	}

	if exponentLen < 0 || pos+exponentLen > len(publicKey) {
		return 0, false
	}

	modulus := publicKey[pos+exponentLen:]
	if len(modulus) == 0 {
		return 0, false
	}

	// The modulus is an unsigned big-endian integer; BitLen yields its
	// true length in bits rather than the padded wire size.
	return new(big.Int).SetBytes(modulus).BitLen(), true
}
