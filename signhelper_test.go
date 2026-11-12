package netsec

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"time"
)

type testChain struct {
	Root         *x509.Certificate
	Intermediate *x509.Certificate
	Leaf         *x509.Certificate
	rootKey      *rsa.PrivateKey
	interKey     *rsa.PrivateKey
	leafKey      *rsa.PrivateKey
}

func newTestChain(commonName string) (*testChain, error) {
	rootKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	interKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	now := time.Now().Add(-time.Hour)

	rootTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: commonName + " Root"},
		NotBefore:             now,
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTmpl, rootTmpl, &rootKey.PublicKey, rootKey)
	if err != nil {
		return nil, err
	}
	rootCert, err := x509.ParseCertificate(rootDER)
	if err != nil {
		return nil, err
	}

	interTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: commonName + " Intermediate"},
		NotBefore:             now,
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	interDER, err := x509.CreateCertificate(rand.Reader, interTmpl, rootTmpl, &interKey.PublicKey, rootKey)
	if err != nil {
		return nil, err
	}
	interCert, err := x509.ParseCertificate(interDER)
	if err != nil {
		return nil, err
	}

	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: commonName + " Signer"},
		NotBefore:    now,
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, interTmpl, &leafKey.PublicKey, interKey)
	if err != nil {
		return nil, err
	}
	leafCert, err := x509.ParseCertificate(leafDER)
	if err != nil {
		return nil, err
	}

	return &testChain{
		Root:         rootCert,
		Intermediate: interCert,
		Leaf:         leafCert,
		rootKey:      rootKey,
		interKey:     interKey,
		leafKey:      leafKey,
	}, nil
}

func (c *testChain) trustPool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(c.Root)
	return pool
}

func makeIntermediate(parent *testChain, commonName string) (*x509.Certificate, *rsa.PrivateKey, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now().Add(-time.Hour)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(100),
		Subject:               pkix.Name{CommonName: commonName + " Intermediate"},
		NotBefore:             now,
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent.Root, &key.PublicKey, parent.rootKey)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	return cert, key, err
}

func makeLeaf(inter *x509.Certificate, interKey *rsa.PrivateKey, publicKey *rsa.PublicKey, commonName string) (*x509.Certificate, error) {
	now := time.Now().Add(-time.Hour)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(101),
		Subject:      pkix.Name{CommonName: commonName + " Leaf"},
		NotBefore:    now,
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, inter, publicKey, interKey)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
}

type sigAlgo struct {
	digestOID asn1.ObjectIdentifier
	sigOID    asn1.ObjectIdentifier
	hash      crypto.Hash
}

var rsaSHA256Algo = sigAlgo{
	digestOID: oidDigestSHA256,
	sigOID:    oidSigRSASHA256,
	hash:      crypto.SHA256,
}

var rsaSHA1Algo = sigAlgo{
	digestOID: asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26},
	sigOID:    oidSigRSAEncryption,
	hash:      crypto.SHA1,
}

// signDetached builds a detached PKCS#7 SignedData blob over content using the
// chain leaf key and embeds the leaf, intermediate and root certificates.
func (c *testChain) signDetached(content []byte, algo sigAlgo) ([]byte, error) {
	digest := hashFor(content, algo.hash)

	contentTypeVal, err := asn1.Marshal(oidPKCS7Data)
	if err != nil {
		return nil, err
	}
	digestVal, err := asn1.Marshal(digest)
	if err != nil {
		return nil, err
	}

	attrs := []pkcs7Attribute{
		{Type: oidAttrContentType, Value: asn1.RawValue{Bytes: contentTypeVal}},
		{Type: oidAttrMessageDigest, Value: asn1.RawValue{Bytes: digestVal}},
	}

	// Re-encode the [0] IMPLICIT SET OF attributes as a universal SET so the
	// RSA signature is computed over exactly the bytes RFC 5652 mandates.
	signedAttrsDER, err := asn1.Marshal(attrs)
	if err != nil {
		return nil, err
	}
	signature, err := rsa.SignPKCS1v15(rand.Reader, c.leafKey, algo.hash, hashFor(signedAttrsDER, algo.hash))
	if err != nil {
		return nil, err
	}

	var issuerRaw asn1.RawValue
	if _, err := asn1.Unmarshal(c.Leaf.RawIssuer, &issuerRaw); err != nil {
		return nil, err
	}
	var serialRaw asn1.RawValue
	serialDER, err := asn1.Marshal(c.Leaf.SerialNumber)
	if err != nil {
		return nil, err
	}
	if _, err := asn1.Unmarshal(serialDER, &serialRaw); err != nil {
		return nil, err
	}

	signerInfo := testSignerInfo{
		Version: 1,
		IssuerAndSerialNumber: testIssuerAndSerial{
			Issuer:       issuerRaw,
			SerialNumber: serialRaw,
		},
		DigestAlgorithm:           algorithmIdentifier{Algorithm: algo.digestOID},
		AuthenticatedAttributes:   attrs,
		DigestEncryptionAlgorithm: algorithmIdentifier{Algorithm: algo.sigOID},
		EncryptedDigest:           signature,
	}

	certSet, err := marshalCertificateSet(c.Leaf, c.Intermediate, c.Root)
	if err != nil {
		return nil, err
	}

	signedData := testSignedData{
		Version:                 1,
		DigestAlgorithms:        []algorithmIdentifier{{Algorithm: algo.digestOID}},
		EncapsulatedContentInfo: pkcs7EncapsulatedContent{ContentType: oidPKCS7Data},
		Certificates:            certSet,
		SignerInfos:             []testSignerInfo{signerInfo},
	}

	sdDER, err := asn1.Marshal(signedData)
	if err != nil {
		return nil, err
	}
	ci := pkcs7ContentInfo{
		ContentType: oidPKCS7SignedData,
		Content: asn1.RawValue{
			Tag:        0,
			Class:      asn1.ClassContextSpecific,
			IsCompound: true,
			Bytes:      sdDER,
		},
	}
	return asn1.Marshal(ci)
}

func hashFor(data []byte, h crypto.Hash) []byte {
	hasher := h.New()
	hasher.Write(data)
	return hasher.Sum(nil)
}

func marshalCertificateSet(certs ...*x509.Certificate) (asn1.RawValue, error) {
	var inner bytes.Buffer
	for _, cert := range certs {
		if cert == nil {
			continue
		}
		inner.Write(cert.Raw)
	}
	return asn1.RawValue{FullBytes: encodeImplicit(0, inner.Bytes())}, nil
}

// encodeImplicit wraps inner in a context-specific constructed DER tag.
func encodeImplicit(tag int, inner []byte) []byte {
	length := len(inner)
	var header []byte
	switch {
	case length < 0x80:
		header = []byte{0xA0 | byte(tag), byte(length)}
	case length < 1<<8:
		header = []byte{0xA0 | byte(tag), 0x81, byte(length)}
	case length < 1<<16:
		header = []byte{0xA0 | byte(tag), 0x82, byte(length >> 8), byte(length)}
	default:
		header = []byte{
			0xA0 | byte(tag), 0x83,
			byte(length >> 16), byte(length >> 8), byte(length),
		}
	}
	return append(header, inner...)
}

type testSignedData struct {
	Version                 int
	DigestAlgorithms        []algorithmIdentifier
	EncapsulatedContentInfo pkcs7EncapsulatedContent
	Certificates            asn1.RawValue `asn1:"tag:0,optional"`
	CRLs                    asn1.RawValue `asn1:"tag:1,optional"`
	SignerInfos             []testSignerInfo
}

type testSignerInfo struct {
	Version                   int
	IssuerAndSerialNumber     testIssuerAndSerial
	DigestAlgorithm           algorithmIdentifier
	AuthenticatedAttributes   []pkcs7Attribute `asn1:"tag:0,optional"`
	DigestEncryptionAlgorithm algorithmIdentifier
	EncryptedDigest           []byte
	UnauthenticatedAttributes []pkcs7Attribute `asn1:"tag:1,optional"`
}

type testIssuerAndSerial struct {
	Issuer       asn1.RawValue
	SerialNumber asn1.RawValue
}
