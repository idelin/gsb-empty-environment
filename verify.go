package netsec

import (
	"bytes"
	"crypto"
	"crypto/dsa"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rsa"
	_ "crypto/sha1"
	_ "crypto/sha256"
	_ "crypto/sha512"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
)

var (
	// ErrMissingContentType indicates that signedAttrs did not contain exactly
	// one content-type attribute.
	ErrMissingContentType = errors.New("netsec: signedAttrs is missing exactly one content-type attribute")

	// ErrMissingMessageDigest indicates that signedAttrs did not contain
	// exactly one message-digest attribute.
	ErrMissingMessageDigest = errors.New("netsec: signedAttrs is missing exactly one message-digest attribute")

	// ErrDuplicateAttribute indicates that a signed attribute type occurred
	// more than once.
	ErrDuplicateAttribute = errors.New("netsec: duplicate signed attribute")

	// ErrContentTypeMismatch indicates that the content-type signed attribute
	// does not match the encapsulated content type.
	ErrContentTypeMismatch = errors.New("netsec: content-type attribute does not match eContentType")

	// ErrMessageDigestMismatch indicates that the signed message-digest does
	// not cover the content being verified.
	ErrMessageDigestMismatch = errors.New("netsec: message-digest does not match content")

	// ErrDetachedContentRequired indicates that the SignedData has no
	// encapsulated content and the caller did not supply any.
	ErrDetachedContentRequired = errors.New("netsec: detached content is required")

	// ErrContentMismatch indicates that both encapsulated and caller-supplied
	// content were present but differed.
	ErrContentMismatch = errors.New("netsec: supplied content differs from encapsulated content")

	// ErrNoSignerCertificate indicates that no certificate matched a
	// SignerIdentifier.
	ErrNoSignerCertificate = errors.New("netsec: no certificate found for signer")

	// ErrUnsupportedAlgorithm indicates that a digest or signature algorithm
	// is not supported.
	ErrUnsupportedAlgorithm = errors.New("netsec: unsupported algorithm")

	// ErrSignedAttributesRequired indicates that policy required signedAttrs.
	ErrSignedAttributesRequired = errors.New("netsec: signed attributes are required")
)

// ValidateSigner applies RFC 5652 signed-attribute rules to one signer and
// verifies its signature over content. It does not perform certificate chain
// validation; use VerifySignedData for that.
func ValidateSigner(sd *SignedData, si *SignerInfo, content []byte) error {
	if sd == nil || si == nil {
		return errors.New("netsec: nil SignedData or SignerInfo")
	}
	if si.Certificate == nil {
		cert, err := findSignerCertificate(sd, si, nil)
		if err != nil {
			return err
		}
		si.Certificate = cert
	}
	return validateSigner(sd, si, content, &VerifyConfig{})
}

// VerifySigner validates one signer. Arguments may be supplied in any order:
// *SignedData, *SignerInfo, content []byte, and optionally a
// *x509.Certificate to use as the signer certificate.
func VerifySigner(args ...any) error {
	var (
		sd      *SignedData
		si      *SignerInfo
		content []byte
		cert    *x509.Certificate
	)
	for _, arg := range args {
		switch value := arg.(type) {
		case *SignedData:
			sd = value
		case *SignerInfo:
			si = value
		case []byte:
			content = value
		case *x509.Certificate:
			cert = value
		}
	}
	if sd == nil || si == nil {
		return errors.New("netsec: VerifySigner requires *SignedData and *SignerInfo")
	}
	if content == nil && sd.HasContent {
		content = sd.Content
	}
	if cert != nil {
		si.Certificate = cert
	}
	return ValidateSigner(sd, si, content)
}

// VerifySignerInfo is an alias for VerifySigner.
func VerifySignerInfo(args ...any) error {
	return VerifySigner(args...)
}

// ValidateSignedAttributes checks the signer's signed attributes and the
// message-digest without checking the signature value.
func ValidateSignedAttributes(sd *SignedData, si *SignerInfo, content []byte) error {
	if sd == nil || si == nil {
		return errors.New("netsec: nil SignedData or SignerInfo")
	}
	if !si.HasSignedAttrs {
		return ErrSignedAttributesRequired
	}
	return validateSignedAttributes(sd, si, content)
}

func validateSigner(sd *SignedData, si *SignerInfo, content []byte, cfg *VerifyConfig) error {
	if cfg.RequireSignedAttributes && !si.HasSignedAttrs {
		return ErrSignedAttributesRequired
	}

	if si.HasSignedAttrs {
		if err := validateSignedAttributes(sd, si, content); err != nil {
			return err
		}
		return verifySignerSignature(si, si.SignedAttrsDER())
	}

	// Without signed attributes, the signature is calculated directly over the
	// encapsulated (or detached) content.
	return verifySignerSignature(si, content)
}

func validateSignedAttributes(sd *SignedData, si *SignerInfo, content []byte) error {
	counts := make(map[string]int, len(si.SignedAttrs))
	var contentTypeValue asn1.ObjectIdentifier
	var messageDigestValue []byte
	hasContentType := false
	hasMessageDigest := false

	for _, attr := range si.SignedAttrs {
		key := attr.Type.String()
		counts[key]++
		if counts[key] > 1 {
			return fmt.Errorf("%w: %s", ErrDuplicateAttribute, key)
		}

		switch {
		case attr.Type.Equal(oidContentType):
			if len(attr.Values) == 0 {
				return fmt.Errorf("%w: content-type has no values", ErrMissingContentType)
			}
			if len(attr.Values) != 1 {
				return fmt.Errorf("%w: content-type has %d values: %w", ErrDuplicateAttribute, len(attr.Values), ErrMissingContentType)
			}
			if attr.Values[0].Class != derClassUniversal || attr.Values[0].Tag != derTagObjectIdentifier {
				return errors.New("netsec: content-type attribute value is not an OBJECT IDENTIFIER")
			}
			oid, err := parseRawOID(attr.Values[0])
			if err != nil {
				return fmt.Errorf("netsec: content-type attribute: %w", err)
			}
			contentTypeValue = oid
			hasContentType = true

		case attr.Type.Equal(oidMessageDigest):
			if len(attr.Values) == 0 {
				return fmt.Errorf("%w: message-digest has no values", ErrMissingMessageDigest)
			}
			if len(attr.Values) != 1 {
				return fmt.Errorf("%w: message-digest has %d values: %w", ErrDuplicateAttribute, len(attr.Values), ErrMissingMessageDigest)
			}
			if attr.Values[0].Class != derClassUniversal || attr.Values[0].Tag != derTagOctetString {
				return errors.New("netsec: message-digest attribute value is not an OCTET STRING")
			}
			digest, err := parseRawOctetString(attr.Values[0])
			if err != nil {
				return fmt.Errorf("netsec: message-digest attribute: %w", err)
			}
			messageDigestValue = digest
			hasMessageDigest = true
		}
	}

	if !hasContentType {
		return ErrMissingContentType
	}
	if !hasMessageDigest {
		return ErrMissingMessageDigest
	}
	if !contentTypeValue.Equal(sd.ContentType) {
		return fmt.Errorf("%w: attribute %s, encapsulated %s", ErrContentTypeMismatch, contentTypeValue, sd.ContentType)
	}

	hash, err := digestHashForOID(si.DigestAlgorithm.Algorithm)
	if err != nil {
		return err
	}
	expected, err := digestContent(hash, content)
	if err != nil {
		return err
	}
	if !hmac.Equal(expected, messageDigestValue) {
		return ErrMessageDigestMismatch
	}
	return nil
}

func digestContent(hash crypto.Hash, content []byte) ([]byte, error) {
	if !hash.Available() {
		return nil, fmt.Errorf("%w: digest %v is not linked", ErrUnsupportedAlgorithm, hash)
	}
	h := hash.New()
	if _, err := h.Write(content); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

func parseRawOID(value asn1.RawValue) (asn1.ObjectIdentifier, error) {
	der := value.FullBytes
	if len(der) == 0 {
		der = encodeDER(value.Class, value.Tag, value.IsCompound, value.Bytes)
	}
	v, rest, err := readDER(der)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return nil, errors.New("trailing bytes after OBJECT IDENTIFIER")
	}
	return parseDEROID(v)
}

func parseRawOctetString(value asn1.RawValue) ([]byte, error) {
	der := value.FullBytes
	if len(der) == 0 {
		der = encodeDER(value.Class, value.Tag, value.IsCompound, value.Bytes)
	}
	v, rest, err := readDER(der)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return nil, errors.New("trailing bytes after OCTET STRING")
	}
	return parseDEROctetString(v)
}

func verifySignerSignature(si *SignerInfo, signedBytes []byte) error {
	if si.Certificate == nil {
		return ErrNoSignerCertificate
	}
	return verifySignature(si.Certificate, si.SignatureAlgorithm, si.DigestAlgorithm, signedBytes, si.Signature)
}

func verifySignature(cert *x509.Certificate, signatureAlgorithm, digestAlgorithm pkix.AlgorithmIdentifier, signedBytes, signature []byte) error {
	if cert == nil {
		return ErrNoSignerCertificate
	}

	digestHash, digestHashErr := digestHashForOID(digestAlgorithm.Algorithm)
	signatureOID := signatureAlgorithm.Algorithm

	switch publicKey := cert.PublicKey.(type) {
	case *rsa.PublicKey:
		hash, isPSS, err := rsaSignatureHash(signatureOID, digestHash, digestHashErr)
		if err != nil {
			return err
		}
		digest, err := digestContent(hash, signedBytes)
		if err != nil {
			return err
		}
		if isPSS {
			opts, err := parsePSSOptions(signatureAlgorithm, hash)
			if err != nil {
				return err
			}
			if err := rsa.VerifyPSS(publicKey, hash, digest, signature, opts); err != nil {
				return fmt.Errorf("netsec: RSA-PSS signature verification failed: %w", err)
			}
			return nil
		}
		if err := rsa.VerifyPKCS1v15(publicKey, hash, digest, signature); err != nil {
			return fmt.Errorf("netsec: RSA signature verification failed: %w", err)
		}
		return nil

	case *ecdsa.PublicKey:
		hash, err := ecdsaSignatureHash(signatureOID, digestHash, digestHashErr)
		if err != nil {
			return err
		}
		digest, err := digestContent(hash, signedBytes)
		if err != nil {
			return err
		}
		if !ecdsa.VerifyASN1(publicKey, digest, signature) {
			return errors.New("netsec: ECDSA signature verification failed")
		}
		return nil

	case ed25519.PublicKey:
		if !signatureOID.Equal(oidEd25519) {
			return fmt.Errorf("%w: Ed25519 key with signature algorithm %s", ErrUnsupportedAlgorithm, signatureOID)
		}
		if !ed25519.Verify(publicKey, signedBytes, signature) {
			return errors.New("netsec: Ed25519 signature verification failed")
		}
		return nil

	case *dsa.PublicKey:
		hash, err := dsaSignatureHash(signatureOID, digestHash, digestHashErr)
		if err != nil {
			return err
		}
		digest, err := digestContent(hash, signedBytes)
		if err != nil {
			return err
		}
		var rs struct {
			R *big.Int
			S *big.Int
		}
		rest, err := asn1.Unmarshal(signature, &rs)
		if err != nil || len(rest) != 0 || rs.R == nil || rs.S == nil {
			return errors.New("netsec: malformed DSA signature")
		}
		if !dsa.Verify(publicKey, digest, rs.R, rs.S) {
			return errors.New("netsec: DSA signature verification failed")
		}
		return nil

	default:
		return fmt.Errorf("%w: public key type %T", ErrUnsupportedAlgorithm, cert.PublicKey)
	}
}

var (
	oidEd25519 = asn1.ObjectIdentifier{1, 3, 101, 112}

	oidMD5    = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 5}
	oidSHA1   = asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}
	oidSHA224 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 4}
	oidSHA256 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidSHA384 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 2}
	oidSHA512 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 3}

	oidRSAEncryption   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
	oidRSASSAPSS       = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 10}
	oidMGF1            = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 8}
	oidSHA1WithRSA     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 5}
	oidSHA224WithRSA   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 14}
	oidSHA256WithRSA   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 11}
	oidSHA384WithRSA   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 12}
	oidSHA512WithRSA   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 13}
	oidECDSAWithSHA1   = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 1}
	oidECDSAWithSHA224 = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 1}
	oidECDSAWithSHA256 = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 2}
	oidECDSAWithSHA384 = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 3}
	oidECDSAWithSHA512 = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 4}
	oidECPublicKey     = asn1.ObjectIdentifier{1, 2, 840, 10045, 2, 1}
	oidDSAWithSHA1     = asn1.ObjectIdentifier{1, 2, 840, 10040, 4, 3}
	oidDSAWithSHA224   = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 3, 1}
	oidDSAWithSHA256   = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 3, 2}
)

func digestHashForOID(oid asn1.ObjectIdentifier) (crypto.Hash, error) {
	switch {
	case oid.Equal(oidSHA1):
		return crypto.SHA1, nil
	case oid.Equal(oidSHA224):
		return crypto.SHA224, nil
	case oid.Equal(oidSHA256):
		return crypto.SHA256, nil
	case oid.Equal(oidSHA384):
		return crypto.SHA384, nil
	case oid.Equal(oidSHA512):
		return crypto.SHA512, nil
	case oid.Equal(oidMD5):
		return 0, fmt.Errorf("%w: MD5 digests are rejected", ErrUnsupportedAlgorithm)
	default:
		return 0, fmt.Errorf("%w: digest OID %s", ErrUnsupportedAlgorithm, oid)
	}
}

func signatureHashForOID(oid asn1.ObjectIdentifier) (crypto.Hash, bool) {
	switch {
	case oid.Equal(oidSHA1WithRSA), oid.Equal(oidECDSAWithSHA1), oid.Equal(oidDSAWithSHA1):
		return crypto.SHA1, true
	case oid.Equal(oidSHA224WithRSA), oid.Equal(oidECDSAWithSHA224), oid.Equal(oidDSAWithSHA224):
		return crypto.SHA224, true
	case oid.Equal(oidSHA256WithRSA), oid.Equal(oidECDSAWithSHA256), oid.Equal(oidDSAWithSHA256):
		return crypto.SHA256, true
	case oid.Equal(oidSHA384WithRSA), oid.Equal(oidECDSAWithSHA384):
		return crypto.SHA384, true
	case oid.Equal(oidSHA512WithRSA), oid.Equal(oidECDSAWithSHA512):
		return crypto.SHA512, true
	default:
		return 0, false
	}
}

func rsaSignatureHash(oid asn1.ObjectIdentifier, digestHash crypto.Hash, digestErr error) (crypto.Hash, bool, error) {
	if oid.Equal(oidRSASSAPSS) {
		if digestErr != nil {
			return 0, true, digestErr
		}
		return digestHash, true, nil
	}
	if oid.Equal(oidRSAEncryption) {
		if digestErr != nil {
			return 0, false, digestErr
		}
		return digestHash, false, nil
	}
	hash, ok := signatureHashForOID(oid)
	if !ok {
		return 0, false, fmt.Errorf("%w: signature OID %s", ErrUnsupportedAlgorithm, oid)
	}
	if digestErr == nil && hash != digestHash {
		return 0, false, fmt.Errorf("%w: signature algorithm %s uses %v but digestAlgorithm uses %v", ErrUnsupportedAlgorithm, oid, hash, digestHash)
	}
	return hash, false, nil
}

func ecdsaSignatureHash(oid asn1.ObjectIdentifier, digestHash crypto.Hash, digestErr error) (crypto.Hash, error) {
	if oid.Equal(oidECPublicKey) {
		if digestErr != nil {
			return 0, digestErr
		}
		return digestHash, nil
	}
	hash, ok := signatureHashForOID(oid)
	if !ok {
		return 0, fmt.Errorf("%w: signature OID %s", ErrUnsupportedAlgorithm, oid)
	}
	if digestErr == nil && hash != digestHash {
		return 0, fmt.Errorf("%w: signature algorithm %s uses %v but digestAlgorithm uses %v", ErrUnsupportedAlgorithm, oid, hash, digestHash)
	}
	return hash, nil
}

func dsaSignatureHash(oid asn1.ObjectIdentifier, digestHash crypto.Hash, digestErr error) (crypto.Hash, error) {
	hash, ok := signatureHashForOID(oid)
	if !ok {
		return 0, fmt.Errorf("%w: signature OID %s", ErrUnsupportedAlgorithm, oid)
	}
	if digestErr == nil && hash != digestHash {
		return 0, fmt.Errorf("%w: signature algorithm %s uses %v but digestAlgorithm uses %v", ErrUnsupportedAlgorithm, oid, hash, digestHash)
	}
	return hash, nil
}

func parsePSSOptions(algorithm pkix.AlgorithmIdentifier, digestHash crypto.Hash) (*rsa.PSSOptions, error) {
	options := &rsa.PSSOptions{
		Hash:       digestHash,
		SaltLength: rsa.PSSSaltLengthEqualsHash,
	}

	parameters := algorithm.Parameters
	if len(parameters.FullBytes) == 0 && len(parameters.Bytes) == 0 {
		// RFC 4055 defaults: SHA-1, MGF1-SHA1, salt length 20.
		if digestHash != crypto.SHA1 {
			return nil, fmt.Errorf("%w: PSS defaults require SHA-1", ErrUnsupportedAlgorithm)
		}
		options.Hash = crypto.SHA1
		options.SaltLength = 20
		return options, nil
	}

	raw := parameters.FullBytes
	if len(raw) == 0 {
		raw = encodeDER(parameters.Class, parameters.Tag, parameters.IsCompound, parameters.Bytes)
	}
	v, rest, err := readDER(raw)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 || v.Class != derClassUniversal || v.Tag != derTagSequence {
		return nil, errors.New("netsec: malformed RSASSA-PSS parameters")
	}
	fields, err := splitDER(v.Content)
	if err != nil {
		return nil, err
	}

	paramHash := crypto.SHA1
	mgfHash := crypto.SHA1
	saltLength := 20
	for _, field := range fields {
		if field.Class != derClassContextSpecific || !field.Constructed {
			return nil, errors.New("netsec: malformed RSASSA-PSS parameter")
		}
		inner, err := splitDER(field.Content)
		if err != nil || len(inner) != 1 {
			return nil, errors.New("netsec: malformed RSASSA-PSS parameter value")
		}
		switch field.Tag {
		case 0:
			hash, err := hashFromAlgorithmIdentifier(inner[0])
			if err != nil {
				return nil, err
			}
			paramHash = hash
		case 1:
			mgfAlgorithm, err := parseAlgorithmIdentifier(inner[0])
			if err != nil {
				return nil, err
			}
			if !mgfAlgorithm.Algorithm.Equal(oidMGF1) {
				return nil, fmt.Errorf("%w: unsupported PSS MGF %s", ErrUnsupportedAlgorithm, mgfAlgorithm.Algorithm)
			}
			mgfRaw := mgfAlgorithm.Parameters.FullBytes
			if len(mgfRaw) == 0 {
				mgfRaw = encodeDER(mgfAlgorithm.Parameters.Class, mgfAlgorithm.Parameters.Tag, mgfAlgorithm.Parameters.IsCompound, mgfAlgorithm.Parameters.Bytes)
			}
			mgfValue, rest, err := readDER(mgfRaw)
			if err != nil || len(rest) != 0 {
				return nil, errors.New("netsec: malformed PSS MGF1 parameters")
			}
			hash, err := hashFromAlgorithmIdentifier(mgfValue)
			if err != nil {
				return nil, err
			}
			mgfHash = hash
		case 2:
			value, err := parseDERInteger(inner[0])
			if err != nil {
				return nil, err
			}
			saltLength = value
		case 3:
			value, err := parseDERInteger(inner[0])
			if err != nil {
				return nil, err
			}
			if value != 1 {
				return nil, errors.New("netsec: unsupported PSS trailer field")
			}
		default:
			return nil, errors.New("netsec: unknown RSASSA-PSS parameter")
		}
	}
	if paramHash != digestHash || mgfHash != digestHash {
		return nil, errors.New("netsec: PSS parameters do not use the signer digest algorithm")
	}
	options.Hash = digestHash
	options.SaltLength = saltLength
	return options, nil
}

func hashFromAlgorithmIdentifier(v derValue) (crypto.Hash, error) {
	algorithm, err := parseAlgorithmIdentifier(v)
	if err != nil {
		return 0, err
	}
	return digestHashForOID(algorithm.Algorithm)
}

func findSignerCertificate(sd *SignedData, si *SignerInfo, extra []*x509.Certificate) (*x509.Certificate, error) {
	all := make([]*x509.Certificate, 0, len(sd.Certificates)+len(extra))
	all = append(all, sd.Certificates...)
	all = append(all, extra...)

	for _, cert := range all {
		if cert == nil {
			continue
		}
		switch {
		case si.SerialNumber != nil:
			if cert.SerialNumber != nil &&
				cert.SerialNumber.Cmp(si.SerialNumber) == 0 &&
				issuerMatches(cert.RawIssuer, si.Issuer) {
				return cert, nil
			}
		case len(si.SubjectKeyIdentifier) != 0:
			if bytes.Equal(cert.SubjectKeyId, si.SubjectKeyIdentifier) {
				return cert, nil
			}
		}
	}
	return nil, ErrNoSignerCertificate
}

func issuerMatches(certIssuer, signerIssuer []byte) bool {
	if bytes.Equal(certIssuer, signerIssuer) {
		return true
	}
	var a, b pkix.RDNSequence
	restA, errA := asn1.Unmarshal(certIssuer, &a)
	restB, errB := asn1.Unmarshal(signerIssuer, &b)
	return errA == nil && errB == nil && len(restA) == 0 && len(restB) == 0 && a.String() == b.String()
}
