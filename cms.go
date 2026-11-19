package netsec

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
)

var (
	oidSignedData    = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	oidData          = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	oidContentType   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 3}
	oidMessageDigest = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}
	oidSHA256        = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidRSAEncryption = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
	oidSHA256WithRSA = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 11}
)

type derElement struct {
	tag     byte
	content []byte
	full    []byte
}

type cmsSignedData struct {
	content []byte
	certs   []*x509.Certificate
	signers []cmsSignerInfo
}

type cmsSignerInfo struct {
	sidKind        int
	issuer         []byte
	serial         *big.Int
	subjectKeyID   []byte
	digestOID      asn1.ObjectIdentifier
	signedAttrs    []byte
	hasSignedAttrs bool
	signatureOID   asn1.ObjectIdentifier
	signature      []byte
}

type cmsAttribute struct {
	oid    asn1.ObjectIdentifier
	values [][]byte
}

func readDER(data []byte) (derElement, []byte, error) {
	var elem derElement
	if len(data) < 2 {
		return elem, nil, errors.New("DER: truncated header")
	}
	elem.tag = data[0]
	if elem.tag&0x1f == 0x1f {
		return elem, nil, errors.New("DER: high-tag-number form is unsupported")
	}
	lengthByte := int(data[1])
	header := 2
	length := lengthByte
	if lengthByte&0x80 != 0 {
		count := lengthByte & 0x7f
		if count == 0 {
			return elem, nil, errors.New("DER: indefinite length is unsupported")
		}
		if count > 4 || len(data) < 2+count {
			return elem, nil, errors.New("DER: invalid length")
		}
		length = 0
		for i := 0; i < count; i++ {
			length = length<<8 | int(data[2+i])
		}
		header += count
		if length < 0 {
			return elem, nil, errors.New("DER: invalid length")
		}
	}
	if length > len(data)-header {
		return elem, nil, errors.New("DER: truncated value")
	}
	elem.content = data[header : header+length]
	elem.full = data[:header+length]
	return elem, data[header+length:], nil
}

func derChildren(elem derElement) ([]derElement, error) {
	var children []derElement
	rest := elem.content
	for len(rest) > 0 {
		child, next, err := readDER(rest)
		if err != nil {
			return nil, err
		}
		children = append(children, child)
		rest = next
	}
	return children, nil
}

func derWrap(tag byte, content []byte) []byte {
	out := []byte{tag}
	length := len(content)
	if length < 0x80 {
		out = append(out, byte(length))
	} else {
		var buf [8]byte
		n := 0
		for v := length; v > 0; v >>= 8 {
			n++
			buf[len(buf)-n] = byte(v)
		}
		out = append(out, 0x80|byte(n))
		out = append(out, buf[len(buf)-n:]...)
	}
	return append(out, content...)
}

func oidFromDER(elem derElement) (asn1.ObjectIdentifier, error) {
	if elem.tag != 0x06 {
		return nil, errors.New("expected OBJECT IDENTIFIER")
	}
	var oid asn1.ObjectIdentifier
	if _, err := asn1.Unmarshal(elem.full, &oid); err != nil {
		return nil, err
	}
	return oid, nil
}

func verifyCMSDetached(sigData, content []byte, cfg *verifyConfig, member string) (*SignatureInfo, bool, error) {
	signedData, handled, err := parseCMSSignedData(sigData)
	if !handled {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, fmt.Errorf("%w: %s: %v", ErrInvalidSignature, member, err)
	}
	if signedData.content != nil && !bytes.Equal(signedData.content, content) {
		return nil, true, fmt.Errorf("%w: %s: encapsulated content differs", ErrInvalidSignature, member)
	}
	if len(signedData.signers) == 0 {
		return nil, true, fmt.Errorf("%w: %s: no SignerInfo", ErrInvalidSignature, member)
	}

	var chainErr error
	var signerErr error
	for _, signer := range signedData.signers {
		info, err := verifyCMSSigner(signedData, signer, content, cfg, member)
		if err == nil {
			return info, true, nil
		}
		if errors.Is(err, ErrUntrustedCertificate) {
			chainErr = err
		} else {
			signerErr = err
		}
	}
	if signerErr != nil {
		return nil, true, signerErr
	}
	if chainErr != nil {
		return nil, true, chainErr
	}
	return nil, true, fmt.Errorf("%w: %s", ErrInvalidSignature, member)
}

func verifyCMSSigner(sd *cmsSignedData, signer cmsSignerInfo, content []byte, cfg *verifyConfig, member string) (*SignatureInfo, error) {
	if !signer.digestOID.Equal(oidSHA256) {
		return nil, fmt.Errorf("%w: %s: digest algorithm %s is not SHA-256",
			ErrInvalidSignature, member, signer.digestOID)
	}
	if !signer.signatureOID.Equal(oidRSAEncryption) && !signer.signatureOID.Equal(oidSHA256WithRSA) {
		return nil, fmt.Errorf("%w: %s: signature algorithm %s is not RSA PKCS#1 v1.5",
			ErrInvalidSignature, member, signer.signatureOID)
	}

	candidates := make([]*x509.Certificate, 0, len(sd.certs)+len(cfg.certs)+1)
	if cfg.signer != nil {
		candidates = append(candidates, cfg.signer)
	}
	candidates = append(candidates, sd.certs...)
	candidates = append(candidates, cfg.certs...)
	cert := findCMSSignerCertificate(deduplicateCertificates(candidates), signer)
	if cert == nil {
		return nil, fmt.Errorf("%w: %s: signer certificate not found", ErrInvalidSignature, member)
	}
	publicKey, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("%w: %s: signer key is not RSA", ErrInvalidSignature, member)
	}

	chain, err := verifyCertificateChain(cert, sd.certs, cfg)
	if err != nil {
		return nil, err
	}

	if signer.hasSignedAttrs {
		attrs, err := parseCMSAttributes(signer.signedAttrs)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrInvalidSignature, member, err)
		}
		contentType, ok := attrs[oidContentType.String()]
		if !ok || len(contentType.values) == 0 {
			return nil, fmt.Errorf("%w: %s: missing contentType attribute", ErrInvalidSignature, member)
		}
		value, err := oidFromDERValue(contentType.values[0])
		if err != nil || !value.Equal(oidData) {
			return nil, fmt.Errorf("%w: %s: wrong contentType attribute", ErrInvalidSignature, member)
		}
		messageDigest, ok := attrs[oidMessageDigest.String()]
		if !ok || len(messageDigest.values) == 0 {
			return nil, fmt.Errorf("%w: %s: missing messageDigest attribute", ErrInvalidSignature, member)
		}
		expectedDigest, err := octetStringValue(messageDigest.values[0])
		if err != nil {
			return nil, fmt.Errorf("%w: %s: invalid messageDigest attribute", ErrInvalidSignature, member)
		}
		actualDigest := sha256.Sum256(content)
		if !bytes.Equal(expectedDigest, actualDigest[:]) {
			return nil, fmt.Errorf("%w: %s: messageDigest attribute does not match", ErrInvalidSignature, member)
		}
		digest := sha256.Sum256(signer.signedAttrs)
		if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signer.signature); err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrInvalidSignature, member, err)
		}
	} else {
		digest := sha256.Sum256(content)
		if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signer.signature); err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrInvalidSignature, member, err)
		}
	}

	return &SignatureInfo{
		Member:        member,
		Algorithm:     "RSA-SHA256",
		SignerSubject: cert.Subject.String(),
		Chain:         chain,
	}, nil
}

func findCMSSignerCertificate(certs []*x509.Certificate, signer cmsSignerInfo) *x509.Certificate {
	for _, cert := range certs {
		switch signer.sidKind {
		case 1:
			if bytes.Equal(cert.RawIssuer, signer.issuer) &&
				signer.serial != nil && cert.SerialNumber.Cmp(signer.serial) == 0 {
				return cert
			}
		case 2:
			if len(signer.subjectKeyID) > 0 && bytes.Equal(cert.SubjectKeyId, signer.subjectKeyID) {
				return cert
			}
		}
	}
	if len(certs) == 1 {
		return certs[0]
	}
	return nil
}

func parseCMSSignedData(data []byte) (*cmsSignedData, bool, error) {
	outer, rest, err := readDER(data)
	if err != nil || len(rest) != 0 || outer.tag != 0x30 {
		return nil, false, nil
	}
	outerChildren, err := derChildren(outer)
	if err != nil || len(outerChildren) < 2 {
		return nil, false, nil
	}
	contentType, err := oidFromDER(outerChildren[0])
	if err != nil || !contentType.Equal(oidSignedData) {
		return nil, false, nil
	}
	if outerChildren[1].tag != 0xA0 {
		return nil, true, errors.New("SignedData content is not [0] EXPLICIT")
	}
	signedDataElem, trailing, err := readDER(outerChildren[1].content)
	if err != nil || len(trailing) != 0 || signedDataElem.tag != 0x30 {
		return nil, true, errors.New("malformed SignedData")
	}
	children, err := derChildren(signedDataElem)
	if err != nil {
		return nil, true, err
	}
	if len(children) < 3 {
		return nil, true, errors.New("short SignedData")
	}

	sd := &cmsSignedData{}
	index := 3
	for index < len(children) {
		switch children[index].tag {
		case 0xA0:
			certs, err := parseCertificateSet(children[index].content)
			if err != nil {
				return nil, true, err
			}
			sd.certs = append(sd.certs, certs...)
			index++
		case 0xA1:
			index++
		case 0x31:
			signers, err := parseSignerInfos(children[index])
			if err != nil {
				return nil, true, err
			}
			sd.signers = append(sd.signers, signers...)
			index++
		default:
			return nil, true, fmt.Errorf("unexpected SignedData field with tag 0x%02x", children[index].tag)
		}
	}

	encapChildren, err := derChildren(children[2])
	if err != nil || len(encapChildren) == 0 {
		return nil, true, errors.New("malformed EncapsulatedContentInfo")
	}
	if len(encapChildren) > 1 && encapChildren[1].tag == 0xA0 {
		inner, trailing, err := readDER(encapChildren[1].content)
		if err != nil || len(trailing) != 0 || inner.tag != 0x04 {
			return nil, true, errors.New("malformed encapsulated content")
		}
		sd.content = append([]byte(nil), inner.content...)
	}
	return sd, true, nil
}

func parseCertificateSet(content []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	rest := content
	for len(rest) > 0 {
		elem, next, err := readDER(rest)
		if err != nil {
			return nil, err
		}
		rest = next
		if elem.tag != 0x30 {
			continue
		}
		cert, err := x509.ParseCertificate(elem.full)
		if err != nil {
			continue
		}
		certs = append(certs, cert)
	}
	return certs, nil
}

func parseSignerInfos(set derElement) ([]cmsSignerInfo, error) {
	children, err := derChildren(set)
	if err != nil {
		return nil, err
	}
	signers := make([]cmsSignerInfo, 0, len(children))
	for _, child := range children {
		if child.tag != 0x30 {
			return nil, errors.New("SignerInfo is not a SEQUENCE")
		}
		signer, err := parseSignerInfo(child)
		if err != nil {
			return nil, err
		}
		signers = append(signers, signer)
	}
	return signers, nil
}

func parseSignerInfo(elem derElement) (cmsSignerInfo, error) {
	var signer cmsSignerInfo
	children, err := derChildren(elem)
	if err != nil {
		return signer, err
	}
	if len(children) < 5 {
		return signer, errors.New("short SignerInfo")
	}
	sid := children[1]
	switch sid.tag {
	case 0x30:
		sidChildren, err := derChildren(sid)
		if err != nil || len(sidChildren) < 2 {
			return signer, errors.New("malformed IssuerAndSerialNumber")
		}
		signer.sidKind = 1
		signer.issuer = append([]byte(nil), sidChildren[0].full...)
		var serial *big.Int
		if _, err := asn1.Unmarshal(sidChildren[1].full, &serial); err != nil {
			return signer, fmt.Errorf("parse signer serial: %w", err)
		}
		signer.serial = serial
	case 0xA0:
		signer.sidKind = 2
		signer.subjectKeyID = append([]byte(nil), sid.content...)
	default:
		return signer, errors.New("unsupported SignerIdentifier")
	}

	signer.digestOID, err = parseAlgorithmOID(children[2])
	if err != nil {
		return signer, fmt.Errorf("parse digest algorithm: %w", err)
	}

	index := 3
	if children[index].tag == 0xA0 {
		signer.hasSignedAttrs = true
		signer.signedAttrs = derWrap(0x31, children[index].content)
		index++
	}
	if len(children) < index+2 {
		return signer, errors.New("short SignerInfo after attributes")
	}
	signer.signatureOID, err = parseAlgorithmOID(children[index])
	if err != nil {
		return signer, fmt.Errorf("parse signature algorithm: %w", err)
	}
	index++
	if children[index].tag != 0x04 {
		return signer, errors.New("signature is not an OCTET STRING")
	}
	signer.signature = append([]byte(nil), children[index].content...)
	if len(signer.signature) == 0 {
		return signer, errors.New("empty signature")
	}
	return signer, nil
}

func parseAlgorithmOID(elem derElement) (asn1.ObjectIdentifier, error) {
	if elem.tag != 0x30 {
		return nil, errors.New("AlgorithmIdentifier is not a SEQUENCE")
	}
	children, err := derChildren(elem)
	if err != nil || len(children) == 0 {
		return nil, errors.New("malformed AlgorithmIdentifier")
	}
	return oidFromDER(children[0])
}

func parseCMSAttributes(der []byte) (map[string]cmsAttribute, error) {
	set, rest, err := readDER(der)
	if err != nil || len(rest) != 0 || set.tag != 0x31 {
		return nil, errors.New("SignedAttributes is not a SET")
	}
	children, err := derChildren(set)
	if err != nil {
		return nil, err
	}
	attrs := make(map[string]cmsAttribute, len(children))
	for _, child := range children {
		if child.tag != 0x30 {
			return nil, errors.New("attribute is not a SEQUENCE")
		}
		attrChildren, err := derChildren(child)
		if err != nil || len(attrChildren) < 2 || attrChildren[1].tag != 0x31 {
			return nil, errors.New("malformed attribute")
		}
		oid, err := oidFromDER(attrChildren[0])
		if err != nil {
			return nil, err
		}
		values, err := derChildren(attrChildren[1])
		if err != nil {
			return nil, err
		}
		attr := cmsAttribute{oid: oid}
		for _, value := range values {
			attr.values = append(attr.values, value.full)
		}
		key := oid.String()
		if existing, ok := attrs[key]; ok {
			existing.values = append(existing.values, attr.values...)
			attrs[key] = existing
		} else {
			attrs[key] = attr
		}
	}
	return attrs, nil
}

func oidFromDERValue(value []byte) (asn1.ObjectIdentifier, error) {
	elem, rest, err := readDER(value)
	if err != nil || len(rest) != 0 {
		return nil, errors.New("malformed OID attribute value")
	}
	return oidFromDER(elem)
}

func octetStringValue(value []byte) ([]byte, error) {
	elem, rest, err := readDER(value)
	if err != nil || len(rest) != 0 || elem.tag != 0x04 {
		return nil, errors.New("attribute value is not an OCTET STRING")
	}
	return elem.content, nil
}
