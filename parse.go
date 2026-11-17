package netsec

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

var (
	oidSignedData    = OIDSignedData
	oidData          = OIDData
	oidContentType   = OIDContentType
	oidMessageDigest = OIDMessageDigest
)

// ParseSignedData parses a DER or PEM encoded CMS ContentInfo whose content
// type is id-signedData. It also accepts a bare SignedData SEQUENCE for
// interoperability with callers that have already unwrapped ContentInfo.
func ParseSignedData(data []byte) (*SignedData, error) {
	der, err := decodePEM(data)
	if err != nil {
		return nil, err
	}

	top, rest, err := readDER(der)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return nil, errors.New("netsec: trailing data after SignedData")
	}
	if top.Class != derClassUniversal || top.Tag != derTagSequence || !top.Constructed {
		return nil, errors.New("netsec: CMS input is not a SEQUENCE")
	}

	fields, err := splitDER(top.Content)
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, errors.New("netsec: empty CMS structure")
	}

	switch {
	case fields[0].Class == derClassUniversal && fields[0].Tag == derTagObjectIdentifier:
		oid, err := parseDEROID(fields[0])
		if err != nil {
			return nil, err
		}
		if !oid.Equal(oidSignedData) {
			return nil, fmt.Errorf("netsec: CMS content type %s is not SignedData", oid)
		}
		if len(fields) != 2 {
			return nil, errors.New("netsec: malformed ContentInfo")
		}
		wrapper := fields[1]
		if wrapper.Class != derClassContextSpecific || wrapper.Tag != 0 || !wrapper.Constructed {
			return nil, errors.New("netsec: SignedData content is not [0] EXPLICIT")
		}
		inner, err := splitDER(wrapper.Content)
		if err != nil {
			return nil, err
		}
		if len(inner) != 1 {
			return nil, errors.New("netsec: malformed SignedData wrapper")
		}
		return parseSignedDataSequence(inner[0])

	case fields[0].Class == derClassUniversal && fields[0].Tag == derTagInteger:
		return parseSignedDataSequence(top)

	default:
		return nil, errors.New("netsec: input is not a CMS ContentInfo or SignedData")
	}
}

// Parse is an alias for ParseSignedData.
func Parse(data []byte) (*SignedData, error) {
	return ParseSignedData(data)
}

// ParseCMS is an alias for ParseSignedData.
func ParseCMS(data []byte) (*SignedData, error) {
	return ParseSignedData(data)
}

func decodePEM(data []byte) ([]byte, error) {
	trimmed := strings.TrimSpace(string(data))
	if !strings.HasPrefix(trimmed, "-----BEGIN ") {
		return data, nil
	}

	var out []byte
	rest := []byte(trimmed)
	for {
		block, next := pem.Decode(rest)
		if block == nil {
			if len(strings.TrimSpace(string(rest))) == 0 {
				break
			}
			return nil, errors.New("netsec: malformed PEM input")
		}
		if block.Type == "PKCS7" || block.Type == "CMS" || block.Type == "SIGNED DATA" {
			if len(out) != 0 {
				return nil, errors.New("netsec: multiple signed data blocks")
			}
			out = block.Bytes
		}
		rest = next
	}
	if len(out) == 0 {
		return nil, errors.New("netsec: PEM does not contain PKCS7/CMS signed data")
	}
	return out, nil
}

func parseSignedDataSequence(v derValue) (*SignedData, error) {
	if v.Class != derClassUniversal || v.Tag != derTagSequence || !v.Constructed {
		return nil, errors.New("netsec: SignedData is not a SEQUENCE")
	}

	fields, err := splitDER(v.Content)
	if err != nil {
		return nil, err
	}
	if len(fields) < 4 {
		return nil, errors.New("netsec: truncated SignedData")
	}

	version, err := parseDERInteger(fields[0])
	if err != nil {
		return nil, fmt.Errorf("netsec: SignedData version: %w", err)
	}

	if fields[1].Class != derClassUniversal || fields[1].Tag != derTagSet {
		return nil, errors.New("netsec: digestAlgorithms is not a SET")
	}
	digestFields, err := splitDER(fields[1].Content)
	if err != nil {
		return nil, err
	}
	digestAlgorithms := make([]pkix.AlgorithmIdentifier, 0, len(digestFields))
	for _, field := range digestFields {
		algorithm, err := parseAlgorithmIdentifier(field)
		if err != nil {
			return nil, fmt.Errorf("netsec: digest algorithm: %w", err)
		}
		digestAlgorithms = append(digestAlgorithms, algorithm)
	}

	contentType, content, hasContent, err := parseEncapsulatedContentInfo(fields[2])
	if err != nil {
		return nil, err
	}

	sd := &SignedData{
		Version:          version,
		DigestAlgorithms: digestAlgorithms,
		ContentType:      contentType,
		Content:          content,
		HasContent:       hasContent,
	}

	var (
		signerSet       *derValue
		sawCertificates bool
	)
	for i := 3; i < len(fields); i++ {
		field := fields[i]
		switch {
		case field.Class == derClassContextSpecific && field.Tag == 0:
			if sawCertificates {
				return nil, errors.New("netsec: duplicate certificates field")
			}
			sawCertificates = true
			certs, err := parseCertificates(field)
			if err != nil {
				return nil, err
			}
			sd.Certificates = certs

		case field.Class == derClassContextSpecific && field.Tag == 1:
			// CRLs are not needed for archive verification.

		case field.Class == derClassUniversal && field.Tag == derTagSet:
			if signerSet != nil {
				return nil, errors.New("netsec: duplicate signerInfos field")
			}
			if i != len(fields)-1 {
				return nil, errors.New("netsec: signerInfos must be the last SignedData field")
			}
			signerSet = &field

		default:
			return nil, fmt.Errorf("netsec: unexpected SignedData field class %d tag %d", field.Class, field.Tag)
		}
	}

	if signerSet == nil {
		return nil, errors.New("netsec: SignedData has no signerInfos")
	}
	signerFields, err := splitDER(signerSet.Content)
	if err != nil {
		return nil, err
	}
	if len(signerFields) == 0 {
		return nil, errors.New("netsec: SignedData has an empty signerInfos set")
	}
	for _, field := range signerFields {
		signer, err := parseSignerInfo(field)
		if err != nil {
			return nil, err
		}
		sd.SignerInfos = append(sd.SignerInfos, signer)
	}

	return sd, nil
}

func parseEncapsulatedContentInfo(v derValue) (asn1.ObjectIdentifier, []byte, bool, error) {
	if v.Class != derClassUniversal || v.Tag != derTagSequence || !v.Constructed {
		return nil, nil, false, errors.New("netsec: encapContentInfo is not a SEQUENCE")
	}

	fields, err := splitDER(v.Content)
	if err != nil {
		return nil, nil, false, err
	}
	if len(fields) < 1 || len(fields) > 2 {
		return nil, nil, false, errors.New("netsec: malformed encapContentInfo")
	}

	contentType, err := parseDEROID(fields[0])
	if err != nil {
		return nil, nil, false, fmt.Errorf("netsec: eContentType: %w", err)
	}
	if len(fields) == 1 {
		return contentType, nil, false, nil
	}

	wrapper := fields[1]
	if wrapper.Class != derClassContextSpecific || wrapper.Tag != 0 || !wrapper.Constructed {
		return nil, nil, false, errors.New("netsec: eContent is not [0] EXPLICIT")
	}
	inner, err := splitDER(wrapper.Content)
	if err != nil {
		return nil, nil, false, err
	}
	if len(inner) != 1 {
		return nil, nil, false, errors.New("netsec: malformed eContent wrapper")
	}
	content, err := parseDEROctetString(inner[0])
	if err != nil {
		return nil, nil, false, fmt.Errorf("netsec: eContent: %w", err)
	}
	return contentType, content, true, nil
}

func parseCertificates(v derValue) ([]*x509.Certificate, error) {
	if v.Class != derClassContextSpecific || v.Tag != 0 || !v.Constructed {
		return nil, errors.New("netsec: certificates is not [0] IMPLICIT")
	}

	choices, err := splitDER(v.Content)
	if err != nil {
		return nil, err
	}
	var certs []*x509.Certificate
	for _, choice := range choices {
		if choice.Class != derClassUniversal || choice.Tag != derTagSequence || !choice.Constructed {
			// Attribute certificates and other CertificateChoices are not
			// needed to identify a SignerInfo.
			continue
		}
		cert, err := x509.ParseCertificate(choice.Full)
		if err != nil {
			return nil, fmt.Errorf("netsec: invalid certificate set entry: %w", err)
		}
		certs = append(certs, cert)
	}
	return certs, nil
}

func parseSignerInfo(v derValue) (*SignerInfo, error) {
	if v.Class != derClassUniversal || v.Tag != derTagSequence || !v.Constructed {
		return nil, errors.New("netsec: SignerInfo is not a SEQUENCE")
	}

	fields, err := splitDER(v.Content)
	if err != nil {
		return nil, err
	}
	if len(fields) < 5 {
		return nil, errors.New("netsec: truncated SignerInfo")
	}

	version, err := parseDERInteger(fields[0])
	if err != nil {
		return nil, fmt.Errorf("netsec: SignerInfo version: %w", err)
	}
	signer := &SignerInfo{Version: version}

	switch {
	case fields[1].Class == derClassUniversal && fields[1].Tag == derTagSequence:
		identifier, err := splitDER(fields[1].Content)
		if err != nil {
			return nil, err
		}
		if len(identifier) != 2 {
			return nil, errors.New("netsec: malformed issuerAndSerialNumber")
		}
		if identifier[0].Class != derClassUniversal || identifier[0].Tag != derTagSequence {
			return nil, errors.New("netsec: signer issuer is not a Name")
		}
		var serial *big.Int
		rest, err := asn1.Unmarshal(identifier[1].Full, &serial)
		if err != nil {
			return nil, fmt.Errorf("netsec: signer serial number: %w", err)
		}
		if len(rest) != 0 || serial == nil {
			return nil, errors.New("netsec: malformed signer serial number")
		}
		signer.Issuer = identifier[0].Full
		signer.SerialNumber = serial

	case fields[1].Class == derClassContextSpecific && fields[1].Tag == 0:
		if fields[1].Constructed {
			return nil, errors.New("netsec: constructed subjectKeyIdentifier")
		}
		signer.SubjectKeyIdentifier = append([]byte(nil), fields[1].Content...)

	default:
		return nil, errors.New("netsec: unsupported SignerIdentifier")
	}

	digestAlgorithm, err := parseAlgorithmIdentifier(fields[2])
	if err != nil {
		return nil, fmt.Errorf("netsec: SignerInfo digestAlgorithm: %w", err)
	}
	signer.DigestAlgorithm = digestAlgorithm

	index := 3
	if fields[index].Class == derClassContextSpecific && fields[index].Tag == 0 {
		if !fields[index].Constructed {
			return nil, errors.New("netsec: signedAttrs is not constructed")
		}
		attrs, err := parseAttributes(fields[index])
		if err != nil {
			return nil, fmt.Errorf("netsec: signedAttrs: %w", err)
		}
		signer.HasSignedAttrs = true
		signer.SignedAttrs = attrs
		signer.signedAttrsContent = fields[index].Content
		index++
	}

	if index >= len(fields) {
		return nil, errors.New("netsec: SignerInfo has no signatureAlgorithm")
	}
	signatureAlgorithm, err := parseAlgorithmIdentifier(fields[index])
	if err != nil {
		return nil, fmt.Errorf("netsec: SignerInfo signatureAlgorithm: %w", err)
	}
	signer.SignatureAlgorithm = signatureAlgorithm
	index++

	if index >= len(fields) {
		return nil, errors.New("netsec: SignerInfo has no signature")
	}
	signature, err := parseDEROctetString(fields[index])
	if err != nil {
		return nil, fmt.Errorf("netsec: SignerInfo signature: %w", err)
	}
	signer.Signature = append([]byte(nil), signature...)
	index++

	if index < len(fields) && fields[index].Class == derClassContextSpecific && fields[index].Tag == 1 {
		unsigned, err := parseAttributes(fields[index])
		if err != nil {
			return nil, fmt.Errorf("netsec: unsignedAttrs: %w", err)
		}
		signer.UnsignedAttrs = unsigned
		index++
	}
	if index != len(fields) {
		return nil, errors.New("netsec: unexpected trailing SignerInfo field")
	}

	return signer, nil
}

func parseAlgorithmIdentifier(v derValue) (pkix.AlgorithmIdentifier, error) {
	if v.Class != derClassUniversal || v.Tag != derTagSequence || !v.Constructed {
		return pkix.AlgorithmIdentifier{}, errors.New("AlgorithmIdentifier is not a SEQUENCE")
	}
	fields, err := splitDER(v.Content)
	if err != nil {
		return pkix.AlgorithmIdentifier{}, err
	}
	if len(fields) < 1 || len(fields) > 2 {
		return pkix.AlgorithmIdentifier{}, errors.New("malformed AlgorithmIdentifier")
	}
	oid, err := parseDEROID(fields[0])
	if err != nil {
		return pkix.AlgorithmIdentifier{}, err
	}
	algorithm := pkix.AlgorithmIdentifier{Algorithm: oid}
	if len(fields) == 2 {
		algorithm.Parameters = rawValue(fields[1])
	}
	return algorithm, nil
}

func parseAttributes(v derValue) ([]Attribute, error) {
	if v.Class != derClassContextSpecific || !v.Constructed {
		return nil, errors.New("attribute set has the wrong tag")
	}

	encoded, err := splitDER(v.Content)
	if err != nil {
		return nil, err
	}
	attributes := make([]Attribute, 0, len(encoded))
	for _, item := range encoded {
		if item.Class != derClassUniversal || item.Tag != derTagSequence || !item.Constructed {
			return nil, errors.New("attribute is not a SEQUENCE")
		}
		fields, err := splitDER(item.Content)
		if err != nil {
			return nil, err
		}
		if len(fields) != 2 {
			return nil, errors.New("malformed attribute")
		}
		oid, err := parseDEROID(fields[0])
		if err != nil {
			return nil, err
		}
		if fields[1].Class != derClassUniversal || fields[1].Tag != derTagSet || !fields[1].Constructed {
			return nil, errors.New("attribute values are not a SET")
		}
		values, err := splitDER(fields[1].Content)
		if err != nil {
			return nil, err
		}
		attribute := Attribute{Type: oid, Values: make([]asn1.RawValue, 0, len(values))}
		for _, value := range values {
			attribute.Values = append(attribute.Values, rawValue(value))
		}
		attributes = append(attributes, attribute)
	}
	return attributes, nil
}
