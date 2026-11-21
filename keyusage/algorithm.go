package keyusage

// Algorithm is the identifier of a concrete algorithm/mode combination.
type Algorithm string

// Whitelisted, sufficiently strong algorithms.
const (
	AlgRSA2048PSSSHA256  Algorithm = "RSA-2048-PSS-SHA256"
	AlgRSA3072PSSSHA384  Algorithm = "RSA-3072-PSS-SHA384"
	AlgECDSAP256SHA256   Algorithm = "ECDSA-P256-SHA256"
	AlgECDSAP384SHA384   Algorithm = "ECDSA-P384-SHA384"
	AlgEd25519           Algorithm = "Ed25519"
	AlgHMACSHA256        Algorithm = "HMAC-SHA256"
	AlgHMACSHA384        Algorithm = "HMAC-SHA384"
	AlgRSA2048OAEPSHA256 Algorithm = "RSA-2048-OAEP-SHA256"
	AlgRSA3072OAEPSHA384 Algorithm = "RSA-3072-OAEP-SHA384"
	AlgAES256GCM         Algorithm = "AES-256-GCM"
	AlgECDHp256          Algorithm = "ECDH-P256"
	AlgECDHp384          Algorithm = "ECDH-P384"
	AlgHKDFSHA256        Algorithm = "HKDF-SHA256"
)

// Explicitly weak or broken algorithms. They are rejected with a precise
// ErrWeakAlgorithm reason even if someone adds them to a whitelist.
const (
	AlgRSA1024PKCS1v15SHA1 Algorithm = "RSA-1024-PKCS1v1.5-SHA1"
	AlgRSA1024OAEPSHA1     Algorithm = "RSA-1024-OAEP-SHA1"
	AlgECDSAP192SHA1       Algorithm = "ECDSA-P192-SHA1"
	Alg3DESCBC             Algorithm = "3DES-CBC"
	AlgDESCBC              Algorithm = "DES-CBC"
	AlgRC4                 Algorithm = "RC4"
	AlgHMACSHA1            Algorithm = "HMAC-SHA1"
	AlgHMACMD5             Algorithm = "HMAC-MD5"
)

// weakReasons maps every known weak algorithm to a human-readable explanation
// used in audit records.
var weakReasons = map[Algorithm]string{
	AlgRSA1024PKCS1v15SHA1: "RSA-1024 with PKCS#1 v1.5 and SHA-1: short modulus and broken padding/hash",
	AlgRSA1024OAEPSHA1:     "RSA-1024 with SHA-1: modulus below 2048-bit minimum and deprecated hash",
	AlgECDSAP192SHA1:       "ECDSA P-192 with SHA-1: insufficient curve strength and deprecated hash",
	Alg3DESCBC:             "3DES: 64-bit block size is vulnerable to birthday attacks (Sweet32)",
	AlgDESCBC:              "DES: 56-bit key is trivially brute-forced",
	AlgRC4:                 "RC4: biased keystream, prohibited for new use",
	AlgHMACSHA1:            "HMAC-SHA-1: SHA-1 is deprecated and must not be used for new deployments",
	AlgHMACMD5:             "HMAC-MD5: MD5 is cryptographically broken",
}

// IsWeak reports whether alg is classified as a weak algorithm.
func IsWeak(alg Algorithm) bool {
	_, weak := weakReasons[alg]
	return weak
}

// weakReason returns the audit explanation and true for weak algorithms.
func weakReason(alg Algorithm) (string, bool) {
	reason, weak := weakReasons[alg]
	return reason, weak
}

// DefaultWhitelist returns a copy of the built-in strong-algorithm whitelist.
func DefaultWhitelist() map[Algorithm]bool {
	return map[Algorithm]bool{
		AlgRSA2048PSSSHA256:  true,
		AlgRSA3072PSSSHA384:  true,
		AlgECDSAP256SHA256:   true,
		AlgECDSAP384SHA384:   true,
		AlgEd25519:           true,
		AlgHMACSHA256:        true,
		AlgHMACSHA384:        true,
		AlgRSA2048OAEPSHA256: true,
		AlgRSA3072OAEPSHA384: true,
		AlgAES256GCM:         true,
		AlgECDHp256:          true,
		AlgECDHp384:          true,
		AlgHKDFSHA256:        true,
	}
}
