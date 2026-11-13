package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"math/big"
	"testing"
	"time"
)

func makeCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test IACA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

func issueReaderCert(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, pub *ecdsa.PublicKey, withReaderUsage bool) *x509.Certificate {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test reader"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	if withReaderUsage {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageAny}
		tmpl.UnknownExtKeyUsage = []asn1.ObjectIdentifier{OIDReaderAuthEKU}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, pub, caKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

// coseKey encodes an EC2 COSE_Key for use as a session key in the transcript.
func coseKey(t *testing.T, pub *ecdsa.PublicKey) []byte {
	t.Helper()
	w := &cborWriter{}
	w.mapHead(5)
	w.int64Val(1)
	w.int64Val(2) // kty: EC2
	w.int64Val(3)
	w.int64Val(-7) // alg: ES256
	w.int64Val(-1)
	w.int64Val(1) // crv: P-256
	w.int64Val(-2)
	w.bytes(pub.X.Bytes())
	w.int64Val(-3)
	w.bytes(pub.Y.Bytes())
	return w.buf
}

type sessionFixture struct {
	transcript   SessionTranscript
	itemsRequest []byte
}

func newSession(t *testing.T) sessionFixture {
	t.Helper()
	deviceKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	readerKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	engagement := &cborWriter{}
	engagement.mapHead(2)
	engagement.text("1.0")
	engagement.text("version")
	engagement.int64Val(0)
	engagement.bytes(coseKey(t, &deviceKey.PublicKey))

	handover := &cborWriter{}
	handover.array(2)
	handover.bytes([]byte("nfc-handover-select"))
	handover.bytes([]byte("nfc-handover-request"))

	items := &cborWriter{}
	items.mapHead(1)
	items.text("docType")
	items.text("org.iso.18013.5.1.mDL")

	return sessionFixture{
		transcript: SessionTranscript{
			DeviceEngagement: engagement.buf,
			ReaderKey:        coseKey(t, &readerKey.PublicKey),
			Handover:         handover.buf,
		},
		itemsRequest: items.buf,
	}
}

func TestVerifyReaderAuthAcceptsMatchingUsageAndTranscript(t *testing.T) {
	ca, caKey := makeCA(t)
	readerKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := issueReaderCert(t, ca, caKey, &readerKey.PublicKey, true)
	fx := newSession(t)

	readerAuth, err := SignReaderAuth(readerKey, fx.transcript, fx.itemsRequest, nil)
	if err != nil {
		t.Fatal(err)
	}

	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if err := VerifyReaderAuth(cert, readerAuth, fx.transcript, fx.itemsRequest, &VerifyOptions{Roots: roots}); err != nil {
		t.Fatalf("expected reader authentication to be accepted, got %v", err)
	}
}

func TestVerifyReaderAuthRejectsCertWithoutReaderUsage(t *testing.T) {
	ca, caKey := makeCA(t)
	readerKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	fx := newSession(t)
	readerAuth, err := SignReaderAuth(readerKey, fx.transcript, fx.itemsRequest, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Swap in a certificate for the same key but without the reader usage.
	cert := issueReaderCert(t, ca, caKey, &readerKey.PublicKey, false)
	err = VerifyReaderAuth(cert, readerAuth, fx.transcript, fx.itemsRequest, nil)
	if !errors.Is(err, ErrReaderUsageMissing) {
		t.Fatalf("expected ErrReaderUsageMissing, got %v", err)
	}
}

func TestVerifyReaderAuthRejectsTamperedHandover(t *testing.T) {
	ca, caKey := makeCA(t)
	readerKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := issueReaderCert(t, ca, caKey, &readerKey.PublicKey, true)
	fx := newSession(t)

	readerAuth, err := SignReaderAuth(readerKey, fx.transcript, fx.itemsRequest, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Flip one byte of the handover inside the session transcript.
	tampered := fx.transcript
	tampered.Handover = append([]byte(nil), fx.transcript.Handover...)
	tampered.Handover[len(tampered.Handover)-1] ^= 0x01

	err = VerifyReaderAuth(cert, readerAuth, tampered, fx.itemsRequest, nil)
	if !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("expected ErrSignatureMismatch, got %v", err)
	}
}

func TestVerifyReaderAuthRejectsIncompleteTranscript(t *testing.T) {
	ca, caKey := makeCA(t)
	readerKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := issueReaderCert(t, ca, caKey, &readerKey.PublicKey, true)
	fx := newSession(t)

	readerAuth, err := SignReaderAuth(readerKey, fx.transcript, fx.itemsRequest, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []SessionTranscript{
		{DeviceEngagement: nil, ReaderKey: fx.transcript.ReaderKey, Handover: fx.transcript.Handover},
		{DeviceEngagement: fx.transcript.DeviceEngagement, ReaderKey: nil, Handover: fx.transcript.Handover},
		{DeviceEngagement: fx.transcript.DeviceEngagement, ReaderKey: fx.transcript.ReaderKey, Handover: nil},
	} {
		if err := VerifyReaderAuth(cert, readerAuth, tc, fx.itemsRequest, nil); !errors.Is(err, ErrTranscriptIncomplete) {
			t.Fatalf("expected ErrTranscriptIncomplete, got %v", err)
		}
	}
}
