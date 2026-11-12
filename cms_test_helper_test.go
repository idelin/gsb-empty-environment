package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"time"
)

type keyKind int

const (
	keyRSA keyKind = iota
	keyECDSA
	keyEd25519
)

type testCert struct {
	cert *x509.Certificate
	key  any
}

func generateTestCert(kind keyKind) (*testCert, error) {
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   "archive-import-test",
			Organization: []string{"netsec tests"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		SubjectKeyId:          []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x43},
	}

	switch kind {
	case keyRSA:
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, err
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
		if err != nil {
			return nil, err
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, err
		}
		return &testCert{cert: cert, key: key}, nil

	case keyECDSA:
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
		if err != nil {
			return nil, err
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, err
		}
		return &testCert{cert: cert, key: key}, nil

	case keyEd25519:
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
		if err != nil {
			return nil, err
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, err
		}
		return &testCert{cert: cert, key: priv}, nil

	default:
		panic("unknown key kind")
	}
}

// signatureSpec maps a key type to the CMS algorithm OIDs used in tests.
type signatureSpec struct {
	digestOID asn1.ObjectIdentifier
	sigOID    asn1.ObjectIdentifier
	hash      crypto.Hash
	pss       bool
}

func specForKind(kind keyKind) signatureSpec {
	switch kind {
	case keyRSA:
		return signatureSpec{digestOID: oidSHA256, sigOID: oidRSAEncryption, hash: crypto.SHA256}
	case keyECDSA:
		return signatureSpec{digestOID: oidSHA256, sigOID: oidECDSAWithSHA256, hash: crypto.SHA256}
	case keyEd25519:
		return signatureSpec{digestOID: oidSHA512, sigOID: oidEd25519, hash: crypto.SHA512}
	default:
		panic("unknown key kind")
	}
}

// builderOptions controls how the SignedData test fixture is assembled.
type builderOptions struct {
	omitSignedAttrs bool
	// attrMutator rewrites the signed attribute set before it is encoded.
	attrMutator func([]Attribute) []Attribute
	// digestOverride, when non-nil, is placed into the message-digest
	// attribute instead of the real content digest.
	digestOverride []byte
	// contentTypeOverride, when non-nil, is placed into the content-type
	// attribute instead of eContentType.
	contentTypeOverride asn1.ObjectIdentifier
	// contentOverride, when non-nil, replaces the eContent octets.
	contentOverride []byte
	// signOverUntagged makes the signature cover the on-wire A0 encoding
	// instead of the canonical SET (0x31) encoding.
	signOverUntagged bool
	// digestAlgOverride replaces the SignerInfo digest algorithm OID.
	digestAlgOverride *asn1.ObjectIdentifier
	// sigAlgOverride replaces the SignerInfo signature algorithm OID.
	sigAlgOverride *asn1.ObjectIdentifier
	// usePSS selects id-RSASSA-PSS with explicit SHA-256/MGF-1 parameters
	// for RSA signers.
	usePSS bool
	// useSKI makes the SignerInfo use the subjectKeyIdentifier CHOICE.
	useSKI bool
	// detached omits eContent from the encoded SignedData.
	detached bool
	// signWrongContent computes the message-digest over different bytes
	// than those placed in eContent.
	signWrongContent bool
}

// buildSignedData produces a DER-encoded CMS SignedData ContentInfo with one
// signer and the given content.
func buildSignedData(tc *testCert, kind keyKind, content []byte, opts builderOptions) ([]byte, error) {
	spec := specForKind(kind)
	if opts.digestAlgOverride != nil {
		spec.digestOID = *opts.digestAlgOverride
	}
	if opts.sigAlgOverride != nil {
		spec.sigOID = *opts.sigAlgOverride
	}
	if opts.usePSS {
		spec.sigOID = oidRSASSA_PSS
		spec.pss = true
	}

	eContentType := oidData
	digestContent := content
	if opts.signWrongContent {
		digestContent = append([]byte(nil), content...)
		digestContent = append(digestContent, '-', 'w', 'r', 'o', 'n', 'g')
	}
	contentDigest := hashBytes(spec.hash, digestContent)

	signer := SignerInfo{
		Version:            1,
		DigestAlgorithm:    AlgorithmIdentifier{Algorithm: spec.digestOID},
		SignatureAlgorithm: buildSignatureAlgorithm(spec),
	}

	if opts.useSKI {
		signer.Version = 3
		signer.SID = SignerIdentifier{SubjectKeyIdentifier: tc.cert.SubjectKeyId}
	} else {
		signer.SID = SignerIdentifier{
			IssuerAndSerialNumber: IssuerAndSerialNumber{
				Issuer: asn1.RawValue{FullBytes: tc.cert.RawIssuer},
			},
		}
		signer.SID.IssuerAndSerialNumber.SerialNumber = tc.cert.SerialNumber
	}

	var signedInput []byte
	if !opts.omitSignedAttrs {
		attrs := []Attribute{
			{
				Type: oidContentType,
				Values: []asn1.RawValue{
					marshalOIDValue(eContentType),
				},
			},
			{
				Type: oidMessageDigest,
				Values: []asn1.RawValue{
					marshalOctetStringValue(contentDigest),
				},
			},
			{
				Type:   oidSigningTime,
				Values: []asn1.RawValue{marshalTimeValue()},
			},
		}

		if opts.contentTypeOverride != nil {
			attrs[0].Values[0] = marshalOIDValue(opts.contentTypeOverride)
		}
		if opts.digestOverride != nil {
			attrs[1].Values[0] = marshalOctetStringValue(opts.digestOverride)
		}
		if opts.attrMutator != nil {
			attrs = opts.attrMutator(attrs)
		}

		setDER, err := asn1.Marshal(attrs)
		if err != nil {
			return nil, err
		}
		var setRaw asn1.RawValue
		if _, err := asn1.Unmarshal(setDER, &setRaw); err != nil {
			return nil, err
		}
		// RFC 5652 signedAttrs is a SET OF (tag 0x31); encoding/asn1
		// produces SEQUENCE OF (0x30) for a slice, so retag before use.
		canonicalSET := make([]byte, len(setRaw.FullBytes))
		copy(canonicalSET, setRaw.FullBytes)
		canonicalSET[0] = 0x31
		// [0] IMPLICIT SET: same length/contents, context tag A0.
		implicit := make([]byte, len(setRaw.FullBytes))
		copy(implicit, setRaw.FullBytes)
		implicit[0] = 0xA0
		signer.SignedAttrs = asn1.RawValue{
			Class:      asn1.ClassContextSpecific,
			Tag:        0,
			IsCompound: true,
			Bytes:      setRaw.Bytes,
			FullBytes:  implicit,
		}

		if opts.signOverUntagged {
			signedInput = implicit
		} else {
			signedInput = canonicalSET
		}
	} else {
		signedInput = digestContent
	}

	sig, err := sign(tc.key, spec.sigOID, spec.hash, signedInput)
	if err != nil {
		return nil, err
	}
	signer.Signature = sig

	eci := EncapsulatedContentInfo{EContentType: eContentType}
	if !opts.detached {
		encodedContent := content
		if opts.contentOverride != nil {
			encodedContent = opts.contentOverride
		}
		octetsDER, err := asn1.Marshal(encodedContent)
		if err != nil {
			return nil, err
		}
		eci.EContent = asn1.RawValue{
			Class:      asn1.ClassContextSpecific,
			Tag:        0,
			IsCompound: true,
			Bytes:      octetsDER,
		}
	}

	sd := SignedData{
		Version: 1,
		DigestAlgorithms: []AlgorithmIdentifier{
			{Algorithm: spec.digestOID},
		},
		EncapContentInfo: eci,
		Certificates:     explicitContext0(tc.cert.Raw),
		SignerInfos:      []SignerInfo{signer},
	}

	sdDER, err := asn1.Marshal(sd)
	if err != nil {
		return nil, err
	}
	ci := ContentInfo{
		ContentType: oidSignedData,
		Content:     explicitContext0(sdDER),
	}
	return asn1.Marshal(ci)
}

// explicitContext0 wraps DER in a constructed context-specific [0] value.
func explicitContext0(inner []byte) asn1.RawValue {
	return asn1.RawValue{
		Class:      asn1.ClassContextSpecific,
		Tag:        0,
		IsCompound: true,
		Bytes:      inner,
	}
}

func marshalOIDValue(oid asn1.ObjectIdentifier) asn1.RawValue {
	der, err := asn1.Marshal(oid)
	if err != nil {
		panic(err)
	}
	var raw asn1.RawValue
	if _, err := asn1.Unmarshal(der, &raw); err != nil {
		panic(err)
	}
	return raw
}

func marshalOctetStringValue(b []byte) asn1.RawValue {
	der, err := asn1.Marshal(b)
	if err != nil {
		panic(err)
	}
	var raw asn1.RawValue
	if _, err := asn1.Unmarshal(der, &raw); err != nil {
		panic(err)
	}
	return raw
}

func marshalTimeValue() asn1.RawValue {
	der, err := asn1.Marshal(time.Now().UTC())
	if err != nil {
		panic(err)
	}
	var raw asn1.RawValue
	if _, err := asn1.Unmarshal(der, &raw); err != nil {
		panic(err)
	}
	return raw
}

// buildSignatureAlgorithm returns the SignerInfo signatureAlgorithm,
// including the explicit RSASSA-PSS parameter block when required.
func buildSignatureAlgorithm(spec signatureSpec) AlgorithmIdentifier {
	ai := AlgorithmIdentifier{Algorithm: spec.sigOID}
	if !spec.pss {
		return ai
	}
	params := rsaPSSParams{
		HashAlg:    AlgorithmIdentifier{Algorithm: spec.digestOID},
		MaskAlg:    pssMaskGenAlgorithm{Algorithm: oidMGF1, Parameters: AlgorithmIdentifier{Algorithm: spec.digestOID}},
		SaltLength: spec.hash.Size(),
	}
	der, err := asn1.Marshal(params)
	if err != nil {
		panic(err)
	}
	var raw asn1.RawValue
	if _, err := asn1.Unmarshal(der, &raw); err != nil {
		panic(err)
	}
	ai.Parameters = raw
	return ai
}
