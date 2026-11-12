package netsec

import (
	"bytes"
	"crypto"
	_ "crypto/md5"
	_ "crypto/sha1"
	_ "crypto/sha256"
	_ "crypto/sha512"
	"crypto/subtle"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

var (
	// ErrMalformed reports DER or RFC 5755 structural errors.
	ErrMalformed = errors.New("netsec: malformed attribute certificate")
	// ErrSignature reports an invalid or unsupported attribute authority signature.
	ErrSignature = errors.New("netsec: attribute certificate signature is not valid")
	// ErrIssuerMismatch reports that the AC issuer does not identify the authority.
	ErrIssuerMismatch = errors.New("netsec: attribute certificate issuer does not match authority")
	// ErrHolderMismatch reports that the Holder constraints do not bind the presented certificate.
	ErrHolderMismatch = errors.New("netsec: attribute certificate holder does not match certificate")
	// ErrExpired reports a time outside the half-open AC validity interval.
	ErrExpired = errors.New("netsec: attribute certificate is outside its validity period")
	// ErrUnknownCriticalExtension reports an unprocessed critical extension.
	ErrUnknownCriticalExtension = errors.New("netsec: unknown critical attribute certificate extension")
	// ErrUnsupportedAlgorithm reports a signature or digest algorithm that cannot be checked.
	ErrUnsupportedAlgorithm = errors.New("netsec: unsupported algorithm")
)

var (
	oidSHA1   = asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}
	oidMD5    = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 5}
	oidSHA224 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 4}
	oidSHA256 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidSHA384 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 2}
	oidSHA512 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 3}

	oidSubjectAltName = asn1.ObjectIdentifier{2, 5, 29, 17}
	oidEmailAddress   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 1}
)

var knownCriticalExtensions = map[string]struct{}{
	// RFC 5280 extensions that can appear in an attribute certificate.
	"2.5.29.9":  {},
	"2.5.29.14": {},
	"2.5.29.15": {},
	"2.5.29.16": {},
	"2.5.29.17": {},
	"2.5.29.18": {},
	"2.5.29.19": {},
	"2.5.29.30": {},
	"2.5.29.31": {},
	"2.5.29.32": {},
	"2.5.29.33": {},
	"2.5.29.35": {},
	"2.5.29.36": {},
	"2.5.29.37": {},
	"2.5.29.46": {},
	"2.5.29.54": {},
	"2.5.29.56": {},

	// id-pe attribute-certificate extensions (RFC 5755 and its predecessors).
	"1.3.6.1.5.5.7.1.1":  {},
	"1.3.6.1.5.5.7.1.2":  {},
	"1.3.6.1.5.5.7.1.3":  {},
	"1.3.6.1.5.5.7.1.4":  {},
	"1.3.6.1.5.5.7.1.5":  {},
	"1.3.6.1.5.5.7.1.6":  {},
	"1.3.6.1.5.5.7.1.7":  {},
	"1.3.6.1.5.5.7.1.8":  {},
	"1.3.6.1.5.5.7.1.9":  {},
	"1.3.6.1.5.5.7.1.10": {},
	"1.3.6.1.5.5.7.1.11": {},
	"1.3.6.1.5.5.7.1.12": {},
}

// Verify fully verifies an RFC 5755 attribute certificate. ac may be a
// DER-encoded []byte or a parsed *AttributeCertificate. holder and authority
// may be *x509.Certificate values or their DER encodings. The authority
// certificate supplies the attribute authority's public key and expected
// issuer name. If at is omitted, time.Now is used. The validity interval is
// half-open: notBefore is valid, notAfter is not.
func Verify(ac, holder, authority interface{}, at ...time.Time) error {
	now, err := verificationTime(at)
	if err != nil {
		return err
	}
	parsed, err := normalizeAttributeCertificate(ac)
	if err != nil {
		return err
	}
	holderCert, err := normalizeCertificate(holder, "holder")
	if err != nil {
		return err
	}
	authorityCert, err := normalizeCertificate(authority, "authority")
	if err != nil {
		return err
	}
	if holderCert == nil {
		return fmt.Errorf("%w: holder certificate is required", ErrHolderMismatch)
	}
	if authorityCert == nil {
		return fmt.Errorf("%w: authority certificate is required", ErrSignature)
	}
	// Accept the two certificate arguments in either order: the AC issuer
	// identifies which one is the authority.
	if authorityCert != holderCert &&
		parsed.issuerMatches(holderCert) && !parsed.issuerMatches(authorityCert) {
		holderCert, authorityCert = authorityCert, holderCert
	}
	return parsed.Verify(holderCert, authorityCert, now)
}

// VerifyAttributeCertificate is an alias for Verify.
func VerifyAttributeCertificate(ac, holder, authority interface{}, at ...time.Time) error {
	return Verify(ac, holder, authority, at...)
}

// VerifyAC is an alias for Verify.
func VerifyAC(ac, holder, authority interface{}, at ...time.Time) error {
	return Verify(ac, holder, authority, at...)
}

// VerifyAttributeCert is an alias for Verify.
func VerifyAttributeCert(ac, holder, authority interface{}, at ...time.Time) error {
	return Verify(ac, holder, authority, at...)
}

// VerifyCertificates is the statically typed form of Verify.
func VerifyCertificates(acDER []byte, holder, authority *x509.Certificate, at ...time.Time) error {
	return Verify(acDER, holder, authority, at...)
}

func normalizeAttributeCertificate(value interface{}) (*AttributeCertificate, error) {
	switch v := value.(type) {
	case *AttributeCertificate:
		if v == nil {
			return nil, fmt.Errorf("%w: nil attribute certificate", ErrMalformed)
		}
		return v, nil
	case []byte:
		return ParseAttributeCertificate(v)
	case nil:
		return nil, fmt.Errorf("%w: attribute certificate is required", ErrMalformed)
	default:
		return nil, fmt.Errorf("%w: unsupported attribute certificate type %T", ErrMalformed, value)
	}
}

func normalizeCertificate(value interface{}, what string) (*x509.Certificate, error) {
	switch v := value.(type) {
	case *x509.Certificate:
		return v, nil
	case []byte:
		cert, err := x509.ParseCertificate(v)
		if err != nil {
			return nil, fmt.Errorf("%w: parse %s certificate: %v", ErrMalformed, what, err)
		}
		return cert, nil
	case nil:
		return nil, nil
	default:
		return nil, fmt.Errorf("%w: unsupported %s certificate type %T", ErrMalformed, what, value)
	}
}

func verificationTime(at []time.Time) (time.Time, error) {
	switch len(at) {
	case 0:
		return time.Now(), nil
	case 1:
		return at[0], nil
	default:
		return time.Time{}, fmt.Errorf("%w: too many verification times", ErrMalformed)
	}
}

// Verify checks the attribute authority signature, issuer binding, validity
// period, critical extensions, and all Holder constraints against holder.
func (ac *AttributeCertificate) Verify(holder, authority *x509.Certificate, at time.Time) error {
	if ac == nil {
		return fmt.Errorf("%w: nil attribute certificate", ErrMalformed)
	}
	if holder == nil {
		return fmt.Errorf("%w: holder certificate is required", ErrHolderMismatch)
	}
	if authority == nil {
		return fmt.Errorf("%w: authority certificate is required", ErrSignature)
	}

	if err := ac.VerifySignature(authority); err != nil {
		return err
	}
	if err := ac.CheckExtensions(); err != nil {
		return err
	}
	if err := ac.CheckValidity(at); err != nil {
		return err
	}
	if err := ac.checkIssuer(authority); err != nil {
		return err
	}
	if err := ac.checkHolder(holder); err != nil {
		return err
	}
	return nil
}

// VerifySignature checks only the attribute authority signature, including
// the inner/outer signature algorithm consistency.
func (ac *AttributeCertificate) VerifySignature(authority *x509.Certificate) error {
	if ac == nil {
		return fmt.Errorf("%w: nil attribute certificate", ErrMalformed)
	}
	if authority == nil {
		return fmt.Errorf("%w: authority certificate is required", ErrSignature)
	}
	if !ac.SignatureAlgorithm.Algorithm.Equal(ac.innerAlg.Algorithm) {
		return fmt.Errorf("%w: inner and outer signature algorithms differ", ErrSignature)
	}
	algorithm, err := x509SignatureAlgorithm(ac.SignatureAlgorithm)
	if err != nil {
		return err
	}
	if err := authority.CheckSignature(algorithm, ac.signedDER, ac.signature); err != nil {
		return fmt.Errorf("%w: %v", ErrSignature, err)
	}
	return nil
}

// CheckSignature is an alias for VerifySignature.
func (ac *AttributeCertificate) CheckSignature(authority *x509.Certificate) error {
	return ac.VerifySignature(authority)
}

// VerifySignature is the package-level form of
// AttributeCertificate.VerifySignature.
func VerifySignature(ac, authority interface{}) error {
	parsed, err := normalizeAttributeCertificate(ac)
	if err != nil {
		return err
	}
	authorityCert, err := normalizeCertificate(authority, "authority")
	if err != nil {
		return err
	}
	if authorityCert == nil {
		return fmt.Errorf("%w: authority certificate is required", ErrSignature)
	}
	return parsed.VerifySignature(authorityCert)
}

// CheckValidity enforces the RFC 5755 half-open validity interval. A
// certificate is valid at notBefore and invalid at notAfter.
func (ac *AttributeCertificate) CheckValidity(at time.Time) error {
	if ac == nil {
		return fmt.Errorf("%w: nil attribute certificate", ErrMalformed)
	}
	if at.Before(ac.NotBefore) || !at.Before(ac.NotAfter) {
		return fmt.Errorf("%w: %s is outside [%s, %s)",
			ErrExpired, at.UTC().Format(time.RFC3339Nano),
			ac.NotBefore.UTC().Format(time.RFC3339Nano),
			ac.NotAfter.UTC().Format(time.RFC3339Nano))
	}
	return nil
}

// CheckExtensions rejects critical extensions whose semantics this package
// does not recognize. Unknown non-critical extensions are ignored.
func (ac *AttributeCertificate) CheckExtensions() error {
	if ac == nil {
		return fmt.Errorf("%w: nil attribute certificate", ErrMalformed)
	}
	for _, extension := range ac.Extensions {
		if !extension.Critical {
			continue
		}
		if _, ok := knownCriticalExtensions[extension.ID.String()]; !ok {
			return fmt.Errorf("%w: %s", ErrUnknownCriticalExtension, extension.ID.String())
		}
	}
	return nil
}

// CheckHolder verifies only the Holder binding. Signature and issuer
// verification remain the caller's responsibility.
func (ac *AttributeCertificate) CheckHolder(holder *x509.Certificate) error {
	return ac.checkHolder(holder)
}

// VerifyHolder is a package-level form of AttributeCertificate.VerifyHolder.
// ac and holder may be DER encodings or parsed values. If at is supplied, the
// half-open validity period is checked as well.
func VerifyHolder(ac, holder interface{}, at ...time.Time) error {
	parsed, err := normalizeAttributeCertificate(ac)
	if err != nil {
		return err
	}
	holderCert, err := normalizeCertificate(holder, "holder")
	if err != nil {
		return err
	}
	return parsed.VerifyHolder(holderCert, at...)
}

// VerifyHolder verifies the Holder binding. If at is supplied, the half-open
// validity period is checked as well.
func (ac *AttributeCertificate) VerifyHolder(holder *x509.Certificate, at ...time.Time) error {
	if err := ac.checkHolder(holder); err != nil {
		return err
	}
	if len(at) == 0 {
		return nil
	}
	if len(at) > 1 {
		return fmt.Errorf("%w: too many verification times", ErrMalformed)
	}
	return ac.CheckValidity(at[0])
}

// CheckHolder is a package-level alias for VerifyHolder.
func CheckHolder(ac, holder interface{}, at ...time.Time) error {
	return VerifyHolder(ac, holder, at...)
}

func (ac *AttributeCertificate) checkHolder(holder *x509.Certificate) error {
	if ac == nil {
		return fmt.Errorf("%w: nil attribute certificate", ErrMalformed)
	}
	if holder == nil {
		return fmt.Errorf("%w: holder certificate is required", ErrHolderMismatch)
	}

	h := ac.Holder
	if h.BaseCertificateID == nil && h.EntityName == nil && h.ObjectDigestInfo == nil {
		return fmt.Errorf("%w: Holder contains no binding", ErrHolderMismatch)
	}
	if h.BaseCertificateID != nil {
		if err := checkIssuerSerial(h.BaseCertificateID, certificateRawIssuer(holder), holder.SerialNumber); err != nil {
			return fmt.Errorf("%w: baseCertificateID: %v", ErrHolderMismatch, err)
		}
	}
	if h.EntityName != nil {
		for _, name := range h.EntityName {
			if !nameMatchesCertificate(name, holder) {
				return fmt.Errorf("%w: entityName %d is not present in the certificate", ErrHolderMismatch, name.Type)
			}
		}
	}
	if h.ObjectDigestInfo != nil {
		if err := checkObjectDigestInfo(h.ObjectDigestInfo, holder); err != nil {
			return fmt.Errorf("%w: objectDigestInfo: %v", ErrHolderMismatch, err)
		}
	}
	return nil
}

func (ac *AttributeCertificate) checkIssuer(authority *x509.Certificate) error {
	if ac.Issuer.V2Form == nil {
		if len(ac.Issuer.V1Form) == 0 {
			return fmt.Errorf("%w: empty issuer", ErrIssuerMismatch)
		}
		if certificateSubjectPresent(authority) && !namesMatchCertificate(ac.Issuer.V1Form, authority) {
			return fmt.Errorf("%w: v1Form issuerName", ErrIssuerMismatch)
		}
		return nil
	}

	v2 := ac.Issuer.V2Form
	if v2.IssuerName == nil && v2.BaseCertificateID == nil && v2.ObjectDigestInfo == nil {
		return fmt.Errorf("%w: empty v2Form", ErrIssuerMismatch)
	}
	if v2.IssuerName != nil && certificateSubjectPresent(authority) &&
		!namesMatchCertificate(v2.IssuerName, authority) {
		return fmt.Errorf("%w: v2Form issuerName", ErrIssuerMismatch)
	}
	if v2.BaseCertificateID != nil {
		if err := checkIssuerSerial(v2.BaseCertificateID, certificateRawIssuer(authority), authority.SerialNumber); err != nil {
			return fmt.Errorf("%w: v2Form baseCertificateID: %v", ErrIssuerMismatch, err)
		}
	}
	if v2.ObjectDigestInfo != nil {
		if err := checkObjectDigestInfo(v2.ObjectDigestInfo, authority); err != nil {
			return fmt.Errorf("%w: v2Form objectDigestInfo: %v", ErrIssuerMismatch, err)
		}
	}
	return nil
}

func (ac *AttributeCertificate) issuerMatches(authority *x509.Certificate) bool {
	return ac != nil && authority != nil && ac.checkIssuer(authority) == nil
}

func namesMatchCertificate(names GeneralNames, cert *x509.Certificate) bool {
	if len(names) == 0 {
		return false
	}
	for _, name := range names {
		if !nameMatchesCertificate(name, cert) {
			return false
		}
	}
	return true
}

func checkIssuerSerial(is *IssuerSerial, issuer []byte, serial *big.Int) error {
	if is == nil {
		return fmt.Errorf("missing IssuerSerial")
	}
	if is.Serial == nil || serial == nil || is.Serial.Cmp(serial) != 0 {
		return fmt.Errorf("serial number does not match")
	}
	if len(is.Issuer) == 0 {
		return fmt.Errorf("issuer name is missing")
	}
	for _, name := range is.Issuer {
		if name.Type != GeneralNameDirectoryName {
			return fmt.Errorf("issuer contains a non-directoryName GeneralName")
		}
		if !distinguishedNamesEqual(name.DirectoryName, issuer) {
			return fmt.Errorf("issuer name does not match")
		}
	}
	if len(is.IssuerUID) != 0 {
		// Go's x509.Certificate does not expose issuer unique identifiers.
		// Fail closed rather than ignoring a stated binding constraint.
		return fmt.Errorf("issuerUID cannot be checked")
	}
	return nil
}

func nameMatchesCertificate(name GeneralName, cert *x509.Certificate) bool {
	if cert == nil {
		return false
	}
	sans := certificateGeneralNames(cert)

	switch name.Type {
	case GeneralNameDirectoryName:
		if distinguishedNamesEqual(name.DirectoryName, certificateRawSubject(cert)) {
			return true
		}
		for _, san := range sans {
			if san.Type == GeneralNameDirectoryName &&
				distinguishedNamesEqual(name.DirectoryName, san.DirectoryName) {
				return true
			}
		}
	case GeneralNameRFC822Name:
		for _, email := range cert.EmailAddresses {
			if stringEqual(name.StringValue, email, true) {
				return true
			}
		}
		for _, subjectName := range cert.Subject.Names {
			if subjectName.Type.Equal(oidEmailAddress) {
				if email, ok := subjectName.Value.(string); ok &&
					stringEqual(name.StringValue, email, true) {
					return true
				}
			}
		}
		for _, san := range sans {
			if san.Type == GeneralNameRFC822Name &&
				stringEqual(name.StringValue, san.StringValue, true) {
				return true
			}
		}
	case GeneralNameDNSName:
		for _, dns := range cert.DNSNames {
			if stringEqual(name.StringValue, dns, true) {
				return true
			}
		}
		for _, san := range sans {
			if san.Type == GeneralNameDNSName &&
				stringEqual(name.StringValue, san.StringValue, true) {
				return true
			}
		}
	case GeneralNameURI:
		for _, uri := range cert.URIs {
			if stringEqual(name.StringValue, uri.String(), false) {
				return true
			}
		}
		for _, san := range sans {
			if san.Type == GeneralNameURI &&
				stringEqual(name.StringValue, san.StringValue, false) {
				return true
			}
		}
	case GeneralNameIPAddress:
		for _, ip := range cert.IPAddresses {
			if ip.Equal(name.IP) {
				return true
			}
		}
		for _, san := range sans {
			if san.Type == GeneralNameIPAddress && san.IP.Equal(name.IP) {
				return true
			}
		}
	case GeneralNameRegisteredID:
		for _, san := range sans {
			if san.Type == GeneralNameRegisteredID && san.OID.Equal(name.OID) {
				return true
			}
		}
	case GeneralNameOtherName:
		for _, san := range sans {
			if san.Type == GeneralNameOtherName && san.OtherName != nil &&
				bytes.Equal(name.OtherName, san.OtherName) {
				return true
			}
		}
	}
	return false
}

func certificateGeneralNames(cert *x509.Certificate) GeneralNames {
	var names GeneralNames
	for _, extension := range cert.Extensions {
		if !extension.Id.Equal(oidSubjectAltName) {
			continue
		}
		root, err := parseDER(extension.Value)
		if err != nil || !root.isUniversal(derTagSequence) {
			continue
		}
		parsed, err := parseGeneralNames(root)
		if err != nil {
			continue
		}
		names = append(names, parsed...)
	}
	return names
}

func certificateRawIssuer(cert *x509.Certificate) []byte {
	if cert == nil {
		return nil
	}
	if len(cert.RawIssuer) != 0 {
		return cert.RawIssuer
	}
	der, err := asn1.Marshal(cert.Issuer.ToRDNSequence())
	if err != nil {
		return nil
	}
	return der
}

func certificateRawSubject(cert *x509.Certificate) []byte {
	if cert == nil {
		return nil
	}
	if len(cert.RawSubject) != 0 {
		return cert.RawSubject
	}
	der, err := asn1.Marshal(cert.Subject.ToRDNSequence())
	if err != nil {
		return nil
	}
	return der
}

func certificateSubjectPresent(cert *x509.Certificate) bool {
	raw := certificateRawSubject(cert)
	return len(raw) > 2
}

func distinguishedNamesEqual(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	if bytes.Equal(a, b) {
		return true
	}

	var rdnA, rdnB pkix.RDNSequence
	if rest, err := asn1.Unmarshal(a, &rdnA); err != nil || len(rest) != 0 {
		return false
	}
	if rest, err := asn1.Unmarshal(b, &rdnB); err != nil || len(rest) != 0 {
		return false
	}
	var nameA, nameB pkix.Name
	nameA.FillFromRDNSequence(&rdnA)
	nameB.FillFromRDNSequence(&rdnB)
	return strings.EqualFold(nameA.String(), nameB.String())
}

func checkObjectDigestInfo(info *ObjectDigestInfo, cert *x509.Certificate) error {
	if info == nil {
		return fmt.Errorf("missing ObjectDigestInfo")
	}
	var object []byte
	switch info.DigestedObjectType {
	case DigestedObjectPublicKey:
		object = cert.RawSubjectPublicKeyInfo
		if len(object) == 0 {
			var err error
			object, err = x509.MarshalPKIXPublicKey(cert.PublicKey)
			if err != nil {
				return fmt.Errorf("public key is unavailable: %v", err)
			}
		}
	case DigestedObjectPublicKeyCert:
		object = cert.Raw
	case DigestedObjectOther:
		return fmt.Errorf("%w: otherObjectTypes is not supported", ErrUnsupportedAlgorithm)
	default:
		return fmt.Errorf("unknown digestedObjectType %d", info.DigestedObjectType)
	}
	if len(object) == 0 {
		return fmt.Errorf("digested object is unavailable")
	}
	hash, err := digestAlgorithm(info.DigestAlgorithm.Algorithm)
	if err != nil {
		return err
	}
	h := hash.New()
	h.Write(object)
	digest := h.Sum(nil)
	if len(info.ObjectDigest) != len(digest) ||
		subtle.ConstantTimeCompare(info.ObjectDigest, digest) != 1 {
		return fmt.Errorf("object digest does not match")
	}
	return nil
}

func digestAlgorithm(oid asn1.ObjectIdentifier) (crypto.Hash, error) {
	switch {
	case oid.Equal(oidMD5):
		return crypto.MD5, nil
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
	default:
		return 0, fmt.Errorf("%w: digest %s", ErrUnsupportedAlgorithm, oid.String())
	}
}

func x509SignatureAlgorithm(alg pkix.AlgorithmIdentifier) (x509.SignatureAlgorithm, error) {
	oid := alg.Algorithm
	switch {
	case oid.Equal(asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 4}):
		return x509.MD5WithRSA, nil
	case oid.Equal(asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 5}):
		return x509.SHA1WithRSA, nil
	case oid.Equal(asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 14}):
		return x509.UnknownSignatureAlgorithm,
			fmt.Errorf("%w: SHA-224 with RSA is not supported by crypto/x509", ErrUnsupportedAlgorithm)
	case oid.Equal(asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 11}):
		return x509.SHA256WithRSA, nil
	case oid.Equal(asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 12}):
		return x509.SHA384WithRSA, nil
	case oid.Equal(asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 13}):
		return x509.SHA512WithRSA, nil
	case oid.Equal(asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 10}):
		return x509RSAPSSAlgorithm(alg.Parameters)
	case oid.Equal(asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 1}):
		return x509.ECDSAWithSHA1, nil
	case oid.Equal(asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 2}):
		return x509.ECDSAWithSHA256, nil
	case oid.Equal(asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 3}):
		return x509.ECDSAWithSHA384, nil
	case oid.Equal(asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 4}):
		return x509.ECDSAWithSHA512, nil
	case oid.Equal(asn1.ObjectIdentifier{1, 2, 840, 10040, 4, 3}):
		return x509.DSAWithSHA1, nil
	case oid.Equal(asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 3, 2}):
		return x509.DSAWithSHA256, nil
	case oid.Equal(asn1.ObjectIdentifier{1, 3, 101, 112}):
		return x509.PureEd25519, nil
	default:
		return x509.UnknownSignatureAlgorithm,
			fmt.Errorf("%w: signature %s", ErrUnsupportedAlgorithm, oid.String())
	}
}

func x509RSAPSSAlgorithm(parameters asn1.RawValue) (x509.SignatureAlgorithm, error) {
	// RSASSA-PSS carries the hash in explicit [0]. Omitted fields default to
	// SHA-1, for which crypto/x509 has no PSS verification constant.
	var params struct {
		Hash pkix.AlgorithmIdentifier `asn1:"optional,explicit,tag:0"`
	}
	if len(parameters.FullBytes) == 0 {
		return x509.UnknownSignatureAlgorithm,
			fmt.Errorf("%w: default SHA-1 RSASSA-PSS parameters are not supported", ErrUnsupportedAlgorithm)
	}
	if rest, err := asn1.Unmarshal(parameters.FullBytes, &params); err != nil || len(rest) != 0 {
		return x509.UnknownSignatureAlgorithm,
			fmt.Errorf("%w: invalid RSASSA-PSS parameters", ErrUnsupportedAlgorithm)
	}
	switch {
	case params.Hash.Algorithm.Equal(oidSHA256):
		return x509.SHA256WithRSAPSS, nil
	case params.Hash.Algorithm.Equal(oidSHA384):
		return x509.SHA384WithRSAPSS, nil
	case params.Hash.Algorithm.Equal(oidSHA512):
		return x509.SHA512WithRSAPSS, nil
	default:
		return x509.UnknownSignatureAlgorithm,
			fmt.Errorf("%w: unsupported RSASSA-PSS hash %s", ErrUnsupportedAlgorithm, params.Hash.Algorithm.String())
	}
}
