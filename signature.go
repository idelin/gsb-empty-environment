package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509/pkix"
	"encoding/asn1"
	"fmt"
)

// SignatureScheme is a TLS 1.3 SignatureScheme codepoint (RFC 8446).
type SignatureScheme uint16

const (
	RSA_PKCS1_SHA256 SignatureScheme = 0x0401
	RSA_PKCS1_SHA384 SignatureScheme = 0x0501
	RSA_PKCS1_SHA512 SignatureScheme = 0x0601

	ECDSA_SECP256R1_SHA256 SignatureScheme = 0x0403
	ECDSA_SECP384R1_SHA384 SignatureScheme = 0x0503
	ECDSA_SECP521R1_SHA512 SignatureScheme = 0x0603

	RSA_PSS_RSAE_SHA256 SignatureScheme = 0x0804
	RSA_PSS_RSAE_SHA384 SignatureScheme = 0x0805
	RSA_PSS_RSAE_SHA512 SignatureScheme = 0x0806

	ED25519 SignatureScheme = 0x0807
	ED448   SignatureScheme = 0x0808

	RSA_PSS_PSS_SHA256 SignatureScheme = 0x0809
	RSA_PSS_PSS_SHA384 SignatureScheme = 0x080a
	RSA_PSS_PSS_SHA512 SignatureScheme = 0x080b
)

var (
	oidRSAEncryption = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
	oidRSAPSS        = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 10}
)

type keyKind uint8

const (
	keyUnknown keyKind = iota
	keyRSA
	keyECDSA
	keyEd25519
)

type signatureSchemeInfo struct {
	kind       keyKind
	hash       crypto.Hash
	curve      elliptic.Curve
	rsaKeyOID  asn1.ObjectIdentifier
	tls13Valid bool
}

func signatureScheme(scheme SignatureScheme) (signatureSchemeInfo, bool) {
	switch scheme {
	case ECDSA_SECP256R1_SHA256:
		return signatureSchemeInfo{kind: keyECDSA, hash: crypto.SHA256, curve: elliptic.P256(), tls13Valid: true}, true
	case ECDSA_SECP384R1_SHA384:
		return signatureSchemeInfo{kind: keyECDSA, hash: crypto.SHA384, curve: elliptic.P384(), tls13Valid: true}, true
	case ECDSA_SECP521R1_SHA512:
		return signatureSchemeInfo{kind: keyECDSA, hash: crypto.SHA512, curve: elliptic.P521(), tls13Valid: true}, true
	case ED25519:
		return signatureSchemeInfo{kind: keyEd25519, tls13Valid: true}, true
	case RSA_PSS_RSAE_SHA256:
		return signatureSchemeInfo{kind: keyRSA, hash: crypto.SHA256, rsaKeyOID: oidRSAEncryption, tls13Valid: true}, true
	case RSA_PSS_RSAE_SHA384:
		return signatureSchemeInfo{kind: keyRSA, hash: crypto.SHA384, rsaKeyOID: oidRSAEncryption, tls13Valid: true}, true
	case RSA_PSS_RSAE_SHA512:
		return signatureSchemeInfo{kind: keyRSA, hash: crypto.SHA512, rsaKeyOID: oidRSAEncryption, tls13Valid: true}, true
	case RSA_PSS_PSS_SHA256:
		return signatureSchemeInfo{kind: keyRSA, hash: crypto.SHA256, rsaKeyOID: oidRSAPSS, tls13Valid: true}, true
	case RSA_PSS_PSS_SHA384:
		return signatureSchemeInfo{kind: keyRSA, hash: crypto.SHA384, rsaKeyOID: oidRSAPSS, tls13Valid: true}, true
	case RSA_PSS_PSS_SHA512:
		return signatureSchemeInfo{kind: keyRSA, hash: crypto.SHA512, rsaKeyOID: oidRSAPSS, tls13Valid: true}, true
	default:
		return signatureSchemeInfo{}, false
	}
}

func (s SignatureScheme) String() string {
	switch s {
	case RSA_PKCS1_SHA256:
		return "rsa_pkcs1_sha256"
	case RSA_PKCS1_SHA384:
		return "rsa_pkcs1_sha384"
	case RSA_PKCS1_SHA512:
		return "rsa_pkcs1_sha512"
	case ECDSA_SECP256R1_SHA256:
		return "ecdsa_secp256r1_sha256"
	case ECDSA_SECP384R1_SHA384:
		return "ecdsa_secp384r1_sha384"
	case ECDSA_SECP521R1_SHA512:
		return "ecdsa_secp521r1_sha512"
	case RSA_PSS_RSAE_SHA256:
		return "rsa_pss_rsae_sha256"
	case RSA_PSS_RSAE_SHA384:
		return "rsa_pss_rsae_sha384"
	case RSA_PSS_RSAE_SHA512:
		return "rsa_pss_rsae_sha512"
	case ED25519:
		return "ed25519"
	case ED448:
		return "ed448"
	case RSA_PSS_PSS_SHA256:
		return "rsa_pss_pss_sha256"
	case RSA_PSS_PSS_SHA384:
		return "rsa_pss_pss_sha384"
	case RSA_PSS_PSS_SHA512:
		return "rsa_pss_pss_sha512"
	default:
		return fmt.Sprintf("signature_scheme(0x%04x)", uint16(s))
	}
}

// allowedForCertificateVerify reports whether the scheme is usable in a TLS 1.3
// CertificateVerify message. In particular, RSA PKCS#1 v1.5 schemes are not.
func (s SignatureScheme) allowedForCertificateVerify() bool {
	info, ok := signatureScheme(s)
	return ok && info.tls13Valid
}

// allowedForDelegatedKey reports whether the scheme may be used by the
// delegated private key. RFC 9345 forbids rsa_pss_rsae_* here because delegated
// RSA keys must be encoded with the RSASSA-PSS OID, not rsaEncryption.
func (s SignatureScheme) allowedForDelegatedKey() bool {
	info, ok := signatureScheme(s)
	if !ok || !info.tls13Valid {
		return false
	}
	return !(info.kind == keyRSA && info.rsaKeyOID.Equal(oidRSAEncryption))
}

func checkKeySupportsScheme(scheme SignatureScheme, pub crypto.PublicKey, keyOID asn1.ObjectIdentifier, pssParams *asn1.RawValue, allowRSAEncryption bool) error {
	info, ok := signatureScheme(scheme)
	if !ok || !info.tls13Valid {
		return fmt.Errorf("%w: %s is not allowed for TLS 1.3 signatures", ErrUnsupportedAlgorithm, scheme)
	}
	if pub == nil {
		return fmt.Errorf("%w: missing public key", ErrUnsupportedAlgorithm)
	}

	switch info.kind {
	case keyECDSA:
		ec, ok := pub.(*ecdsa.PublicKey)
		if !ok || ec.Curve == nil || ec.Curve.Params().Name != info.curve.Params().Name {
			return fmt.Errorf("%w: %s requires an ECDSA key on curve %s", ErrAlgorithmMismatch, scheme, info.curve.Params().Name)
		}
	case keyEd25519:
		ed, ok := pub.(ed25519.PublicKey)
		if !ok || len(ed) != ed25519.PublicKeySize {
			return fmt.Errorf("%w: %s requires an Ed25519 key", ErrAlgorithmMismatch, scheme)
		}
	case keyRSA:
		rsaKey, ok := pub.(*rsa.PublicKey)
		if !ok || rsaKey.N == nil {
			return fmt.Errorf("%w: %s requires an RSA key", ErrAlgorithmMismatch, scheme)
		}
		if rsaKey.N.BitLen() < 2048 {
			return fmt.Errorf("%w: RSA key is smaller than 2048 bits", ErrUnsupportedAlgorithm)
		}
		if info.rsaKeyOID.Equal(oidRSAEncryption) {
			if !allowRSAEncryption {
				return fmt.Errorf("%w: delegated RSA keys must use the RSASSA-PSS OID", ErrAlgorithmMismatch)
			}
			if !keyOID.Equal(oidRSAEncryption) {
				return fmt.Errorf("%w: %s requires an rsaEncryption key", ErrAlgorithmMismatch, scheme)
			}
		} else {
			if !keyOID.Equal(oidRSAPSS) {
				return fmt.Errorf("%w: %s requires an RSASSA-PSS key", ErrAlgorithmMismatch, scheme)
			}
			if err := checkPSSKeyParameters(pssParams, info.hash); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("%w: unsupported key type", ErrUnsupportedAlgorithm)
	}
	return nil
}

func verifySignature(scheme SignatureScheme, pub crypto.PublicKey, message, signature []byte) error {
	info, ok := signatureScheme(scheme)
	if !ok || !info.tls13Valid {
		return fmt.Errorf("%w: %s", ErrUnsupportedAlgorithm, scheme)
	}
	if len(signature) == 0 {
		return fmt.Errorf("%w: empty signature", ErrSignatureInvalid)
	}

	switch info.kind {
	case keyECDSA:
		ec, ok := pub.(*ecdsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: not an ECDSA key", ErrAlgorithmMismatch)
		}
		digest, err := hashMessage(info.hash, message)
		if err != nil {
			return err
		}
		if !ecdsa.VerifyASN1(ec, digest, signature) {
			return fmt.Errorf("%w: ECDSA signature mismatch", ErrSignatureInvalid)
		}
		return nil
	case keyEd25519:
		ed, ok := pub.(ed25519.PublicKey)
		if !ok {
			return fmt.Errorf("%w: not an Ed25519 key", ErrAlgorithmMismatch)
		}
		if !ed25519.Verify(ed, message, signature) {
			return fmt.Errorf("%w: Ed25519 signature mismatch", ErrSignatureInvalid)
		}
		return nil
	case keyRSA:
		rsaKey, ok := pub.(*rsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: not an RSA key", ErrAlgorithmMismatch)
		}
		digest, err := hashMessage(info.hash, message)
		if err != nil {
			return err
		}
		opts := &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: info.hash}
		if err := rsa.VerifyPSS(rsaKey, info.hash, digest, signature, opts); err != nil {
			return fmt.Errorf("%w: RSA-PSS signature mismatch: %v", ErrSignatureInvalid, err)
		}
		return nil
	default:
		return fmt.Errorf("%w: unsupported key type", ErrUnsupportedAlgorithm)
	}
}

func hashMessage(hash crypto.Hash, message []byte) ([]byte, error) {
	if !hash.Available() {
		return nil, fmt.Errorf("%w: hash %v unavailable", ErrUnsupportedAlgorithm, hash)
	}
	switch hash {
	case crypto.SHA256:
		sum := sha256.Sum256(message)
		return sum[:], nil
	case crypto.SHA384:
		sum := sha512.Sum384(message)
		return sum[:], nil
	case crypto.SHA512:
		sum := sha512.Sum512(message)
		return sum[:], nil
	default:
		return nil, fmt.Errorf("%w: unsupported hash %v", ErrUnsupportedAlgorithm, hash)
	}
}

func checkPSSKeyParameters(params *asn1.RawValue, want crypto.Hash) error {
	if params == nil {
		return fmt.Errorf("%w: missing RSASSA-PSS parameters", ErrUnsupportedAlgorithm)
	}
	if err := validatePSSParameters(*params, want); err != nil {
		return err
	}
	return nil
}

func validatePSSParameters(params asn1.RawValue, want crypto.Hash) error {
	if params.Class != asn1.ClassUniversal || params.Tag != asn1.TagSequence {
		return fmt.Errorf("%w: malformed RSASSA-PSS parameters", ErrUnsupportedAlgorithm)
	}

	hashAlg := crypto.SHA1
	mgfHash := crypto.SHA1
	saltLen := 20
	trailerField := 1

	content := params.Bytes
	for len(content) > 0 {
		var element asn1.RawValue
		rest, err := asn1.Unmarshal(content, &element)
		if err != nil {
			return fmt.Errorf("%w: malformed RSASSA-PSS parameters", ErrUnsupportedAlgorithm)
		}
		content = rest
		if element.Class != asn1.ClassContextSpecific {
			return fmt.Errorf("%w: unexpected RSASSA-PSS parameter", ErrUnsupportedAlgorithm)
		}
		switch element.Tag {
		case 0:
			var ai pkix.AlgorithmIdentifier
			if _, err := asn1.Unmarshal(element.Bytes, &ai); err != nil {
				return fmt.Errorf("%w: malformed PSS hash algorithm", ErrUnsupportedAlgorithm)
			}
			hashAlg = hashFromOID(ai.Algorithm)
		case 1:
			var ai pkix.AlgorithmIdentifier
			if _, err := asn1.Unmarshal(element.Bytes, &ai); err != nil {
				return fmt.Errorf("%w: malformed PSS mask algorithm", ErrUnsupportedAlgorithm)
			}
			if !ai.Algorithm.Equal(oidMGF1) {
				return fmt.Errorf("%w: unsupported PSS mask algorithm", ErrUnsupportedAlgorithm)
			}
			var mgfAI pkix.AlgorithmIdentifier
			if _, err := asn1.Unmarshal(ai.Parameters.FullBytes, &mgfAI); err != nil {
				return fmt.Errorf("%w: malformed PSS MGF hash", ErrUnsupportedAlgorithm)
			}
			mgfHash = hashFromOID(mgfAI.Algorithm)
		case 2:
			if _, err := asn1.Unmarshal(element.Bytes, &saltLen); err != nil {
				return fmt.Errorf("%w: malformed PSS salt length", ErrUnsupportedAlgorithm)
			}
		case 3:
			if _, err := asn1.Unmarshal(element.Bytes, &trailerField); err != nil {
				return fmt.Errorf("%w: malformed PSS trailer field", ErrUnsupportedAlgorithm)
			}
		default:
			return fmt.Errorf("%w: unknown RSASSA-PSS parameter", ErrUnsupportedAlgorithm)
		}
	}

	if hashAlg != want {
		return fmt.Errorf("%w: PSS key hash restriction does not match %s", ErrAlgorithmMismatch, want)
	}
	if mgfHash != want {
		return fmt.Errorf("%w: PSS key MGF hash restriction does not match %s", ErrAlgorithmMismatch, want)
	}
	if saltLen != want.Size() {
		return fmt.Errorf("%w: PSS key salt length restriction does not match %s", ErrAlgorithmMismatch, want)
	}
	if trailerField != 1 {
		return fmt.Errorf("%w: unsupported PSS trailer field", ErrUnsupportedAlgorithm)
	}
	return nil
}

var (
	oidSHA1   = asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}
	oidSHA256 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidSHA384 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 2}
	oidSHA512 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 3}
	oidMGF1   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 8}
)

func hashFromOID(oid asn1.ObjectIdentifier) crypto.Hash {
	switch {
	case oid.Equal(oidSHA256):
		return crypto.SHA256
	case oid.Equal(oidSHA384):
		return crypto.SHA384
	case oid.Equal(oidSHA512):
		return crypto.SHA512
	case oid.Equal(oidSHA1):
		return crypto.SHA1
	default:
		return 0
	}
}
