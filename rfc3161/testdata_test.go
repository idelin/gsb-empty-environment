package rfc3161

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"math/big"
	"time"
)

type testTSA struct {
	rootCert *x509.Certificate
	rootKey  crypto.Signer
	cert     *x509.Certificate
	key      crypto.Signer
	roots    *x509.CertPool
}

type ekuSpec struct {
	critical bool
	oids     []asn1.ObjectIdentifier
	present  bool
}

// newTestTSA builds a root CA and a TSA leaf whose EKU extension is assembled
// manually so tests can exercise non-critical / extra-EKU cases that
// x509.CreateCertificate would refuse to emit.
func newTestTSA(eku ekuSpec) (*testTSA, error) {
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	rootSerial := big.NewInt(1)
	rootTmpl := &x509.Certificate{
		SerialNumber:          rootSerial,
		Subject:               pkix.Name{CommonName: "Test TSA Root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTmpl, rootTmpl, &rootKey.PublicKey, rootKey)
	if err != nil {
		return nil, err
	}
	rootCert, err := x509.ParseCertificate(rootDER)
	if err != nil {
		return nil, err
	}

	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	leafTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "Test TSA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		BasicConstraintsValid: true,
		IsCA:                  false,
		KeyUsage:              x509.KeyUsageDigitalSignature,
	}
	if eku.present {
		ext, err := buildEKUExtension(eku.critical, eku.oids)
		if err != nil {
			return nil, err
		}
		leafTmpl.ExtraExtensions = append(leafTmpl.ExtraExtensions, ext)
	}

	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, rootCert, &leafKey.PublicKey, rootKey)
	if err != nil {
		return nil, err
	}
	leafCert, err := x509.ParseCertificate(leafDER)
	if err != nil {
		return nil, err
	}

	pool := x509.NewCertPool()
	pool.AddCert(rootCert)
	return &testTSA{
		rootCert: rootCert,
		rootKey:  rootKey,
		cert:     leafCert,
		key:      leafKey,
		roots:    pool,
	}, nil
}

func buildEKUExtension(critical bool, oids []asn1.ObjectIdentifier) (pkix.Extension, error) {
	value, err := asn1.Marshal(oids)
	if err != nil {
		return pkix.Extension{}, err
	}
	return pkix.Extension{
		Id:       oidExtKeyUsage,
		Critical: critical,
		Value:    value,
	}, nil
}

type tokenSpec struct {
	hash        crypto.Hash
	hashed      []byte
	nonce       *big.Int
	policy      asn1.ObjectIdentifier
	genTime     time.Time
	accuracy    accuracy
	omitAcc     bool
	omitNonce   bool
	sigOID      asn1.ObjectIdentifier
	digestOID   asn1.ObjectIdentifier
	includeCert bool
	cert        *x509.Certificate
	key         crypto.Signer
}

// buildToken assembles a minimal RFC 3161 TimeStampToken CMS object for tests.
func buildToken(spec tokenSpec) ([]byte, error) {
	info := tstInfo{
		Version: 1,
		Policy:  spec.policy,
		MessageImprint: messageImprint{
			HashAlgorithm: pkix.AlgorithmIdentifier{
				Algorithm: hashOIDs[spec.hash],
			},
			HashedMessage: spec.hashed,
		},
		SerialNumber: big.NewInt(42),
		GenTime:      spec.genTime,
	}
	if !spec.omitAcc {
		info.Accuracy = spec.accuracy
	}
	if !spec.omitNonce {
		info.Nonce = spec.nonce
	}
	eContent, err := asn1.Marshal(info)
	if err != nil {
		return nil, err
	}

	hasher := spec.hash.New()
	hasher.Write(eContent)
	contentDigest := hasher.Sum(nil)

	signedAttrs, err := buildSignedAttrs(contentDigest)
	if err != nil {
		return nil, err
	}
	signatureInput := asn1.RawValue{
		Class:      asn1.ClassUniversal,
		Tag:        asn1.TagSet,
		IsCompound: true,
		Bytes:      signedAttrs.Bytes,
	}
	inputDER, err := asn1.Marshal(signatureInput)
	if err != nil {
		return nil, err
	}

	sigAlg, signature, err := signToken(spec.key, spec.sigOID, spec.hash, inputDER)
	if err != nil {
		return nil, err
	}

	sid, err := buildSID(spec.cert)
	if err != nil {
		return nil, err
	}

	si := signerInfo{
		Version: 1,
		SID:     sid,
		DigestAlgorithm: pkix.AlgorithmIdentifier{
			Algorithm: spec.digestOID,
		},
		SignedAttrs:        signedAttrs,
		SignatureAlgorithm: sigAlg,
		Signature:          signature,
	}

	sd := signedData{
		Version:          3,
		DigestAlgorithms: []pkix.AlgorithmIdentifier{{Algorithm: spec.digestOID}},
		EncapContentInfo: encapContentInfo{
			EContentType: oidTSTInfo,
			EContent:     eContent,
		},
	}
	if spec.includeCert {
		sd.Certificates = wrapCertBag(spec.cert.Raw)
	}
	siSetDER, err := asn1.Marshal([]signerInfo{si})
	if err != nil {
		return nil, err
	}
	var siSet asn1.RawValue
	if _, err := asn1.Unmarshal(siSetDER, &siSet); err != nil {
		return nil, err
	}
	// Go encodes a slice as SEQUENCE OF; CMS requires the SET OF tag. Rewrite
	// the universal tag byte directly so the re-marshal keeps it.
	siSetDER[0] = 0x31
	if _, err := asn1.Unmarshal(siSetDER, &siSet); err != nil {
		return nil, err
	}
	sd.SignerInfos = siSet

	sdDER, err := asn1.Marshal(sd)
	if err != nil {
		return nil, err
	}
	ci := contentInfo{
		ContentType: oidSignedData,
		Content: asn1.RawValue{
			Class:      asn1.ClassContextSpecific,
			Tag:        0,
			IsCompound: true,
			Bytes:      sdDER,
		},
	}
	return asn1.Marshal(ci)
}

func buildSignedAttrs(contentDigest []byte) (asn1.RawValue, error) {
	ctAttr := signedAttribute{Type: oidContentType}
	ctBytes, err := asn1.Marshal(oidTSTInfo)
	if err != nil {
		return asn1.RawValue{}, err
	}
	ctAttr.Value = asn1.RawValue{
		Class:      asn1.ClassUniversal,
		Tag:        asn1.TagSet,
		IsCompound: true,
		Bytes:      ctBytes,
	}

	mdAttr := signedAttribute{Type: oidMessageDgst}
	mdBytes, err := asn1.Marshal(contentDigest)
	if err != nil {
		return asn1.RawValue{}, err
	}
	mdAttr.Value = asn1.RawValue{
		Class:      asn1.ClassUniversal,
		Tag:        asn1.TagSet,
		IsCompound: true,
		Bytes:      mdBytes,
	}

	// RFC 5652: signedAttrs is encoded SET OF then tagged IMPLICIT [0].
	setDER, err := asn1.Marshal([]signedAttribute{ctAttr, mdAttr})
	if err != nil {
		return asn1.RawValue{}, err
	}
	var set asn1.RawValue
	if _, err := asn1.Unmarshal(setDER, &set); err != nil {
		return asn1.RawValue{}, err
	}
	return asn1.RawValue{
		Class:      asn1.ClassContextSpecific,
		Tag:        0,
		IsCompound: true,
		Bytes:      set.Bytes,
	}, nil
}

func buildSID(cert *x509.Certificate) (asn1.RawValue, error) {
	serialDER, err := asn1.Marshal(cert.SerialNumber)
	if err != nil {
		return asn1.RawValue{}, err
	}
	// IssuerAndSerialNumber ::= SEQUENCE { issuer Name, serialNumber
	// CertificateSerialNumber }; concatenate the two DER TLVs by hand.
	body := append(append([]byte{}, cert.RawIssuer...), serialDER...)
	inner, err := asn1.Marshal(asn1.RawValue{
		Class:      asn1.ClassUniversal,
		Tag:        asn1.TagSequence,
		IsCompound: true,
		Bytes:      body,
	})
	if err != nil {
		return asn1.RawValue{}, err
	}
	var innerRaw asn1.RawValue
	if _, err := asn1.Unmarshal(inner, &innerRaw); err != nil {
		return asn1.RawValue{}, err
	}
	return asn1.RawValue{
		Class:      asn1.ClassContextSpecific,
		Tag:        0,
		IsCompound: true,
		Bytes:      body,
	}, nil
}

func wrapCertBag(certDER []byte) asn1.RawValue {
	var raw asn1.RawValue
	if _, err := asn1.Unmarshal(certDER, &raw); err != nil {
		panic(err)
	}
	return asn1.RawValue{
		Class:      asn1.ClassContextSpecific,
		Tag:        0,
		IsCompound: true,
		Bytes:      certDER,
	}
}

func signToken(key crypto.Signer, sigOID asn1.ObjectIdentifier, hash crypto.Hash, input []byte) (pkix.AlgorithmIdentifier, []byte, error) {
	switch sigOID.String() {
	case oidRSAEncryption.String(), oidECDSAWithSHA256.String():
		hasher := hash.New()
		hasher.Write(input)
		digest := hasher.Sum(nil)
		sig, err := key.Sign(rand.Reader, digest, hash)
		if err != nil {
			return pkix.AlgorithmIdentifier{}, nil, err
		}
		return pkix.AlgorithmIdentifier{Algorithm: sigOID}, sig, nil
	case oidEd25519.String():
		sig, err := key.Sign(rand.Reader, input, crypto.Hash(0))
		if err != nil {
			return pkix.AlgorithmIdentifier{}, nil, err
		}
		return pkix.AlgorithmIdentifier{Algorithm: sigOID}, sig, nil
	default:
		return pkix.AlgorithmIdentifier{}, nil, errors.New("unsupported test sig OID")
	}
}

var testPolicy = asn1.ObjectIdentifier{1, 2, 3, 4, 5}
var otherPolicy = asn1.ObjectIdentifier{1, 2, 3, 4, 6}

func defaultSpec(tsa *testTSA, hash crypto.Hash, digest []byte, nonce *big.Int, genTime time.Time) tokenSpec {
	return tokenSpec{
		hash:        hash,
		hashed:      digest,
		nonce:       nonce,
		policy:      testPolicy,
		genTime:     genTime,
		accuracy:    accuracy{Seconds: 1, Millis: 500},
		sigOID:      oidRSAEncryption,
		digestOID:   hashOIDs[hash],
		includeCert: true,
		cert:        tsa.cert,
		key:         tsa.key,
	}
}

// rebuildTokenWithImprint re-issues a structurally identical token using the
// same TSA key but with a different messageImprint digest, simulating a valid
// token stolen from another artifact (the CMS signature stays valid).
func rebuildTokenWithImprint(tsa *testTSA, tokenDER, newDigest []byte, hash crypto.Hash) ([]byte, error) {
	parsed, err := parseToken(tokenDER)
	if err != nil {
		return nil, err
	}
	sigOID := parsed.signerInfo.SignatureAlgorithm.Algorithm
	spec := tokenSpec{
		hash:        hash,
		hashed:      newDigest,
		nonce:       parsed.info.Nonce,
		policy:      parsed.info.Policy,
		genTime:     parsed.info.GenTime,
		accuracy:    parsed.info.Accuracy,
		omitAcc:     parsed.info.Accuracy == (accuracy{}),
		omitNonce:   parsed.info.Nonce == nil,
		sigOID:      sigOID,
		digestOID:   parsed.signerInfo.DigestAlgorithm.Algorithm,
		includeCert: true,
		cert:        parsed.signer,
		key:         tsa.key,
	}
	if !sigOID.Equal(oidRSAEncryption) {
		return nil, errors.New("unexpected signature OID in test token")
	}
	return buildToken(spec)
}
