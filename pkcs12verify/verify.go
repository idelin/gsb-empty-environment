// Package pkcs12verify performs raw ASN.1 DER level structural integrity and
// security-policy validation of PKCS#12 (PFX, RFC 7292) key packages.
//
// It is intentionally a validation-only package: it never decrypts key
// material and never imports certificates. A PFX is accepted only when:
//
//   - the blob is strict, canonical DER with exactly one PFX TLV (no trailing
//     bytes, no BER indefinite or non-minimal lengths);
//   - version is 3, authSafe is id-data carrying an OCTET STRING, and the
//     mandatory MacData is present;
//   - the integrity MAC uses SHA-256/384/512 via the RFC 7292 KDF, or
//     PBMAC1 with PBKDF2-HMAC-SHA256 (MD5/SHA-1 are hard rejected), with salt
//     and iteration counts inside the configured policy window;
//   - the raw authSafe.content octets authenticate under a constant-time MAC;
//   - every SafeBag is a certBag (X.509) or a pkcs8ShroudedKeyBag using
//     PBES2 + PBKDF2 + AES-256-CBC/GCM; plaintext keyBag and all RC2/RC4/DES/
//     3DES / legacy PBE algorithms are rejected.
package pkcs12verify

import (
	"encoding/asn1"
	"errors"
	"fmt"
)

// Options configures the security policy. Zero values are replaced by safe
// defaults inside Verify; use a literal only when overriding.
type Options struct {
	// MinIterations and MaxIterations bound every PBKDF2/PKCS#12 iteration
	// count. The window blocks both offline-cracking low counts and CPU
	// exhaustion high counts.
	MinIterations int
	MaxIterations int

	// MinSaltLength is the minimum accepted MAC / PBKDF2 salt length.
	MinSaltLength int
}

// Default policy window: 10k..500k iterations and a 128-bit minimum salt.
const (
	DefaultMinIterations = 10000
	DefaultMaxIterations = 500000
	DefaultMinSalt       = 16
)

func (o Options) withDefaults() Options {
	if o.MinIterations == 0 {
		o.MinIterations = DefaultMinIterations
	}
	if o.MaxIterations == 0 {
		o.MaxIterations = DefaultMaxIterations
	}
	if o.MinSaltLength == 0 {
		o.MinSaltLength = DefaultMinSalt
	}
	return o
}

// BagKind classifies accepted SafeBag types.
type BagKind string

const (
	BagCert         BagKind = "certBag"
	BagShroudedKey  BagKind = "pkcs8ShroudedKeyBag"
	CipherAES256CBC string  = "AES-256-CBC"
	CipherAES256GCM string  = "AES-256-GCM"
	MACLegacy       string  = "pkcs12-mac"
	MACPBMAC1       string  = "PBMAC1"
)

// CipherInfo describes an accepted PBES2 encryption scheme.
type CipherInfo struct {
	Algorithm     string
	KDFIterations int
	PRF           string
}

// Report summarises the validated package for audit logging.
type Report struct {
	MACMode          string
	MACDigestOID     string
	MACIterations    int
	CertBagCount     int
	ShroudedKeyCount int
	EncryptedData    bool
	Ciphers          []CipherInfo
}

// Verify validates one complete PKCS#12 PFX blob against the policy. password
// is the UTF-8 integrity password; an empty password works only when the
// package itself was MACed with one.
func Verify(derBytes []byte, password string, opts Options) (*Report, error) {
	if len(derBytes) == 0 {
		return nil, wrap(ErrMalformed, "empty PFX input")
	}
	opts = opts.withDefaults()

	// (1) Strict canonical DER: exactly one TLV, no trailing garbage.
	if err := requireSingleDER(derBytes); err != nil {
		return nil, err
	}

	var pfx pfxPdu
	rest, err := asn1.Unmarshal(derBytes, &pfx)
	if err != nil {
		return nil, wrap(ErrMalformed, "PFX ASN.1 decode failed: "+err.Error())
	}
	if len(rest) != 0 {
		return nil, ErrTrailingBytes
	}

	if pfx.Version != 3 {
		return nil, wrap(ErrUnsupportedVersion, fmt.Sprintf("PFX version %d is not 3", pfx.Version))
	}

	// authSafe must be id-data wrapping the DER-encoded AuthenticatedSafe.
	if !pfx.AuthSafe.ContentType.Equal(oidData) {
		return nil, wrap(ErrMalformed, "authSafe contentType must be id-data")
	}
	authSafeContent, err := explicitOctetString(pfx.AuthSafe.Content)
	if err != nil {
		return nil, wrap(ErrMalformed, "authSafe content is absent")
	}
	if len(authSafeContent) == 0 {
		return nil, wrap(ErrMalformed, "authSafe content is empty")
	}

	// (2) Mandatory MacData and integrity verification.
	if len(pfx.MacData.Mac.Algorithm.Algorithm) == 0 {
		return nil, ErrMissingMacData
	}

	algoID, err := macAlgorithmIdentifier(pfx.MacData)
	if err != nil {
		return nil, err
	}
	if err := verifyMAC(pfx.MacData, algoID, authSafeContent, password, opts); err != nil {
		return nil, err
	}

	// (3) Only after integrity is proven, parse and policy-check the contents.
	if err := requireSingleDER(authSafeContent); err != nil {
		return nil, err
	}

	report := &Report{MACDigestOID: algoID.Algorithm.String()}
	if algoID.Algorithm.Equal(oidPBMAC1) {
		report.MACMode = MACPBMAC1
	} else {
		report.MACMode = MACLegacy
		report.MACIterations = pfx.MacData.Iterations
	}

	var authenticatedSafe []contentInfo
	if err := unmarshalStrict(authSafeContent, &authenticatedSafe); err != nil {
		return nil, wrap(ErrMalformed, "AuthenticatedSafe decode failed: "+err.Error())
	}
	if len(authenticatedSafe) == 0 {
		return nil, wrap(ErrMalformed, "AuthenticatedSafe is empty")
	}

	for i := range authenticatedSafe {
		if err := verifyContentInfo(authenticatedSafe[i], opts, report); err != nil {
			return nil, fmt.Errorf("AuthenticatedSafe[%d]: %w", i, err)
		}
	}

	if report.CertBagCount+report.ShroudedKeyCount == 0 {
		return nil, wrap(ErrMalformed, "no certBag or pkcs8ShroudedKeyBag present")
	}
	return report, nil
}

// verifyContentInfo validates one AuthenticatedSafe ContentInfo. id-data
// contains DER SafeContents directly; id-encryptedData contains an
// EncryptedData envelope whose algorithm must meet the PBES2/AES-256 policy.
func verifyContentInfo(ci contentInfo, opts Options, report *Report) error {
	switch {
	case ci.ContentType.Equal(oidData):
		payload, err := explicitOctetString(ci.Content)
		if err != nil {
			return wrap(ErrMalformed, "data ContentInfo must wrap an OCTET STRING")
		}
		return verifySafeContents(payload, opts, report)
	case ci.ContentType.Equal(oidEncryptedData):
		report.EncryptedData = true
		return verifyEncryptedData(ci, opts)
	default:
		return wrap(ErrUnsupportedBag, "unsupported AuthenticatedSafe contentType "+ci.ContentType.String())
	}
}

// verifySafeContents walks one DER-encoded SafeContents sequence.
func verifySafeContents(der []byte, opts Options, report *Report) error {
	if err := requireSingleDER(der); err != nil {
		return err
	}
	var bags []safeBag
	if err := unmarshalStrict(der, &bags); err != nil {
		return wrap(ErrMalformed, "SafeContents decode failed: "+err.Error())
	}
	if len(bags) == 0 {
		return wrap(ErrMalformed, "empty SafeContents")
	}
	for i := range bags {
		if err := verifySafeBag(bags[i], opts, report); err != nil {
			return fmt.Errorf("SafeBag[%d]: %w", i, err)
		}
	}
	return nil
}

// verifySafeBag enforces the bag-type allow list and the shrouded-key
// encryption policy. Plaintext keyBag and all weak algorithms are rejected.
func verifySafeBag(bag safeBag, opts Options, report *Report) error {
	for _, attr := range bag.Attributes {
		if err := requireSingleDER(attr.FullBytes); err != nil {
			return wrap(ErrNonCanonicalDER, "invalid SafeBag attribute encoding")
		}
	}

	switch {
	case bag.ID.Equal(oidCertBag):
		var cb certBag
		inner, err := explicitValue(bag.Value)
		if err != nil {
			return wrap(ErrMalformed, "certBag value unwrap failed: "+err.Error())
		}
		if err := unmarshalStrict(inner, &cb); err != nil {
			return wrap(ErrMalformed, "certBag decode failed: "+err.Error())
		}
		if !cb.ID.Equal(oidX509Certificate) {
			return wrap(ErrUnsupportedBag, "only X.509 certificates are accepted in certBag")
		}
		if len(cb.Data) == 0 {
			return wrap(ErrMalformed, "certBag carries no certificate")
		}
		report.CertBagCount++

	case bag.ID.Equal(oidPKCS8ShroudedKBag):
		inner, err := explicitValue(bag.Value)
		if err != nil {
			return wrap(ErrMalformed, "pkcs8ShroudedKeyBag value unwrap failed: "+err.Error())
		}
		if len(inner) == 0 {
			return wrap(ErrMalformed, "pkcs8ShroudedKeyBag value is empty")
		}
		var epki encryptedPrivateKeyInfo
		if err := unmarshalStrict(inner, &epki); err != nil {
			return wrap(ErrMalformed, "EncryptedPrivateKeyInfo decode failed: "+err.Error())
		}
		if len(epki.EncryptedData) == 0 {
			return wrap(ErrMalformed, "shrouded key ciphertext is empty")
		}
		info, err := verifyShroudedAlgorithm(epki.EncryptionAlgorithm, opts)
		if err != nil {
			return err
		}
		report.Ciphers = append(report.Ciphers, info)
		report.ShroudedKeyCount++

	case bag.ID.Equal(oidKeyBag):
		return wrap(ErrUnencryptedKey, "plaintext keyBag is forbidden")

	default:
		return wrap(ErrUnsupportedBag, "unsupported SafeBag OID "+bag.ID.String())
	}
	return nil
}

// explicitValue unwraps a SafeBag bagValue [0] EXPLICIT TLV and returns the
// inner DER bytes.
func explicitValue(v asn1.RawValue) ([]byte, error) {
	if v.Class != asn1.ClassContextSpecific || v.Tag != 0 || !v.IsCompound {
		return nil, wrap(ErrNonCanonicalDER, "expected context-specific [0] EXPLICIT bagValue")
	}
	var inner asn1.RawValue
	rest, err := asn1.Unmarshal(v.Bytes, &inner)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return nil, ErrTrailingBytes
	}
	return inner.FullBytes, nil
}

// verifyShroudedAlgorithm maps the accepted policy result into a CipherInfo.
func verifyShroudedAlgorithm(algo algorithmIdentifier, opts Options) (CipherInfo, error) {
	if !algo.Algorithm.Equal(oidPBES2) {
		if name, weak := weakAlgorithms[algo.Algorithm.String()]; weak {
			return CipherInfo{}, wrap(ErrWeakCipher, "weak shrouded-key algorithm: "+name)
		}
		return CipherInfo{}, wrap(ErrUnsupportedCipher, "shrouded key must use PBES2")
	}
	if err := verifyPBES2(algo, opts); err != nil {
		return CipherInfo{}, err
	}

	var params pbes2Params
	if err := unmarshalStrict(algo.Parameters.FullBytes, &params); err != nil {
		return CipherInfo{}, wrap(ErrMalformed, "PBES2 parameters decode failed")
	}
	var kdf pbkdf2Params
	if err := unmarshalStrict(params.KeyDerivationFunc.Parameters.FullBytes, &kdf); err != nil {
		return CipherInfo{}, wrap(ErrMalformed, "PBKDF2 parameters decode failed")
	}

	info := CipherInfo{KDFIterations: kdf.IterationCount, PRF: kdf.PRF.Algorithm.String()}
	if params.EncryptionScheme.Algorithm.Equal(oidAES256CBC) {
		info.Algorithm = CipherAES256CBC
	} else {
		info.Algorithm = CipherAES256GCM
	}
	return info, nil
}

// verifyEncryptedData policy-checks a PKCS#7 EncryptedData envelope. The
// content-encryption algorithm is held to the same PBES2/AES-256 rules.
func verifyEncryptedData(ci contentInfo, opts Options) error {
	payload, err := explicitOctetString(ci.Content)
	if err != nil {
		return wrap(ErrMalformed, "encryptedData ContentInfo must wrap an OCTET STRING")
	}
	var ed encryptedDataBody
	if err := unmarshalStrict(payload, &ed); err != nil {
		return wrap(ErrMalformed, "EncryptedData decode failed: "+err.Error())
	}
	if ed.Version != 0 {
		return wrap(ErrMalformed, "EncryptedData version must be 0")
	}
	if !ed.EncryptedContentInfo.ContentType.Equal(oidData) {
		return wrap(ErrMalformed, "EncryptedData must protect id-data")
	}
	if len(ed.EncryptedContentInfo.EncryptedContent) == 0 {
		return wrap(ErrMalformed, "EncryptedData ciphertext is empty")
	}
	_, err = verifyShroudedAlgorithm(ed.EncryptedContentInfo.ContentEncryptionAlgorithm, opts)
	return err
}

// explicitOctetString unpacks a [0] EXPLICIT primitive OCTET STRING as produced
// by ContentInfo. It rejects missing wrappers, constructed (BER-style) octet
// strings and any nested trailing bytes.
func explicitOctetString(wrapper asn1.RawValue) ([]byte, error) {
	if wrapper.Class != asn1.ClassContextSpecific || wrapper.Tag != 0 || !wrapper.IsCompound {
		return nil, wrap(ErrNonCanonicalDER, "expected context-specific [0] EXPLICIT wrapper")
	}
	var inner asn1.RawValue
	rest, err := asn1.Unmarshal(wrapper.Bytes, &inner)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return nil, ErrTrailingBytes
	}
	if inner.Class != asn1.ClassUniversal || inner.Tag != asn1.TagOctetString || inner.IsCompound {
		return nil, wrap(ErrNonCanonicalDER, "[0] must wrap a primitive OCTET STRING")
	}
	return inner.Bytes, nil
}

func wrap(base error, msg string) error {
	return fmt.Errorf("%w: %s", base, msg)
}

// IsPolicyError reports whether err (or any wrapped error) is one of this
// package's policy sentinels.
func IsPolicyError(err error) bool {
	return errors.Is(err, ErrMalformed) ||
		errors.Is(err, ErrNonCanonicalDER) ||
		errors.Is(err, ErrTrailingBytes) ||
		errors.Is(err, ErrUnsupportedVersion) ||
		errors.Is(err, ErrMissingMacData) ||
		errors.Is(err, ErrDigestAlgorithm) ||
		errors.Is(err, ErrSaltTooShort) ||
		errors.Is(err, ErrIterations) ||
		errors.Is(err, ErrIntegrity) ||
		errors.Is(err, ErrUnencryptedKey) ||
		errors.Is(err, ErrWeakCipher) ||
		errors.Is(err, ErrUnsupportedCipher) ||
		errors.Is(err, ErrUnsupportedBag)
}
