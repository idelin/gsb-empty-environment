package netsec

import (
	"bytes"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
	"time"
)

type cmsContentInfo struct {
	ContentType asn1.ObjectIdentifier
	Content     asn1.RawValue
}

type cmsSignedData struct {
	Version          int
	DigestAlgorithms []AlgorithmIdentifier
	EncapContent     cmsEncapContent
	Certificates     []*x509.Certificate
	SignerInfos      []cmsSignerInfo
}

type cmsEncapContent struct {
	ContentType asn1.ObjectIdentifier
	Content     []byte
}

type cmsSignerInfo struct {
	Version              int
	Issuer               asn1.RawValue
	SerialNumber         *big.Int
	SubjectKeyIdentifier []byte
	DigestAlgorithm      AlgorithmIdentifier
	SignedAttributes     []Attribute
	RawSignedAttributes  asn1.RawValue
	SignatureAlgorithm   AlgorithmIdentifier
	Signature            []byte
	UnauthenticatedAttrs []Attribute
	Certificate          *x509.Certificate
}

// ParseTimestampToken parses an RFC 3161 TimeStampToken and verifies the CMS
// signature using certificates embedded in the token. Certificate chain trust
// is not evaluated here; use Verifier.Verify for that.
func ParseTimestampToken(raw []byte) (*TimestampToken, error) {
	return parseTimestampToken(raw, true)
}

// ParseToken is an alias for ParseTimestampToken.
func ParseToken(raw []byte) (*TimestampToken, error) {
	return ParseTimestampToken(raw)
}

// InspectTimestampToken parses the token fields without verifying CMS
// signatures. It is intended for diagnostics and must not be used before a
// token has been accepted by Verifier.Verify.
func InspectTimestampToken(raw []byte) (*TimestampToken, error) {
	return parseTimestampToken(raw, false)
}

func parseTimestampToken(raw []byte, verifySignatures bool) (*TimestampToken, error) {
	if len(raw) == 0 {
		return nil, ErrNoTimestampToken
	}
	contentInfo, err := parseContentInfo(raw)
	if err != nil {
		return nil, wrapError(ErrMalformedToken, err)
	}
	if !equalOID(contentInfo.ContentType, OIDContentTypeSignedData) {
		return nil, errorf(ErrUnsupportedContentType, "outer content type is %s", contentInfo.ContentType.String())
	}
	signedData, err := parseSignedData(contentInfo.Content)
	if err != nil {
		return nil, wrapError(ErrMalformedToken, err)
	}
	if !equalOID(signedData.EncapContent.ContentType, OIDContentTypeTSTInfo) {
		return nil, errorf(ErrUnsupportedContentType, "encapsulated content type is %s", signedData.EncapContent.ContentType.String())
	}
	tstInfo, err := parseTSTInfo(signedData.EncapContent.Content)
	if err != nil {
		return nil, err
	}

	token := &TimestampToken{
		Raw:                bytes.Clone(raw),
		ContentType:        contentInfo.ContentType,
		EncapContentType:   signedData.EncapContent.ContentType,
		Version:            tstInfo.Version,
		Policy:             tstInfo.Policy,
		MessageImprint:     tstInfo.MessageImprint,
		Imprint:            tstInfo.MessageImprint,
		SerialNumber:       tstInfo.SerialNumber,
		GenTime:            tstInfo.GenTime,
		Accuracy:           tstInfo.Accuracy,
		Ordering:           tstInfo.Ordering,
		Nonce:              tstInfo.Nonce,
		TSAName:            tstInfo.TSAName,
		SignerCertificates: signedData.Certificates,
		Certificates:       signedData.Certificates,
		HashAlgorithm:      tstInfo.MessageImprint.HashAlgorithm,
		HashedMessage:      bytes.Clone(tstInfo.MessageImprint.HashedMessage),
	}

	for _, info := range signedData.SignerInfos {
		identity := certIdentity(info.Certificate)
		issuerName := ""
		if info.Certificate != nil {
			issuerName = identity.description()
		}
		signer := TokenSigner{
			Certificate:          info.Certificate,
			IssuerIdentity:       identity,
			IssuerName:           issuerName,
			SerialNumber:         cloneBigInt(info.SerialNumber),
			DigestAlgorithm:      info.DigestAlgorithm,
			SignatureAlgorithm:   info.SignatureAlgorithm,
			SignedAttributes:     info.SignedAttributes,
			Signature:            bytes.Clone(info.Signature),
			SubjectKeyIdentifier: bytes.Clone(info.SubjectKeyIdentifier),
		}
		token.Signers = append(token.Signers, signer)
	}

	if verifySignatures {
		valid := 0
		var firstFailure error
		for i := range token.Signers {
			if token.Signers[i].Certificate == nil {
				continue
			}
			if err := verifyCMSSigner(signedData, &signedData.SignerInfos[i], &token.Signers[i]); err != nil {
				if firstFailure == nil {
					firstFailure = err
				}
				continue
			}
			token.Signers[i].SignatureVerified = true
			valid++
		}
		if valid == 0 {
			if firstFailure != nil &&
				(errors.Is(firstFailure, ErrUnsupportedContentType) || errors.Is(firstFailure, ErrUnsupportedDigest)) {
				return nil, firstFailure
			}
			if firstFailure != nil {
				return nil, &verifyError{kind: ErrTokenSignature, err: firstFailure}
			}
			return nil, errorf(ErrTokenSignature, "no valid CMS signer found")
		}
	}

	if primary := token.primarySigner(); primary != nil && primary.Certificate != nil {
		token.IssuerIdentity = primary.IssuerIdentity
		token.IssuerName = primary.IssuerName
		token.Issuer = primary.IssuerName
		token.TimestampSigner = primary.Certificate
		token.SigningTime = primary.SigningTime
	}
	token.SignatureTimestamp = token.SigningTime
	if token.SignatureTimestamp.IsZero() {
		token.SignatureTimestamp = token.GenTime
	}
	return token, nil
}

func parseContentInfo(raw []byte) (cmsContentInfo, error) {
	value, err := parseOneDER(raw)
	if err != nil {
		return cmsContentInfo{}, err
	}
	children, err := parseSequence(value)
	if err != nil {
		return cmsContentInfo{}, err
	}
	if len(children) < 1 || len(children) > 2 {
		return cmsContentInfo{}, errors.New("invalid ContentInfo")
	}
	contentType, err := parseOID(children[0])
	if err != nil {
		return cmsContentInfo{}, err
	}
	result := cmsContentInfo{ContentType: contentType}
	if len(children) == 2 {
		if children[1].Class != asn1.ClassContextSpecific || children[1].Tag != 0 || !children[1].IsCompound {
			return cmsContentInfo{}, errors.New("ContentInfo content is not [0] EXPLICIT")
		}
		inner, err := parseOneDER(children[1].Bytes)
		if err != nil {
			return cmsContentInfo{}, err
		}
		result.Content = inner
	}
	return result, nil
}

func parseSignedData(value asn1.RawValue) (cmsSignedData, error) {
	children, err := parseSequence(value)
	if err != nil {
		return cmsSignedData{}, err
	}
	if len(children) < 4 {
		return cmsSignedData{}, errors.New("SignedData is too short")
	}
	version, err := parseInteger(children[0])
	if err != nil || !version.IsInt64() {
		return cmsSignedData{}, errors.New("invalid SignedData version")
	}
	result := cmsSignedData{Version: int(version.Int64())}
	digestSets, err := parseSet(children[1])
	if err != nil {
		return cmsSignedData{}, err
	}
	for _, digestValue := range digestSets {
		algorithm, err := parseAlgorithmIdentifier(digestValue)
		if err != nil {
			return cmsSignedData{}, err
		}
		algorithm.Name = digestAlgorithmName(algorithm.OID)
		result.DigestAlgorithms = append(result.DigestAlgorithms, algorithm)
	}
	result.EncapContent, err = parseEncapContent(children[2])
	if err != nil {
		return cmsSignedData{}, err
	}

	var signerSet *asn1.RawValue
	for index := 3; index < len(children); index++ {
		child := children[index]
		switch {
		case child.Class == asn1.ClassContextSpecific && child.Tag == 0:
			if result.Certificates != nil {
				return cmsSignedData{}, errors.New("duplicate certificate set")
			}
			result.Certificates, err = parseCertificateSet(child)
			if err != nil {
				return cmsSignedData{}, err
			}
		case child.Class == asn1.ClassContextSpecific && child.Tag == 1:
			// CRLs are not needed for timestamp binding. Their presence is
			// accepted, but the configured revocation checker is authoritative.
		case child.Class == asn1.ClassUniversal && child.Tag == asn1.TagSet:
			if signerSet != nil {
				return cmsSignedData{}, errors.New("duplicate SignerInfos set")
			}
			copied := child
			signerSet = &copied
		default:
			return cmsSignedData{}, fmt.Errorf("unexpected SignedData field class=%d tag=%d", child.Class, child.Tag)
		}
	}
	if signerSet == nil {
		return cmsSignedData{}, errors.New("SignedData has no SignerInfos")
	}
	signerValues, err := parseSet(*signerSet)
	if err != nil {
		return cmsSignedData{}, err
	}
	if len(signerValues) == 0 {
		return cmsSignedData{}, errors.New("SignedData has an empty SignerInfos set")
	}
	for _, signerValue := range signerValues {
		info, err := parseSignerInfo(signerValue)
		if err != nil {
			return cmsSignedData{}, err
		}
		info.Certificate = findSignerCertificate(info, result.Certificates)
		result.SignerInfos = append(result.SignerInfos, info)
	}
	return result, nil
}

func parseEncapContent(value asn1.RawValue) (cmsEncapContent, error) {
	children, err := parseSequence(value)
	if err != nil {
		return cmsEncapContent{}, err
	}
	if len(children) < 1 || len(children) > 2 {
		return cmsEncapContent{}, errors.New("invalid EncapsulatedContentInfo")
	}
	contentType, err := parseOID(children[0])
	if err != nil {
		return cmsEncapContent{}, err
	}
	result := cmsEncapContent{ContentType: contentType}
	if len(children) == 2 {
		if children[1].Class != asn1.ClassContextSpecific || children[1].Tag != 0 {
			return cmsEncapContent{}, errors.New("encapsulated content is not [0]")
		}
		inner, err := parseOneDER(children[1].Bytes)
		if err != nil {
			return cmsEncapContent{}, err
		}
		if inner.Class == asn1.ClassUniversal && inner.Tag == asn1.TagOctetString {
			result.Content, err = parseOctetString(inner)
			if err != nil {
				return cmsEncapContent{}, err
			}
		} else {
			result.Content = bytes.Clone(children[1].Bytes)
		}
	}
	if len(result.Content) == 0 {
		return cmsEncapContent{}, errors.New("timestamp token has no TSTInfo content")
	}
	return result, nil
}

func parseCertificateSet(value asn1.RawValue) ([]*x509.Certificate, error) {
	if value.Class != asn1.ClassContextSpecific || !value.IsCompound {
		return nil, errors.New("invalid certificate set")
	}
	values, err := splitDER(value.Bytes)
	if err != nil {
		return nil, err
	}
	var certificates []*x509.Certificate
	for _, candidate := range values {
		if candidate.Class != asn1.ClassUniversal || candidate.Tag != asn1.TagSequence {
			continue
		}
		certificate, err := x509.ParseCertificate(candidate.FullBytes)
		if err != nil {
			continue
		}
		certificates = append(certificates, certificate)
	}
	if len(certificates) == 0 {
		return nil, errors.New("timestamp token contains no X.509 certificates")
	}
	return certificates, nil
}

func parseSignerInfo(value asn1.RawValue) (cmsSignerInfo, error) {
	children, err := parseSequence(value)
	if err != nil {
		return cmsSignerInfo{}, err
	}
	if len(children) < 5 {
		return cmsSignerInfo{}, errors.New("SignerInfo is too short")
	}
	var result cmsSignerInfo
	version, err := parseInteger(children[0])
	if err != nil || !version.IsInt64() {
		return cmsSignerInfo{}, errors.New("invalid SignerInfo version")
	}
	result.Version = int(version.Int64())

	switch {
	case children[1].Class == asn1.ClassUniversal && children[1].Tag == asn1.TagSequence:
		issuerAndSerial, err := parseSequence(children[1])
		if err != nil || len(issuerAndSerial) != 2 {
			return cmsSignerInfo{}, errors.New("invalid IssuerAndSerialNumber")
		}
		result.Issuer = issuerAndSerial[0]
		result.SerialNumber, err = parseInteger(issuerAndSerial[1])
		if err != nil {
			return cmsSignerInfo{}, err
		}
	case children[1].Class == asn1.ClassContextSpecific && children[1].Tag == 0:
		result.SubjectKeyIdentifier, err = parseImplicitOctets(children[1])
		if err != nil {
			return cmsSignerInfo{}, err
		}
	default:
		return cmsSignerInfo{}, errors.New("SignerInfo has no signer identifier")
	}

	result.DigestAlgorithm, err = parseAlgorithmIdentifier(children[2])
	if err != nil {
		return cmsSignerInfo{}, err
	}
	result.DigestAlgorithm.Name = digestAlgorithmName(result.DigestAlgorithm.OID)

	index := 3
	if children[index].Class == asn1.ClassContextSpecific && children[index].Tag == 0 {
		result.RawSignedAttributes = children[index]
		result.SignedAttributes, err = parseAttributes(children[index])
		if err != nil {
			return cmsSignerInfo{}, err
		}
		index++
	}
	if index+1 >= len(children) {
		return cmsSignerInfo{}, errors.New("SignerInfo is missing signature fields")
	}
	result.SignatureAlgorithm, err = parseAlgorithmIdentifier(children[index])
	if err != nil {
		return cmsSignerInfo{}, err
	}
	result.SignatureAlgorithm.Name = signatureAlgorithmName(result.SignatureAlgorithm.OID)
	index++
	result.Signature, err = parseOctetString(children[index])
	if err != nil {
		return cmsSignerInfo{}, err
	}
	if len(result.Signature) == 0 {
		return cmsSignerInfo{}, errors.New("SignerInfo has an empty signature")
	}
	index++
	if index < len(children) {
		if children[index].Class != asn1.ClassContextSpecific || children[index].Tag != 1 {
			return cmsSignerInfo{}, errors.New("unexpected SignerInfo field")
		}
		result.UnauthenticatedAttrs, err = parseAttributes(children[index])
		if err != nil {
			return cmsSignerInfo{}, err
		}
		index++
	}
	if index != len(children) {
		return cmsSignerInfo{}, errors.New("trailing SignerInfo fields")
	}
	return result, nil
}

func parseAttributes(value asn1.RawValue) ([]Attribute, error) {
	if value.Class != asn1.ClassContextSpecific || !value.IsCompound {
		return nil, errors.New("invalid CMS attributes")
	}
	values, err := splitDER(value.Bytes)
	if err != nil {
		return nil, err
	}
	attributes := make([]Attribute, 0, len(values))
	for _, attributeValue := range values {
		children, err := parseSequence(attributeValue)
		if err != nil || len(children) != 2 {
			return nil, errors.New("invalid CMS attribute")
		}
		oid, err := parseOID(children[0])
		if err != nil {
			return nil, err
		}
		attributeValues, err := parseSet(children[1])
		if err != nil {
			return nil, err
		}
		attributes = append(attributes, Attribute{Type: oid, Values: attributeValues})
	}
	return attributes, nil
}

func findSignerCertificate(info cmsSignerInfo, certificates []*x509.Certificate) *x509.Certificate {
	for _, certificate := range certificates {
		if len(info.SubjectKeyIdentifier) > 0 {
			if equalBytes(certificate.SubjectKeyId, info.SubjectKeyIdentifier) {
				return certificate
			}
			continue
		}
		if info.SerialNumber != nil &&
			certificate.SerialNumber != nil &&
			certificate.SerialNumber.Cmp(info.SerialNumber) == 0 &&
			equalBytes(certificate.RawIssuer, info.Issuer.FullBytes) {
			return certificate
		}
	}
	return nil
}

type parsedTSTInfo struct {
	Version        int
	Policy         asn1.ObjectIdentifier
	MessageImprint MessageImprint
	SerialNumber   *big.Int
	GenTime        time.Time
	Accuracy       *Accuracy
	Ordering       bool
	Nonce          *big.Int
	TSAName        *asn1.RawValue
}

func parseTSTInfo(raw []byte) (parsedTSTInfo, error) {
	value, err := parseOneDER(raw)
	if err != nil {
		return parsedTSTInfo{}, wrapError(ErrMalformedToken, err)
	}
	children, err := parseSequence(value)
	if err != nil {
		return parsedTSTInfo{}, wrapError(ErrMalformedToken, err)
	}
	if len(children) < 5 {
		return parsedTSTInfo{}, errorf(ErrMalformedToken, "TSTInfo is too short")
	}
	version, err := parseInteger(children[0])
	if err != nil || !version.IsInt64() || version.Int64() != 1 {
		return parsedTSTInfo{}, errorf(ErrMalformedToken, "unsupported TSTInfo version")
	}
	policy, err := parseOID(children[1])
	if err != nil {
		return parsedTSTInfo{}, wrapError(ErrMalformedToken, err)
	}
	imprint, err := parseMessageImprint(children[2])
	if err != nil {
		return parsedTSTInfo{}, err
	}
	serial, err := parseInteger(children[3])
	if err != nil {
		return parsedTSTInfo{}, errorf(ErrMalformedToken, "invalid timestamp serial number")
	}
	genTime, err := parseGeneralizedTime(children[4])
	if err != nil {
		return parsedTSTInfo{}, wrapError(ErrMalformedToken, err)
	}
	result := parsedTSTInfo{
		Version:        1,
		Policy:         policy,
		MessageImprint: imprint,
		SerialNumber:   serial,
		GenTime:        genTime,
	}
	for index := 5; index < len(children); index++ {
		child := children[index]
		switch {
		case child.Class == asn1.ClassUniversal && child.Tag == asn1.TagSequence:
			result.Accuracy, err = parseAccuracy(child)
			if err != nil {
				return parsedTSTInfo{}, wrapError(ErrMalformedToken, err)
			}
		case child.Class == asn1.ClassUniversal && child.Tag == asn1.TagBoolean:
			result.Ordering, err = parseBoolean(child)
			if err != nil {
				return parsedTSTInfo{}, wrapError(ErrMalformedToken, err)
			}
		case child.Class == asn1.ClassUniversal && child.Tag == asn1.TagInteger:
			result.Nonce, err = parseInteger(child)
			if err != nil {
				return parsedTSTInfo{}, wrapError(ErrMalformedToken, err)
			}
		case child.Class == asn1.ClassContextSpecific && child.Tag == 0:
			copied := child
			result.TSAName = &copied
		case child.Class == asn1.ClassContextSpecific && child.Tag == 1:
			// Extensions are not needed for binding but are part of the signed
			// TSTInfo and therefore remain covered by the CMS signature.
		default:
			return parsedTSTInfo{}, errorf(ErrMalformedToken, "unexpected TSTInfo field class=%d tag=%d", child.Class, child.Tag)
		}
	}
	return result, nil
}

func parseMessageImprint(value asn1.RawValue) (MessageImprint, error) {
	children, err := parseSequence(value)
	if err != nil || len(children) != 2 {
		return MessageImprint{}, errorf(ErrMalformedToken, "invalid message imprint")
	}
	algorithm, err := parseAlgorithmIdentifier(children[0])
	if err != nil {
		return MessageImprint{}, wrapError(ErrMalformedToken, err)
	}
	hash, name, err := hashForOID(algorithm.OID)
	if err != nil {
		return MessageImprint{}, err
	}
	digest, err := parseOctetString(children[1])
	if err != nil {
		return MessageImprint{}, wrapError(ErrMalformedToken, err)
	}
	if len(digest) != hash.Size() {
		return MessageImprint{}, errorf(ErrDigestAlgorithmMismatch,
			"%s imprint has %d bytes, want %d", name, len(digest), hash.Size())
	}
	return MessageImprint{
		HashAlgorithm: algorithm.OID,
		Hash:          hash,
		HashName:      name,
		HashedMessage: digest,
	}, nil
}

func parseAccuracy(value asn1.RawValue) (*Accuracy, error) {
	children, err := parseSequence(value)
	if err != nil {
		return nil, err
	}
	result := &Accuracy{}
	for index, child := range children {
		number, err := parseInteger(child)
		if err != nil || !number.IsInt64() {
			return nil, errors.New("invalid accuracy value")
		}
		switch index {
		case 0:
			result.Seconds = int(number.Int64())
		case 1:
			result.Millis = int(number.Int64())
		case 2:
			result.Micros = int(number.Int64())
		default:
			return nil, errors.New("too many accuracy values")
		}
	}
	return result, nil
}

func cloneBigInt(value *big.Int) *big.Int {
	if value == nil {
		return nil
	}
	return new(big.Int).Set(value)
}

func digestAlgorithmName(oid asn1.ObjectIdentifier) string {
	_, name, err := hashForOID(oid)
	if err != nil {
		return ""
	}
	return name
}
