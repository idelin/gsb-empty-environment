package rfc3161

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"math/big"
)

// CMS ContentInfo / SignedData (RFC 5652 sections 3, 5).

type contentInfo struct {
	ContentType asn1.ObjectIdentifier
	Content     asn1.RawValue `asn1:"explicit,tag:0"`
}

type signedData struct {
	Version          int
	DigestAlgorithms []pkix.AlgorithmIdentifier
	EncapContentInfo encapContentInfo
	Certificates     asn1.RawValue `asn1:"optional,implicit,tag:0"`
	CRLs             asn1.RawValue `asn1:"optional,tag:1"`
	SignerInfos      asn1.RawValue
}

type encapContentInfo struct {
	EContentType asn1.ObjectIdentifier
	EContent     []byte `asn1:"explicit,tag:0,optional"`
}

type signerInfo struct {
	Version            int
	SID                asn1.RawValue `asn1:"implicit,tag:0"`
	DigestAlgorithm    pkix.AlgorithmIdentifier
	SignedAttrs        asn1.RawValue `asn1:"optional,implicit,tag:0"`
	SignatureAlgorithm pkix.AlgorithmIdentifier
	Signature          []byte
	UnsignedAttrs      asn1.RawValue `asn1:"optional,tag:1"`
}

type signedAttribute struct {
	Type  asn1.ObjectIdentifier
	Value asn1.RawValue
}

// rsaPSSParams mirrors RSASSA-PSS-params (RFC 4055 section 3.1).
type rsaPSSParams struct {
	HashAlg      pkix.AlgorithmIdentifier `asn1:"optional,tag:0"`
	MaskGenAlg   maskGenAlgorithm         `asn1:"optional,tag:1"`
	SaltLength   int                      `asn1:"optional,tag:2"`
	TrailerField int                      `asn1:"optional,tag:3"`
}

type maskGenAlgorithm struct {
	Algorithm  asn1.ObjectIdentifier
	Parameters asn1.RawValue
}

type parsedToken struct {
	signedData signedData
	eContent   []byte
	signer     *x509.Certificate
	signerInfo signerInfo
	info       tstInfo
}

// parseToken performs the structural checks of RFC 5652 needed to locate the
// TSTInfo, the signing certificate and the single signer's signature.
func parseToken(tokenDER []byte) (*parsedToken, error) {
	var ci contentInfo
	rest, err := asn1.Unmarshal(tokenDER, &ci)
	if err != nil {
		return nil, errors.Join(ErrInvalidToken, err)
	}
	if len(rest) != 0 {
		return nil, errors.Join(ErrInvalidToken, errors.New("trailing data after ContentInfo"))
	}
	if !ci.ContentType.Equal(oidSignedData) {
		return nil, errors.Join(ErrInvalidToken, errors.New("outer ContentInfo is not id-signedData"))
	}

	var sd signedData
	rest, err = asn1.Unmarshal(ci.Content.Bytes, &sd)
	if err != nil {
		return nil, errors.Join(ErrInvalidToken, err)
	}
	if len(rest) != 0 {
		return nil, errors.Join(ErrInvalidToken, errors.New("trailing data after SignedData"))
	}

	if sd.Version != 3 && sd.Version != 1 {
		return nil, errors.Join(ErrInvalidToken, errors.New("unexpected SignedData version"))
	}
	if len(sd.DigestAlgorithms) == 0 {
		return nil, errors.Join(ErrInvalidToken, errors.New("SignedData has no digest algorithms"))
	}
	var signerInfos []signerInfo
	setRest := sd.SignerInfos.Bytes
	for len(setRest) > 0 {
		var siRaw asn1.RawValue
		var err error
		setRest, err = asn1.Unmarshal(setRest, &siRaw)
		if err != nil {
			return nil, errors.Join(ErrInvalidToken, err)
		}
		si, err := parseSignerInfo(siRaw.FullBytes)
		if err != nil {
			return nil, errors.Join(ErrInvalidToken, err)
		}
		signerInfos = append(signerInfos, si)
	}
	if len(signerInfos) != 1 {
		return nil, errors.Join(ErrInvalidToken, errors.New("TimeStampToken must contain exactly one SignerInfo"))
	}
	if !sd.EncapContentInfo.EContentType.Equal(oidTSTInfo) {
		return nil, errors.Join(ErrInvalidToken, errors.New("encapsulated content is not id-ct-TSTInfo"))
	}
	if len(sd.EncapContentInfo.EContent) == 0 {
		return nil, errors.Join(ErrInvalidToken, errors.New("missing encapsulated TSTInfo content"))
	}

	cert, err := findSignerCert(sd.Certificates, signerInfos[0].SID)
	if err != nil {
		return nil, err
	}

	var info tstInfo
	rest, err = asn1.Unmarshal(sd.EncapContentInfo.EContent, &info)
	if err != nil {
		return nil, errors.Join(ErrInvalidToken, err)
	}
	if len(rest) != 0 {
		return nil, errors.Join(ErrInvalidToken, errors.New("trailing data after TSTInfo"))
	}

	return &parsedToken{
		signedData: sd,
		eContent:   sd.EncapContentInfo.EContent,
		signer:     cert,
		signerInfo: signerInfos[0],
		info:       info,
	}, nil
}

// findSignerCert parses the IMPLICIT [0] certificate bag and matches the
// SignerIdentifier. SKI identifiers are supported in addition to the RFC 3161
// default IssuerAndSerialNumber.
func findSignerCert(bag asn1.RawValue, sid asn1.RawValue) (*x509.Certificate, error) {
	if len(bag.Bytes) == 0 {
		return nil, errors.Join(ErrInvalidToken, errors.New("SignedData carries no certificates"))
	}

	var certs []*x509.Certificate
	rest := bag.Bytes
	for len(rest) > 0 {
		var raw asn1.RawValue
		var err error
		rest, err = asn1.Unmarshal(rest, &raw)
		if err != nil {
			return nil, errors.Join(ErrInvalidToken, err)
		}
		// Certificates are SEQUENCE; attribute certificates ([1]/[2]) are skipped.
		if raw.Tag == asn1.TagSequence && raw.Class == asn1.ClassUniversal {
			cert, err := x509.ParseCertificate(raw.FullBytes)
			if err != nil {
				return nil, errors.Join(ErrInvalidToken, err)
			}
			certs = append(certs, cert)
		}
	}
	if len(certs) == 0 {
		return nil, errors.Join(ErrInvalidToken, errors.New("no usable certificate in SignedData certificate bag"))
	}

	switch {
	case sid.Tag == 0 && sid.Class == asn1.ClassContextSpecific:
		rawIssuer, serial, err := parseIssuerAndSerial(sid.Bytes)
		if err != nil {
			return nil, errors.Join(ErrInvalidToken, err)
		}
		for _, cert := range certs {
			if cert.SerialNumber.Cmp(serial) == 0 &&
				equalRawBytes(cert.RawIssuer, rawIssuer) {
				return cert, nil
			}
		}
	case sid.Tag == 2 && sid.Class == asn1.ClassContextSpecific:
		var ski []byte
		if _, err := asn1.Unmarshal(sid.Bytes, &ski); err != nil {
			return nil, errors.Join(ErrInvalidToken, err)
		}
		for _, cert := range certs {
			if equalRawBytes(cert.SubjectKeyId, ski) {
				return cert, nil
			}
		}
	default:
		return nil, errors.Join(ErrInvalidToken, errors.New("unsupported SignerIdentifier type"))
	}
	return nil, errors.Join(ErrInvalidToken, errors.New("signer certificate matching SignerIdentifier not found in certificate bag"))
}

// parseIssuerAndSerial manually splits IssuerAndSerialNumber (RFC 5652
// section 5.3) without going through Go's SET-tag-aware struct decoding,
// which mis-handles nested RawValues when the enclosing SignerInfos is a SET.
func parseIssuerAndSerial(data []byte) (rawIssuer []byte, serial *big.Int, err error) {
	// data is the content of SignerIdentifier [0] IMPLICIT, i.e. the body
	// (without SEQUENCE header) of IssuerAndSerialNumber.
	var issuer asn1.RawValue
	rest, e := asn1.Unmarshal(data, &issuer)
	if e != nil || len(rest) == 0 {
		return nil, nil, errors.New("invalid IssuerAndSerialNumber issuer")
	}
	var sn *big.Int
	if _, e := asn1.Unmarshal(rest, &sn); e != nil {
		return nil, nil, errors.New("invalid IssuerAndSerialNumber serial")
	}
	return issuer.FullBytes, sn, nil
}

func equalRawBytes(a, b []byte) bool {
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

// parseSignerInfo manually decodes a SignerInfo SEQUENCE. Go's encoding/asn1
// retags nested SEQUENCE values as SETs when a SET OF encloses the struct, so
// the fields are split element by element with RawValues to keep DER intact.
func parseSignerInfo(der []byte) (signerInfo, error) {
	var si signerInfo
	var outer asn1.RawValue
	rest, err := asn1.Unmarshal(der, &outer)
	if err != nil || len(rest) != 0 {
		return si, errors.New("invalid SignerInfo")
	}

	var version asn1.RawValue
	rest, err = asn1.Unmarshal(outer.Bytes, &version)
	if err != nil {
		return si, err
	}
	if _, err := asn1.Unmarshal(version.FullBytes, &si.Version); err != nil {
		return si, err
	}

	// SignerIdentifier: CHOICE { issuerAndSerialNumber [0], subjectKeyIdentifier [2] }.
	var sid asn1.RawValue
	rest, err = asn1.Unmarshal(rest, &sid)
	if err != nil {
		return si, err
	}
	if sid.Class != asn1.ClassContextSpecific || (sid.Tag != 0 && sid.Tag != 2) {
		return si, errors.New("invalid SignerIdentifier")
	}
	si.SID = sid

	if rest, err = asn1.Unmarshal(rest, &si.DigestAlgorithm); err != nil {
		return si, err
	}

	// SignedAttrs [0] IMPLICIT is optional.
	if len(rest) > 0 {
		var next asn1.RawValue
		if _, e := asn1.Unmarshal(rest, &next); e == nil &&
			next.Class == asn1.ClassContextSpecific && next.Tag == 0 {
			si.SignedAttrs = next
			rest = rest[len(next.FullBytes):]
		}
	}

	if rest, err = asn1.Unmarshal(rest, &si.SignatureAlgorithm); err != nil {
		return si, err
	}
	if rest, err = asn1.Unmarshal(rest, &si.Signature); err != nil {
		return si, err
	}

	if len(rest) > 0 {
		var unsigned asn1.RawValue
		if _, e := asn1.Unmarshal(rest, &unsigned); e != nil {
			return si, e
		}
		if unsigned.Class != asn1.ClassContextSpecific || unsigned.Tag != 1 {
			return si, errors.New("invalid unsignedAttrs")
		}
		si.UnsignedAttrs = unsigned
		rest = rest[len(unsigned.FullBytes):]
	}
	if len(rest) != 0 {
		return si, errors.New("trailing bytes in SignerInfo")
	}
	return si, nil
}

// parseSignedAttributes decodes the SET OF Attribute carried in signedAttrs
// [0] IMPLICIT, iterating element by element so Go's SET-tag handling does not
// retag the inner SEQUENCE/SET values.
func parseSignedAttributes(data []byte) ([]signedAttribute, error) {
	var attrs []signedAttribute
	rest := data
	for len(rest) > 0 {
		var attrRaw asn1.RawValue
		var err error
		rest, err = asn1.Unmarshal(rest, &attrRaw)
		if err != nil {
			return nil, err
		}
		var attr signedAttribute
		if _, err := asn1.Unmarshal(attrRaw.FullBytes, &attr); err != nil {
			return nil, err
		}
		attrs = append(attrs, attr)
	}
	return attrs, nil
}

// verifySignature checks the message-digest signed attribute against the
// eContent and the CMS signature value against the DER-encoded signed
// attributes (RFC 5652 sections 5.4 and 5.6).
func (t *parsedToken) verifySignature() error {
	si := t.signerInfo
	if len(si.SignedAttrs.Bytes) == 0 {
		return errors.Join(ErrInvalidToken, errors.New("SignerInfo has no signed attributes"))
	}

	attrs, err := parseSignedAttributes(si.SignedAttrs.Bytes)
	if err != nil {
		return errors.Join(ErrInvalidToken, err)
	}

	var (
		gotContentType  asn1.ObjectIdentifier
		haveContentType bool
		gotDigest       []byte
	)
	for _, attr := range attrs {
		switch {
		case attr.Type.Equal(oidContentType):
			var valueSet asn1.RawValue
			if _, err := asn1.Unmarshal(attr.Value.FullBytes, &valueSet); err != nil {
				return errors.Join(ErrInvalidToken, err)
			}
			if _, err := asn1.Unmarshal(valueSet.Bytes, &gotContentType); err != nil {
				return errors.Join(ErrInvalidToken, err)
			}
			haveContentType = true
		case attr.Type.Equal(oidMessageDgst):
			var valueSet asn1.RawValue
			if _, err := asn1.Unmarshal(attr.Value.FullBytes, &valueSet); err != nil {
				return errors.Join(ErrInvalidToken, err)
			}
			if _, err := asn1.Unmarshal(valueSet.Bytes, &gotDigest); err != nil {
				return errors.Join(ErrInvalidToken, err)
			}
		}
	}
	if !haveContentType {
		return errors.Join(ErrInvalidToken, errors.New("missing content-type signed attribute"))
	}
	if len(gotDigest) == 0 {
		return errors.Join(ErrInvalidToken, errors.New("missing message-digest signed attribute"))
	}
	if !gotContentType.Equal(oidTSTInfo) {
		return errors.Join(ErrSignature, errors.New("content-type attribute does not match id-ct-TSTInfo"))
	}

	digestHash, ok := oidHashes[si.DigestAlgorithm.Algorithm.String()]
	if !ok {
		return errors.Join(ErrInvalidToken, errors.New("unsupported CMS digest algorithm: "+si.DigestAlgorithm.Algorithm.String()))
	}
	if !digestHash.Available() {
		return errors.Join(ErrInvalidToken, errors.New("CMS digest hash is not linked into the binary"))
	}
	hasher := digestHash.New()
	hasher.Write(t.eContent)
	if !equalRawBytes(hasher.Sum(nil), gotDigest) {
		return errors.Join(ErrSignature, errors.New("message-digest attribute does not match TSTInfo digest"))
	}

	// The signature input is the re-encoded SET of signed attributes (the
	// IMPLICIT [0] tag replaced by the universal SET tag).
	signatureInput := makeSetTLV(si.SignedAttrs.Bytes)

	if err := verifyWithPublicKey(t.signer.PublicKey, si.SignatureAlgorithm, digestHash, signatureInput, si.Signature); err != nil {
		return err
	}

	// The SignedData digestAlgorithms set must include the signer's algorithm.
	found := false
	for _, alg := range t.signedData.DigestAlgorithms {
		if alg.Algorithm.Equal(si.DigestAlgorithm.Algorithm) {
			found = true
			break
		}
	}
	if !found {
		return errors.Join(ErrInvalidToken, errors.New("signer digest algorithm absent from SignedData digestAlgorithms"))
	}
	return nil
}

func verifyWithPublicKey(pub any, sigAlg pkix.AlgorithmIdentifier, digestHash crypto.Hash, input, signature []byte) error {
	oid := sigAlg.Algorithm
	switch {
	case oid.Equal(oidRSAEncryption):
		if hasParameters(sigAlg) {
			return errors.Join(ErrInvalidToken, errors.New("rsaEncryption signature algorithm must have absent or NULL parameters"))
		}
		rsaKey, ok := pub.(*rsa.PublicKey)
		if !ok {
			return errors.Join(ErrSignature, errors.New("signer public key is not RSA"))
		}
		hashed := hashBytes(digestHash, input)
		if err := rsa.VerifyPKCS1v15(rsaKey, digestHash, hashed, signature); err != nil {
			return errors.Join(ErrSignature, err)
		}

	case oid.Equal(oidRSAPSS):
		rsaKey, ok := pub.(*rsa.PublicKey)
		if !ok {
			return errors.Join(ErrSignature, errors.New("signer public key is not RSA"))
		}
		opts, err := parsePSSOptions(sigAlg, digestHash)
		if err != nil {
			return err
		}
		hashed := hashBytes(digestHash, input)
		if err := rsa.VerifyPSS(rsaKey, digestHash, hashed, signature, opts); err != nil {
			return errors.Join(ErrSignature, err)
		}

	case oid.Equal(oidECDSAWithSHA1), oid.Equal(oidECDSAWithSHA224),
		oid.Equal(oidECDSAWithSHA256), oid.Equal(oidECDSAWithSHA384),
		oid.Equal(oidECDSAWithSHA512):
		ecKey, ok := pub.(*ecdsa.PublicKey)
		if !ok {
			return errors.Join(ErrSignature, errors.New("signer public key is not ECDSA"))
		}
		sigHash, ok := ecdsaHashOIDs[oid.String()]
		if !ok {
			return errors.Join(ErrInvalidToken, errors.New("unknown ECDSA signature hash"))
		}
		if sigHash != digestHash {
			return errors.Join(ErrInvalidToken, errors.New("ECDSA signature hash does not match CMS digest algorithm"))
		}
		hashed := hashBytes(sigHash, input)
		if !ecdsa.VerifyASN1(ecKey, hashed, signature) {
			return errors.Join(ErrSignature, errors.New("ECDSA signature is invalid"))
		}

	case oid.Equal(oidEd25519):
		edKey, ok := pub.(ed25519.PublicKey)
		if !ok {
			return errors.Join(ErrSignature, errors.New("signer public key is not Ed25519"))
		}
		if !ed25519.Verify(edKey, input, signature) {
			return errors.Join(ErrSignature, errors.New("Ed25519 signature is invalid"))
		}

	default:
		return errors.Join(ErrInvalidToken, errors.New("unsupported signature algorithm: "+oid.String()))
	}
	return nil
}

func parsePSSOptions(sigAlg pkix.AlgorithmIdentifier, digestHash crypto.Hash) (*rsa.PSSOptions, error) {
	var params rsaPSSParams
	if len(sigAlg.Parameters.Bytes) != 0 {
		if _, err := asn1.Unmarshal(sigAlg.Parameters.FullBytes, &params); err != nil {
			return nil, errors.Join(ErrInvalidToken, err)
		}
	}

	// RFC 4055 defaults: sha1, mgf1SHA1, saltLength=20, trailerField=1.
	hash := crypto.SHA1
	if len(params.HashAlg.Algorithm) != 0 {
		h, ok := oidHashes[params.HashAlg.Algorithm.String()]
		if !ok {
			return nil, errors.Join(ErrInvalidToken, errors.New("unsupported RSA-PSS hash algorithm"))
		}
		hash = h
	}
	if hash != digestHash {
		return nil, errors.Join(ErrInvalidToken, errors.New("RSA-PSS hash does not match CMS digest algorithm"))
	}

	mgf := params.MaskGenAlg
	if len(mgf.Algorithm) == 0 {
		mgf.Algorithm = oidMGF1
	}
	if !mgf.Algorithm.Equal(oidMGF1) {
		return nil, errors.Join(ErrInvalidToken, errors.New("unsupported RSA-PSS mask generation algorithm"))
	}
	mgfHash := crypto.SHA1
	if len(mgf.Parameters.Bytes) != 0 {
		var mgfAlgID pkix.AlgorithmIdentifier
		if _, err := asn1.Unmarshal(mgf.Parameters.FullBytes, &mgfAlgID); err != nil {
			return nil, errors.Join(ErrInvalidToken, err)
		}
		h, ok := oidHashes[mgfAlgID.Algorithm.String()]
		if !ok {
			return nil, errors.Join(ErrInvalidToken, errors.New("unsupported RSA-PSS MGF-1 hash"))
		}
		mgfHash = h
	}
	// Go's crypto/rsa derives the MGF-1 hash from Hash; require the standard
	// combination where mask and message hash agree.
	if mgfHash != hash {
		return nil, errors.Join(ErrInvalidToken, errors.New("RSA-PSS MGF-1 hash differs from message hash"))
	}

	saltLength := 20
	if params.SaltLength != 0 {
		saltLength = params.SaltLength
	}
	trailer := 1
	if params.TrailerField != 0 {
		trailer = params.TrailerField
	}
	if trailer != 1 {
		return nil, errors.Join(ErrInvalidToken, errors.New("unsupported RSA-PSS trailer field"))
	}

	return &rsa.PSSOptions{
		SaltLength: saltLength,
		Hash:       hash,
	}, nil
}

func hasParameters(alg pkix.AlgorithmIdentifier) bool {
	return len(alg.Parameters.FullBytes) != 0 &&
		!(alg.Parameters.Tag == asn1.TagNull && len(alg.Parameters.Bytes) == 0)
}

func hashBytes(hash crypto.Hash, data []byte) []byte {
	h := hash.New()
	h.Write(data)
	return h.Sum(nil)
}

// makeSetTLV wraps already-encoded SET OF content with the universal SET tag
// (0x31) and a DER length, without reinterpreting the inner element bytes.
func makeSetTLV(content []byte) []byte {
	n := len(content)
	var header []byte
	if n < 0x80 {
		header = []byte{0x31, byte(n)}
	} else {
		var lengthBytes []byte
		for n > 0 {
			lengthBytes = append([]byte{byte(n & 0xff)}, lengthBytes...)
			n >>= 8
		}
		header = append([]byte{0x31, 0x80 | byte(len(lengthBytes))}, lengthBytes...)
	}
	return append(header, content...)
}
