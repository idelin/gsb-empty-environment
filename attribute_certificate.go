package netsec

import (
	"crypto/x509/pkix"
	"encoding/asn1"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"
)

// GeneralName CHOICE alternatives from RFC 5280.
const (
	GeneralNameOtherName     = 0
	GeneralNameRFC822Name    = 1
	GeneralNameDNSName       = 2
	GeneralNameX400Address   = 3
	GeneralNameDirectoryName = 4
	GeneralNameEDIPartyName  = 5
	GeneralNameURI           = 6
	GeneralNameIPAddress     = 7
	GeneralNameRegisteredID  = 8
)

// ObjectDigestInfo digestedObjectType values from RFC 5755.
const (
	DigestedObjectPublicKey     = 0
	DigestedObjectPublicKeyCert = 1
	DigestedObjectOther         = 2
)

// Holder is the RFC 5755 Holder structure. At least one member must be
// present; every member that is present is a binding constraint.
type Holder struct {
	BaseCertificateID *IssuerSerial
	EntityName        GeneralNames
	ObjectDigestInfo  *ObjectDigestInfo
}

// IssuerSerial binds a holder or an attribute authority to a certificate
// issuer and serial number.
type IssuerSerial struct {
	Issuer    GeneralNames
	Serial    *big.Int
	IssuerUID []byte
}

// GeneralName is a parsed GeneralName. Exactly one of the value fields is
// meaningful for a given Type.
type GeneralName struct {
	Type          int
	StringValue   string
	DirectoryName []byte
	IP            net.IP
	OID           asn1.ObjectIdentifier
	OtherName     []byte
	Raw           []byte
}

// GeneralNames is a non-empty RFC 5280 GeneralNames sequence.
type GeneralNames []GeneralName

// ObjectDigestInfo binds a holder by a digest of its public key, certificate,
// or another object.
type ObjectDigestInfo struct {
	DigestedObjectType int
	OtherObjectTypeID  asn1.ObjectIdentifier
	DigestAlgorithm    pkix.AlgorithmIdentifier
	ObjectDigest       []byte
}

// V2Form is the RFC 5755 v2Form attribute certificate issuer.
type V2Form struct {
	IssuerName        GeneralNames
	BaseCertificateID *IssuerSerial
	ObjectDigestInfo  *ObjectDigestInfo
}

// AttCertIssuer is the RFC 5755 AttCertIssuer CHOICE. Exactly one of V1Form
// and V2Form is set.
type AttCertIssuer struct {
	V1Form GeneralNames
	V2Form *V2Form
}

// Attribute is one attribute from the attribute certificate.
type Attribute struct {
	Type   asn1.ObjectIdentifier
	Values []asn1.RawValue
}

// Extension is an X.509 extension carried in an attribute certificate.
type Extension struct {
	ID       asn1.ObjectIdentifier
	Critical bool
	Value    []byte
}

// AttributeCertificate is a parsed RFC 5755 AttributeCertificate.
type AttributeCertificate struct {
	Raw                []byte
	Version            int
	Holder             Holder
	Issuer             AttCertIssuer
	SignatureAlgorithm pkix.AlgorithmIdentifier
	SerialNumber       *big.Int
	NotBefore          time.Time
	NotAfter           time.Time
	Attributes         []Attribute
	Extensions         []Extension

	signedDER []byte
	innerAlg  pkix.AlgorithmIdentifier
	signature []byte
}

// AttributeCert is a short alias for AttributeCertificate.
type AttributeCert = AttributeCertificate

// AC is a short alias for AttributeCertificate.
type AC = AttributeCertificate

func parseAttributeCertificateInfo(n derNode) (*AttributeCertificate, error) {
	if err := n.requireUniversal(derTagSequence, "AttributeCertificateInfo"); err != nil {
		return nil, err
	}
	fields, err := n.children()
	if err != nil {
		return nil, err
	}
	if len(fields) != 7 && len(fields) != 8 {
		return nil, fmt.Errorf("%w: AttributeCertificateInfo has %d fields", ErrMalformed, len(fields))
	}

	version, err := parseBigInt(fields[0], "attribute certificate version")
	if err != nil {
		return nil, err
	}
	if !version.IsInt64() || version.Int64() != 1 {
		return nil, fmt.Errorf("%w: unsupported attribute certificate version", ErrMalformed)
	}

	holder, err := parseHolder(fields[1])
	if err != nil {
		return nil, err
	}
	issuer, err := parseAttCertIssuer(fields[2])
	if err != nil {
		return nil, err
	}
	innerAlg, err := parseAlgorithmIdentifier(fields[3], "signature")
	if err != nil {
		return nil, err
	}
	serial, err := parseBigInt(fields[4], "attribute certificate serial number")
	if err != nil {
		return nil, err
	}
	notBefore, notAfter, err := parseValidity(fields[5])
	if err != nil {
		return nil, err
	}
	attributes, err := parseAttributes(fields[6])
	if err != nil {
		return nil, err
	}

	var extensions []Extension
	if len(fields) == 8 {
		extensions, err = parseExtensions(fields[7])
		if err != nil {
			return nil, err
		}
	}

	return &AttributeCertificate{
		Version:      int(version.Int64()),
		Holder:       holder,
		Issuer:       issuer,
		SerialNumber: serial,
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		Attributes:   attributes,
		Extensions:   extensions,
		signedDER:    append([]byte(nil), n.full...),
		innerAlg:     innerAlg,
	}, nil
}

func parseHolder(n derNode) (Holder, error) {
	var holder Holder
	if err := n.requireUniversal(derTagSequence, "Holder"); err != nil {
		return holder, err
	}
	fields, err := n.children()
	if err != nil {
		return holder, err
	}
	for _, field := range fields {
		if field.class != derClassContext {
			return holder, fmt.Errorf("%w: Holder member has the wrong tag", ErrMalformed)
		}
		switch field.tag {
		case 0:
			seq, err := taggedSequence(field, "baseCertificateID")
			if err != nil {
				return holder, err
			}
			if holder.BaseCertificateID != nil {
				return holder, fmt.Errorf("%w: duplicate baseCertificateID", ErrMalformed)
			}
			is, err := parseIssuerSerial(seq)
			if err != nil {
				return holder, err
			}
			holder.BaseCertificateID = &is
		case 1:
			seq, err := taggedSequence(field, "entityName")
			if err != nil {
				return holder, err
			}
			if holder.EntityName != nil {
				return holder, fmt.Errorf("%w: duplicate entityName", ErrMalformed)
			}
			names, err := parseGeneralNames(seq)
			if err != nil {
				return holder, err
			}
			holder.EntityName = names
		case 2:
			seq, err := taggedSequence(field, "objectDigestInfo")
			if err != nil {
				return holder, err
			}
			if holder.ObjectDigestInfo != nil {
				return holder, fmt.Errorf("%w: duplicate objectDigestInfo", ErrMalformed)
			}
			info, err := parseObjectDigestInfo(seq)
			if err != nil {
				return holder, err
			}
			holder.ObjectDigestInfo = &info
		default:
			return holder, fmt.Errorf("%w: unknown Holder member [%d]", ErrMalformed, field.tag)
		}
	}
	if len(fields) == 0 {
		return holder, fmt.Errorf("%w: Holder is empty", ErrMalformed)
	}
	return holder, nil
}

func parseAttCertIssuer(n derNode) (AttCertIssuer, error) {
	var issuer AttCertIssuer
	if n.isUniversal(derTagSequence) {
		names, err := parseGeneralNames(n)
		if err != nil {
			return issuer, err
		}
		issuer.V1Form = names
		return issuer, nil
	}
	if n.class != derClassContext || n.tag != 0 {
		return issuer, fmt.Errorf("%w: invalid AttCertIssuer", ErrMalformed)
	}
	seq, err := v2FormSequence(n)
	if err != nil {
		return issuer, err
	}
	fields, err := seq.children()
	if err != nil {
		return issuer, err
	}
	v2 := &V2Form{}
	for _, field := range fields {
		if field.isUniversal(derTagSequence) {
			if v2.IssuerName != nil {
				return issuer, fmt.Errorf("%w: duplicate v2Form issuerName", ErrMalformed)
			}
			names, err := parseGeneralNames(field)
			if err != nil {
				return issuer, err
			}
			v2.IssuerName = names
			continue
		}
		if field.class != derClassContext {
			return issuer, fmt.Errorf("%w: invalid v2Form member", ErrMalformed)
		}
		switch field.tag {
		case 0:
			if v2.BaseCertificateID != nil {
				return issuer, fmt.Errorf("%w: duplicate v2Form baseCertificateID", ErrMalformed)
			}
			inner, err := taggedSequence(field, "v2Form baseCertificateID")
			if err != nil {
				return issuer, err
			}
			is, err := parseIssuerSerial(inner)
			if err != nil {
				return issuer, err
			}
			v2.BaseCertificateID = &is
		case 2:
			if v2.ObjectDigestInfo != nil {
				return issuer, fmt.Errorf("%w: duplicate v2Form objectDigestInfo", ErrMalformed)
			}
			inner, err := taggedSequence(field, "v2Form objectDigestInfo")
			if err != nil {
				return issuer, err
			}
			info, err := parseObjectDigestInfo(inner)
			if err != nil {
				return issuer, err
			}
			v2.ObjectDigestInfo = &info
		default:
			return issuer, fmt.Errorf("%w: unknown v2Form member [%d]", ErrMalformed, field.tag)
		}
	}
	if v2.IssuerName == nil && v2.BaseCertificateID == nil && v2.ObjectDigestInfo == nil {
		return issuer, fmt.Errorf("%w: empty v2Form", ErrMalformed)
	}
	issuer.V2Form = v2
	return issuer, nil
}

func v2FormSequence(n derNode) (derNode, error) {
	if !n.constructed {
		return derNode{}, fmt.Errorf("%w: v2Form is not constructed", ErrMalformed)
	}
	first, rest, err := readDER(n.content, 1)
	if err == nil && len(rest) == 0 && first.isUniversal(derTagSequence) {
		// With EXPLICIT TAGS, [0] wraps the V2Form. With IMPLICIT TAGS
		// and only an issuerName present, the content is that
		// GeneralNames sequence itself. Decide by attempting the V2Form
		// field shape; a GeneralNames sequence does not have one.
		if looksLikeV2Form(first) {
			return first, nil
		}
	}
	return derNode{
		class:       derClassUniversal,
		tag:         derTagSequence,
		constructed: true,
		content:     n.content,
		full:        marshalSequence(n.content),
	}, nil
}

func looksLikeV2Form(n derNode) bool {
	if !n.isUniversal(derTagSequence) {
		return false
	}
	fields, err := n.children()
	if err != nil || len(fields) == 0 || len(fields) > 3 {
		return false
	}
	var sawIssuerName, sawBaseCertificateID, sawObjectDigestInfo bool
	for _, field := range fields {
		if field.isUniversal(derTagSequence) {
			if sawIssuerName {
				return false
			}
			sawIssuerName = true
			continue
		}
		if field.class != derClassContext {
			return false
		}
		switch field.tag {
		case 0:
			if sawBaseCertificateID {
				return false
			}
			seq, err := taggedSequence(field, "v2Form baseCertificateID")
			if err != nil {
				return false
			}
			if _, err := parseIssuerSerial(seq); err != nil {
				return false
			}
			sawBaseCertificateID = true
		case 2:
			if sawObjectDigestInfo {
				return false
			}
			seq, err := taggedSequence(field, "v2Form objectDigestInfo")
			if err != nil {
				return false
			}
			if _, err := parseObjectDigestInfo(seq); err != nil {
				return false
			}
			sawObjectDigestInfo = true
		default:
			return false
		}
	}
	return true
}

func parseIssuerSerial(n derNode) (IssuerSerial, error) {
	var is IssuerSerial
	if err := n.requireUniversal(derTagSequence, "IssuerSerial"); err != nil {
		return is, err
	}
	fields, err := n.children()
	if err != nil {
		return is, err
	}
	if len(fields) != 2 && len(fields) != 3 {
		return is, fmt.Errorf("%w: IssuerSerial has %d fields", ErrMalformed, len(fields))
	}
	if err := fields[0].requireUniversal(derTagSequence, "IssuerSerial issuer"); err != nil {
		return is, err
	}
	names, err := parseGeneralNames(fields[0])
	if err != nil {
		return is, err
	}
	serial, err := parseBigInt(fields[1], "IssuerSerial serial")
	if err != nil {
		return is, err
	}
	is.Issuer = names
	is.Serial = serial
	if len(fields) == 3 {
		uid, err := parseBitString(fields[2], "IssuerSerial issuerUID")
		if err != nil {
			return is, err
		}
		is.IssuerUID = uid
	}
	return is, nil
}

func parseGeneralNames(n derNode) (GeneralNames, error) {
	if err := n.requireUniversal(derTagSequence, "GeneralNames"); err != nil {
		return nil, err
	}
	fields, err := n.children()
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("%w: empty GeneralNames", ErrMalformed)
	}
	names := make(GeneralNames, 0, len(fields))
	for _, field := range fields {
		name, err := parseGeneralName(field)
		if err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, nil
}

func parseGeneralName(n derNode) (GeneralName, error) {
	var name GeneralName
	if n.class != derClassContext || n.tag < GeneralNameOtherName || n.tag > GeneralNameRegisteredID {
		return name, fmt.Errorf("%w: invalid GeneralName tag", ErrMalformed)
	}
	name.Type = n.tag
	name.Raw = append([]byte(nil), n.full...)

	switch n.tag {
	case GeneralNameOtherName:
		seq, err := taggedSequence(n, "otherName")
		if err != nil {
			return name, err
		}
		name.OtherName = append([]byte(nil), seq.full...)
	case GeneralNameRFC822Name, GeneralNameDNSName, GeneralNameURI:
		name.StringValue = string(primitiveContent(n, derTagIA5String))
	case GeneralNameDirectoryName:
		seq, err := taggedSequence(n, "directoryName")
		if err != nil {
			return name, err
		}
		name.DirectoryName = append([]byte(nil), seq.full...)
	case GeneralNameIPAddress:
		content := n.content
		if child, rest, err := readDER(content, 1); err == nil &&
			len(rest) == 0 && child.isUniversal(derTagOctetString) {
			content = child.content
		}
		switch len(content) {
		case 4, 16, 8, 32:
			name.IP = append(net.IP(nil), content...)
		default:
			return name, fmt.Errorf("%w: invalid iPAddress length", ErrMalformed)
		}
	case GeneralNameRegisteredID:
		oid, err := oidFromImplicit(n)
		if err != nil {
			return name, err
		}
		name.OID = oid
	case GeneralNameX400Address, GeneralNameEDIPartyName:
		// These name forms cannot be matched to an X.509 subject without a
		// dedicated directory-specific implementation.
	}
	return name, nil
}

func parseObjectDigestInfo(n derNode) (ObjectDigestInfo, error) {
	var info ObjectDigestInfo
	if err := n.requireUniversal(derTagSequence, "ObjectDigestInfo"); err != nil {
		return info, err
	}
	fields, err := n.children()
	if err != nil {
		return info, err
	}
	if len(fields) < 3 || len(fields) > 4 {
		return info, fmt.Errorf("%w: ObjectDigestInfo has %d fields", ErrMalformed, len(fields))
	}
	if err := fields[0].requireUniversal(derTagEnumerated, "digestedObjectType"); err != nil {
		return info, err
	}
	objectValue, err := parseBigInt(derNode{
		class:   derClassUniversal,
		tag:     derTagInteger,
		content: fields[0].content,
		full:    append(derLength(derTagInteger, len(fields[0].content)), fields[0].content...),
	}, "digestedObjectType")
	if err != nil || !objectValue.IsInt64() {
		return info, fmt.Errorf("%w: invalid digestedObjectType", ErrMalformed)
	}
	objectType := int(objectValue.Int64())
	info.DigestedObjectType = objectType

	index := 1
	if objectType == DigestedObjectOther {
		if len(fields) != 4 {
			return info, fmt.Errorf("%w: otherObjectTypeID is required", ErrMalformed)
		}
		oid, err := parseOID(fields[index])
		if err != nil {
			return info, err
		}
		info.OtherObjectTypeID = oid
		index++
	} else if objectType != DigestedObjectPublicKey && objectType != DigestedObjectPublicKeyCert {
		return info, fmt.Errorf("%w: unknown digestedObjectType %d", ErrMalformed, objectType)
	} else if len(fields) != 3 {
		return info, fmt.Errorf("%w: unexpected ObjectDigestInfo member", ErrMalformed)
	}

	if index >= len(fields)-1 {
		return info, fmt.Errorf("%w: ObjectDigestInfo is missing fields", ErrMalformed)
	}
	alg, err := parseAlgorithmIdentifier(fields[index], "object digest algorithm")
	if err != nil {
		return info, err
	}
	info.DigestAlgorithm = alg
	digest, err := parseBitString(fields[index+1], "objectDigest")
	if err != nil {
		return info, err
	}
	info.ObjectDigest = digest
	return info, nil
}

func parseAlgorithmIdentifier(n derNode, what string) (pkix.AlgorithmIdentifier, error) {
	var alg pkix.AlgorithmIdentifier
	if err := n.requireUniversal(derTagSequence, what+" algorithm"); err != nil {
		return alg, err
	}
	fields, err := n.children()
	if err != nil {
		return alg, err
	}
	if len(fields) != 1 && len(fields) != 2 {
		return alg, fmt.Errorf("%w: invalid %s AlgorithmIdentifier", ErrMalformed, what)
	}
	oid, err := parseOID(fields[0])
	if err != nil {
		return alg, err
	}
	alg.Algorithm = oid
	if len(fields) == 2 {
		alg.Parameters = asn1.RawValue{
			Class:      fields[1].class,
			Tag:        fields[1].tag,
			IsCompound: fields[1].constructed,
			Bytes:      fields[1].content,
			FullBytes:  fields[1].full,
		}
	}
	return alg, nil
}

func parseValidity(n derNode) (time.Time, time.Time, error) {
	if err := n.requireUniversal(derTagSequence, "AttCertValidityPeriod"); err != nil {
		return time.Time{}, time.Time{}, err
	}
	fields, err := n.children()
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if len(fields) != 2 {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: AttCertValidityPeriod has %d fields", ErrMalformed, len(fields))
	}
	notBefore, err := parseGeneralizedTime(fields[0], "notBeforeTime")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	notAfter, err := parseGeneralizedTime(fields[1], "notAfterTime")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return notBefore, notAfter, nil
}

func parseAttributes(n derNode) ([]Attribute, error) {
	if err := n.requireUniversal(derTagSequence, "attributes"); err != nil {
		return nil, err
	}
	fields, err := n.children()
	if err != nil {
		return nil, err
	}
	attrs := make([]Attribute, 0, len(fields))
	for _, field := range fields {
		if err := field.requireUniversal(derTagSequence, "Attribute"); err != nil {
			return nil, err
		}
		parts, err := field.children()
		if err != nil {
			return nil, err
		}
		if len(parts) != 2 {
			return nil, fmt.Errorf("%w: Attribute has %d fields", ErrMalformed, len(parts))
		}
		oid, err := parseOID(parts[0])
		if err != nil {
			return nil, err
		}
		if err := parts[1].requireUniversal(derTagSet, "Attribute values"); err != nil {
			return nil, err
		}
		values, err := parts[1].children()
		if err != nil {
			return nil, err
		}
		attr := Attribute{Type: oid, Values: make([]asn1.RawValue, 0, len(values))}
		for _, value := range values {
			attr.Values = append(attr.Values, asn1.RawValue{
				Class:      value.class,
				Tag:        value.tag,
				IsCompound: value.constructed,
				Bytes:      value.content,
				FullBytes:  value.full,
			})
		}
		attrs = append(attrs, attr)
	}
	return attrs, nil
}

func parseExtensions(n derNode) ([]Extension, error) {
	if err := n.requireUniversal(derTagSequence, "extensions"); err != nil {
		return nil, err
	}
	fields, err := n.children()
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		// RFC 5755 requires SIZE (1..MAX), but accepting an empty
		// SEQUENCE is harmless and accommodates common DER writers.
		return nil, nil
	}
	exts := make([]Extension, 0, len(fields))
	for _, field := range fields {
		if err := field.requireUniversal(derTagSequence, "Extension"); err != nil {
			return nil, err
		}
		parts, err := field.children()
		if err != nil {
			return nil, err
		}
		if len(parts) != 2 && len(parts) != 3 {
			return nil, fmt.Errorf("%w: Extension has %d fields", ErrMalformed, len(parts))
		}
		oid, err := parseOID(parts[0])
		if err != nil {
			return nil, err
		}
		ext := Extension{ID: oid}
		valueIndex := 1
		if len(parts) == 3 {
			critical, err := parseBool(parts[1], "extension critical")
			if err != nil {
				return nil, err
			}
			ext.Critical = critical
			valueIndex = 2
		} else if parts[1].isUniversal(derTagBoolean) {
			return nil, fmt.Errorf("%w: Extension is missing extnValue", ErrMalformed)
		}
		value, err := parseOctetString(parts[valueIndex], "extension extnValue")
		if err != nil {
			return nil, err
		}
		ext.Value = value
		exts = append(exts, ext)
	}
	return exts, nil
}

// ParseAttributeCertificate parses one DER-encoded RFC 5755
// AttributeCertificate. It performs structural validation only; use Verify or
// AttributeCertificate.Verify before trusting it.
func ParseAttributeCertificate(der []byte) (*AttributeCertificate, error) {
	root, err := parseDER(der)
	if err != nil {
		return nil, err
	}
	if err := root.requireUniversal(derTagSequence, "AttributeCertificate"); err != nil {
		return nil, err
	}
	fields, err := root.children()
	if err != nil {
		return nil, err
	}
	if len(fields) != 3 {
		return nil, fmt.Errorf("%w: AttributeCertificate has %d fields", ErrMalformed, len(fields))
	}

	ac, err := parseAttributeCertificateInfo(fields[0])
	if err != nil {
		return nil, err
	}
	outerAlg, err := parseAlgorithmIdentifier(fields[1], "signature")
	if err != nil {
		return nil, err
	}
	signature, err := parseBitString(fields[2], "signatureValue")
	if err != nil {
		return nil, err
	}

	ac.Raw = append([]byte(nil), der...)
	ac.SignatureAlgorithm = outerAlg
	ac.signature = append([]byte(nil), signature...)
	return ac, nil
}

// ParseAC is an alias for ParseAttributeCertificate.
func ParseAC(der []byte) (*AttributeCertificate, error) {
	return ParseAttributeCertificate(der)
}

// ParseAttributeCert is an alias for ParseAttributeCertificate.
func ParseAttributeCert(der []byte) (*AttributeCertificate, error) {
	return ParseAttributeCertificate(der)
}

func stringEqual(a, b string, fold bool) bool {
	if fold {
		return strings.EqualFold(a, b)
	}
	return a == b
}
