package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"math/big"
	"testing"
	"time"
)

type testAttribute struct {
	oid   asn1.ObjectIdentifier
	value []byte
}

type testFixture struct {
	der         []byte
	cert        *x509.Certificate
	content     []byte
	contentType asn1.ObjectIdentifier
}

type fixtureOptions struct {
	contentType     asn1.ObjectIdentifier
	content         []byte
	attrs           []testAttribute
	signImplicit    bool
	omitSignedAttrs bool
}

func makeFixture(t *testing.T, options fixtureOptions) testFixture {
	t.Helper()

	if options.contentType == nil {
		options.contentType = oidData
	}
	if options.content == nil {
		options.content = []byte("archive payload")
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(0x143),
		Subject:               pkix.Name{CommonName: "archive signer"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		SubjectKeyId:          []byte{0x01, 0x43},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		t.Fatal(err)
	}

	if options.attrs == nil && !options.omitSignedAttrs {
		options.attrs = []testAttribute{
			{oidContentType, mustMarshalOID(t, options.contentType)},
			{oidMessageDigest, encodeDEROctetString(digestSHA256(options.content))},
		}
	}

	var attrDER [][]byte
	for _, attr := range options.attrs {
		attrDER = append(attrDER, encodeDERSequence(
			mustMarshalOID(t, attr.oid),
			encodeDERSet(attr.value),
		))
	}
	sortDER(attrDER)

	var attrContent []byte
	for _, encoded := range attrDER {
		attrContent = append(attrContent, encoded...)
	}

	signedInputTag := derClassUniversal
	signedInputTagNumber := derTagSet
	if options.signImplicit {
		signedInputTag = derClassContextSpecific
		signedInputTagNumber = 0
	}
	signedInput := encodeDER(signedInputTag, signedInputTagNumber, true, attrContent)
	if options.omitSignedAttrs {
		signedInput = options.content
	}
	signature := ed25519.Sign(privateKey, signedInput)

	sha256Algorithm := encodeDERSequence(
		mustMarshalOID(t, oidSHA256),
		encodeDERNull(),
	)
	ed25519Algorithm := encodeDERSequence(mustMarshalOID(t, oidEd25519))
	serialDER, err := asn1.Marshal(cert.SerialNumber)
	if err != nil {
		t.Fatal(err)
	}
	signerFields := [][]byte{
		encodeDERInteger(1),
		encodeDERSequence(cert.RawIssuer, serialDER),
		sha256Algorithm,
	}
	if !options.omitSignedAttrs {
		signerFields = append(signerFields, encodeDER(derClassContextSpecific, 0, true, attrContent))
	}
	signerFields = append(signerFields, ed25519Algorithm, encodeDEROctetString(signature))
	signerInfo := encodeDERSequence(signerFields...)

	encapContent := encodeDERSequence(
		mustMarshalOID(t, options.contentType),
		encodeDER(derClassContextSpecific, 0, true, encodeDEROctetString(options.content)),
	)
	signedData := encodeDERSequence(
		encodeDERInteger(1),
		encodeDERSet(sha256Algorithm),
		encapContent,
		encodeDER(derClassContextSpecific, 0, true, cert.Raw),
		encodeDERSet(signerInfo),
	)
	contentInfo := encodeDERSequence(
		mustMarshalOID(t, oidSignedData),
		encodeDER(derClassContextSpecific, 0, true, signedData),
	)

	return testFixture{
		der:         contentInfo,
		cert:        cert,
		content:     options.content,
		contentType: options.contentType,
	}
}

func digestSHA256(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}

func mustMarshalOID(t *testing.T, oid asn1.ObjectIdentifier) []byte {
	t.Helper()
	der, err := asn1.Marshal(oid)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func sortDER(values [][]byte) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && string(values[j]) < string(values[j-1]); j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func validAttributes(content []byte, contentType asn1.ObjectIdentifier) []testAttribute {
	return []testAttribute{
		{oidContentType, mustMarshalOIDNoTest(contentType)},
		{oidMessageDigest, encodeDEROctetString(digestSHA256(content))},
	}
}

func mustMarshalOIDNoTest(oid asn1.ObjectIdentifier) []byte {
	der, err := asn1.Marshal(oid)
	if err != nil {
		panic(err)
	}
	return der
}

func TestVerifySignedDataValid(t *testing.T) {
	content := []byte("archive payload")
	fixture := makeFixture(t, fixtureOptions{
		content: content,
		attrs:   validAttributes(content, oidData),
	})

	if err := VerifySignedData(fixture.der, content, WithTrustedCertificates(fixture.cert)); err != nil {
		t.Fatalf("valid SignedData rejected: %v", err)
	}

	roots := x509.NewCertPool()
	roots.AddCert(fixture.cert)
	if err := VerifySignedData(fixture.der, roots); err != nil {
		t.Fatalf("valid embedded SignedData rejected: %v", err)
	}
}

func TestVerifySignedDataAcceptsPEM(t *testing.T) {
	content := []byte("pem payload")
	fixture := makeFixture(t, fixtureOptions{
		content: content,
		attrs:   validAttributes(content, oidData),
	})
	encoded := pem.EncodeToMemory(&pem.Block{Type: "PKCS7", Bytes: fixture.der})

	if err := VerifySignedData(encoded, WithTrustedCertificates(fixture.cert)); err != nil {
		t.Fatalf("valid PEM SignedData rejected: %v", err)
	}
}

func TestVerifySignedDataRejectsMissingMessageDigest(t *testing.T) {
	content := []byte("archive payload")
	fixture := makeFixture(t, fixtureOptions{
		content: content,
		attrs: []testAttribute{
			{oidContentType, mustMarshalOIDNoTest(oidData)},
		},
	})

	err := VerifySignedData(fixture.der, WithTrustedCertificates(fixture.cert))
	if !errors.Is(err, ErrMissingMessageDigest) {
		t.Fatalf("got %v, want ErrMissingMessageDigest", err)
	}
}

func TestVerifySignedDataRejectsMissingContentType(t *testing.T) {
	content := []byte("archive payload")
	fixture := makeFixture(t, fixtureOptions{
		content: content,
		attrs: []testAttribute{
			{oidMessageDigest, encodeDEROctetString(digestSHA256(content))},
		},
	})

	err := VerifySignedData(fixture.der, WithTrustedCertificates(fixture.cert))
	if !errors.Is(err, ErrMissingContentType) {
		t.Fatalf("got %v, want ErrMissingContentType", err)
	}
}

func TestVerifySignedDataRejectsDuplicateMessageDigest(t *testing.T) {
	content := []byte("archive payload")
	digest := testAttribute{oidMessageDigest, encodeDEROctetString(digestSHA256(content))}
	fixture := makeFixture(t, fixtureOptions{
		content: content,
		attrs: []testAttribute{
			{oidContentType, mustMarshalOIDNoTest(oidData)},
			digest,
			digest,
		},
	})

	err := VerifySignedData(fixture.der, WithTrustedCertificates(fixture.cert))
	if !errors.Is(err, ErrDuplicateAttribute) {
		t.Fatalf("got %v, want ErrDuplicateAttribute", err)
	}
}

func TestVerifySignedDataRejectsDuplicateContentType(t *testing.T) {
	content := []byte("archive payload")
	contentType := testAttribute{oidContentType, mustMarshalOIDNoTest(oidData)}
	fixture := makeFixture(t, fixtureOptions{
		content: content,
		attrs: []testAttribute{
			contentType,
			contentType,
			{oidMessageDigest, encodeDEROctetString(digestSHA256(content))},
		},
	})

	err := VerifySignedData(fixture.der, WithTrustedCertificates(fixture.cert))
	if !errors.Is(err, ErrDuplicateAttribute) {
		t.Fatalf("got %v, want ErrDuplicateAttribute", err)
	}
}

func TestVerifySignedDataRejectsModifiedDigest(t *testing.T) {
	content := []byte("archive payload")
	fixture := makeFixture(t, fixtureOptions{
		content: content,
		attrs: []testAttribute{
			{oidContentType, mustMarshalOIDNoTest(oidData)},
			{oidMessageDigest, encodeDEROctetString(digestSHA256([]byte("different content")))},
		},
	})

	err := VerifySignedData(fixture.der, WithTrustedCertificates(fixture.cert))
	if !errors.Is(err, ErrMessageDigestMismatch) {
		t.Fatalf("got %v, want ErrMessageDigestMismatch", err)
	}
}

func TestVerifySignedDataRejectsAppendedContent(t *testing.T) {
	content := []byte("archive payload")
	fixture := makeFixture(t, fixtureOptions{
		content: content,
		attrs:   validAttributes(content, oidData),
	})
	tampered := append(append([]byte{}, content...), []byte(" unsigned suffix")...)

	err := VerifySignedData(fixture.der, tampered, WithTrustedCertificates(fixture.cert))
	if !errors.Is(err, ErrContentMismatch) {
		t.Fatalf("got %v, want ErrContentMismatch", err)
	}
}

func TestVerifySignedDataRejectsContentTypeMismatch(t *testing.T) {
	content := []byte("archive payload")
	fixture := makeFixture(t, fixtureOptions{
		content:     content,
		contentType: oidSignedData,
		attrs:       validAttributes(content, oidData),
	})

	err := VerifySignedData(fixture.der, WithTrustedCertificates(fixture.cert))
	if !errors.Is(err, ErrContentTypeMismatch) {
		t.Fatalf("got %v, want ErrContentTypeMismatch", err)
	}
}

func TestVerifySignedDataSignsOverSetTag(t *testing.T) {
	content := []byte("archive payload")
	fixture := makeFixture(t, fixtureOptions{
		content:      content,
		attrs:        validAttributes(content, oidData),
		signImplicit: true,
	})

	err := VerifySignedData(fixture.der, WithTrustedCertificates(fixture.cert))
	if err == nil {
		t.Fatal("accepted signature calculated over the implicit A0 tag")
	}
}

func TestVerifySignedDataWithoutSignedAttributes(t *testing.T) {
	content := []byte("no signed attrs")
	fixture := makeFixture(t, fixtureOptions{
		content:         content,
		omitSignedAttrs: true,
	})

	if err := VerifySignedData(fixture.der, WithTrustedCertificates(fixture.cert)); err != nil {
		t.Fatalf("RFC 5652 no-signedAttrs form rejected by default: %v", err)
	}

	err := VerifySignedData(fixture.der, WithTrustedCertificates(fixture.cert), RequireSignedAttributes())
	if !errors.Is(err, ErrSignedAttributesRequired) {
		t.Fatalf("got %v, want ErrSignedAttributesRequired", err)
	}
}
