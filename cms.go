package rpki

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"

	"crypto/x509/pkix"
)

var (
	ErrCMS          = errors.New("rpki: CMS validation failed")
	ErrCMSSignature = errors.New("rpki: CMS signature invalid")
)

// CMSSigned is the verified signed content of an RPKI CMS object.
type CMSSigned struct {
	ContentType     asn1.ObjectIdentifier
	Content         []byte
	Signer          *CertInfo
	Certificates    []*CertInfo
	SignedAttrs     bool
	DigestAlgorithm asn1.ObjectIdentifier
}

type contentInfoWire struct {
	ContentType asn1.ObjectIdentifier
	Content     asn1.RawValue
}

type signedDataWire struct {
	Version          int
	DigestAlgorithms asn1.RawValue
	EncapContent     encapContentInfoWire
	Certificates     asn1.RawValue `asn1:"optional,tag:0,context"`
	CRLs             asn1.RawValue `asn1:"optional,tag:1,context"`
	SignerInfos      asn1.RawValue
}

type encapContentInfoWire struct {
	ContentType asn1.ObjectIdentifier
	EContent    []byte `asn1:"explicit,optional,tag:0"`
}

type signerInfoWire struct {
	Version         int
	SID             asn1.RawValue
	DigestAlgorithm pkix.AlgorithmIdentifier
	SignedAttrs     asn1.RawValue `asn1:"optional,tag:0,context"`
	SignatureAlg    pkix.AlgorithmIdentifier
	Signature       []byte
	UnsignedAttrs   asn1.RawValue `asn1:"optional,tag:1,context"`
}

type attributeWire struct {
	Type   asn1.ObjectIdentifier
	Values asn1.RawValue `asn1:"set"`
}

// ParseAndVerifyCMS parses CMS SignedData and verifies its cryptographic signature.
// Certificate chain, revocation, validity, and resources are checked separately.
func ParseAndVerifyCMS(der []byte) (*CMSSigned, error) {
	var ci contentInfoWire
	rest, err := asn1.Unmarshal(der, &ci)
	if err != nil {
		return nil, fmt.Errorf("%w: ContentInfo: %v", ErrCMS, err)
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("%w: trailing ContentInfo bytes", ErrCMS)
	}
	if !ci.ContentType.Equal(oidSignedData) {
		return nil, fmt.Errorf("%w: unexpected CMS contentType %s", ErrCMS, ci.ContentType)
	}
	var sd signedDataWire
	rest, err = asn1.Unmarshal(ci.Content.Bytes, &sd)
	if err != nil {
		return nil, fmt.Errorf("%w: SignedData: %v", ErrCMS, err)
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("%w: trailing SignedData bytes", ErrCMS)
	}
	if sd.EncapContent.ContentType == nil {
		return nil, fmt.Errorf("%w: missing encapsulated content type", ErrCMS)
	}

	certs, err := parseCMSCertificates(sd.Certificates)
	if err != nil {
		return nil, err
	}
	signerInfos, err := unmarshalChildren(sd.SignerInfos.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: signerInfos SET: %v", ErrCMS, err)
	}
	if len(signerInfos) != 1 {
		return nil, fmt.Errorf("%w: exactly one SignerInfo required, got %d", ErrCMS, len(signerInfos))
	}
	var si signerInfoWire
	if _, err := asn1.Unmarshal(signerInfos[0].FullBytes, &si); err != nil {
		return nil, fmt.Errorf("%w: SignerInfo: %v", ErrCMS, err)
	}
	signer, err := findSignerCertificate(certs, si.SID)
	if err != nil {
		return nil, err
	}
	if err := verifySignerInfo(&si, sd.EncapContent.EContent, sd.EncapContent.ContentType, signer); err != nil {
		return nil, err
	}
	return &CMSSigned{
		ContentType:     sd.EncapContent.ContentType,
		Content:         sd.EncapContent.EContent,
		Signer:          signer,
		Certificates:    certs,
		SignedAttrs:     len(si.SignedAttrs.FullBytes) != 0,
		DigestAlgorithm: si.DigestAlgorithm.Algorithm,
	}, nil
}

func parseCMSCertificates(raw asn1.RawValue) ([]*CertInfo, error) {
	if len(raw.Bytes) == 0 {
		return nil, fmt.Errorf("%w: SignedData contains no certificates", ErrCMS)
	}
	children, err := unmarshalChildren(raw.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: certificates: %v", ErrCMS, err)
	}
	var certs []*CertInfo
	for _, child := range children {
		cert, err := ParseCertificate(child.FullBytes)
		if err != nil {
			return nil, err
		}
		certs = append(certs, cert)
	}
	if len(certs) == 0 {
		return nil, fmt.Errorf("%w: SignedData contains no parseable certificates", ErrCMS)
	}
	return certs, nil
}

func findSignerCertificate(certs []*CertInfo, sid asn1.RawValue) (*CertInfo, error) {
	if sid.Class == asn1.ClassContextSpecific && sid.Tag == 0 {
		ski := sid.Bytes
		for _, cert := range certs {
			if bytesEqual(cert.X509.SubjectKeyId, ski) {
				return cert, nil
			}
		}
		return nil, fmt.Errorf("%w: no certificate matches SKI %x", ErrCMS, ski)
	}
	var issuerSerial struct {
		Issuer asn1.RawValue
		Serial *big.Int
	}
	if _, err := asn1.Unmarshal(sid.FullBytes, &issuerSerial); err != nil {
		return nil, fmt.Errorf("%w: SignerIdentifier: %v", ErrCMS, err)
	}
	for _, cert := range certs {
		if bytesEqual(cert.X509.RawIssuer, issuerSerial.Issuer.FullBytes) && issuerSerial.Serial.Cmp(cert.X509.SerialNumber) == 0 {
			return cert, nil
		}
	}
	return nil, fmt.Errorf("%w: no certificate matches issuerAndSerial %s", ErrCMS, issuerSerial.Serial)
}

func verifySignerInfo(si *signerInfoWire, eContent []byte, contentType asn1.ObjectIdentifier, signer *CertInfo) error {
	if !si.DigestAlgorithm.Algorithm.Equal(oidSHA256) {
		return fmt.Errorf("%w: unsupported digest %s", ErrCMS, si.DigestAlgorithm.Algorithm)
	}
	pub, ok := signer.X509.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve == nil || pub.Curve.Params().Name != "P-256" {
		return fmt.Errorf("%w: signer key is not ECDSA P-256", ErrCMS)
	}

	var signedDigest []byte
	if len(si.SignedAttrs.FullBytes) != 0 {
		attrsDER, err := signedAttrsAsSetDER(si.SignedAttrs)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(attrsDER)
		signedDigest = digest[:]
		if err := checkSignedAttributes(si.SignedAttrs.Bytes, eContent, contentType); err != nil {
			return err
		}
	} else {
		digest := sha256.Sum256(eContent)
		signedDigest = digest[:]
	}
	if !ecdsa.VerifyASN1(pub, signedDigest, si.Signature) {
		return fmt.Errorf("%w: ECDSA-P256 verification failed", ErrCMSSignature)
	}
	return nil
}

func signedAttrsAsSetDER(raw asn1.RawValue) ([]byte, error) {
	set := asn1.RawValue{
		Class:      asn1.ClassUniversal,
		Tag:        asn1.TagSet,
		IsCompound: true,
		Bytes:      raw.Bytes,
	}
	der, err := asn1.Marshal(set)
	if err != nil {
		return nil, fmt.Errorf("%w: re-encode signedAttrs: %v", ErrCMS, err)
	}
	return der, nil
}

func checkSignedAttributes(content []byte, eContent []byte, contentType asn1.ObjectIdentifier) error {
	children, err := unmarshalChildren(content)
	if err != nil {
		return fmt.Errorf("%w: signedAttrs: %v", ErrCMS, err)
	}
	var foundContentType, foundMessageDigest bool
	for _, child := range children {
		var attr attributeWire
		if _, err := asn1.Unmarshal(child.FullBytes, &attr); err != nil {
			return fmt.Errorf("%w: Attribute: %v", ErrCMS, err)
		}
		switch {
		case attr.Type.Equal(oidContentType):
			foundContentType = true
			oids, err := decodeObjectIdentifierSet(attr.Values)
			if err != nil || len(oids) != 1 || !oids[0].Equal(contentType) {
				return fmt.Errorf("%w: invalid content-type signed attribute", ErrCMS)
			}
		case attr.Type.Equal(oidMessageDigest):
			foundMessageDigest = true
			digest, err := decodeOctetStringSet(attr.Values)
			if err != nil || len(digest) != sha256.Size {
				return fmt.Errorf("%w: invalid message-digest signed attribute", ErrCMS)
			}
			actual := sha256.Sum256(eContent)
			if !bytesEqual(digest, actual[:]) {
				return fmt.Errorf("%w: message-digest does not match eContent", ErrCMS)
			}
		}
	}
	if !foundContentType {
		return fmt.Errorf("%w: signedAttrs missing content-type", ErrCMS)
	}
	if !foundMessageDigest {
		return fmt.Errorf("%w: signedAttrs missing message-digest", ErrCMS)
	}
	return nil
}

func decodeObjectIdentifierSet(raw asn1.RawValue) ([]asn1.ObjectIdentifier, error) {
	children, err := unmarshalChildren(raw.Bytes)
	if err != nil {
		return nil, err
	}
	var out []asn1.ObjectIdentifier
	for _, child := range children {
		var oid asn1.ObjectIdentifier
		if _, err := asn1.Unmarshal(child.FullBytes, &oid); err != nil {
			return nil, err
		}
		out = append(out, oid)
	}
	return out, nil
}

func decodeOctetStringSet(raw asn1.RawValue) ([]byte, error) {
	children, err := unmarshalChildren(raw.Bytes)
	if err != nil {
		return nil, err
	}
	if len(children) != 1 {
		return nil, fmt.Errorf("%w: expected one OCTET STRING in SET, got %d", ErrCMS, len(children))
	}
	var value []byte
	if _, err := asn1.Unmarshal(children[0].FullBytes, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
