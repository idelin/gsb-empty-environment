package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"
)

const (
	testNamespace = "org.iso.18013.5.1"
	testDocType   = "org.iso.18013.5.1.mDL"
)

type fixture struct {
	key     *ecdsa.PrivateKey
	certDER []byte
	roots   *x509.CertPool
	now     time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test Issuer"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots.AddCert(cert)
	return &fixture{key: key, certDER: der, roots: roots, now: now}
}

// makeItem builds an IssuerSignedItem and returns its generic form (for
// embedding in nameSpaces) and its full IssuerSignedItemBytes.
func makeItem(t *testing.T, digestID uint64, identifier string, value any) (any, []byte) {
	t.Helper()
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		t.Fatal(err)
	}
	inner, err := encodeCBOR(map[any]any{
		"digestID":          digestID,
		"random":            salt,
		"elementIdentifier": identifier,
		"elementValue":      value,
	})
	if err != nil {
		t.Fatal(err)
	}
	item := cborTag{num: 24, value: inner}
	itemBytes, err := encodeCBOR(item)
	if err != nil {
		t.Fatal(err)
	}
	return item, itemBytes
}

// buildMSO builds the MSO payload (MobileSecurityObjectBytes) covering the
// given item bytes in the test namespace.
func (f *fixture) buildMSO(t *testing.T, itemBytes ...[]byte) []byte {
	t.Helper()
	digests := map[any]any{}
	for i, b := range itemBytes {
		sum := sha256.Sum256(b)
		digests[uint64(i)] = sum[:]
	}
	tdate := func(tm time.Time) cborTag {
		return cborTag{num: 0, value: tm.UTC().Format(time.RFC3339)}
	}
	mso, err := encodeCBOR(map[any]any{
		"version":         "1.0",
		"digestAlgorithm": "SHA-256",
		"docType":         testDocType,
		"valueDigests":    map[any]any{testNamespace: digests},
		"validityInfo": map[any]any{
			"signed":     tdate(f.now),
			"validFrom":  tdate(f.now.Add(-time.Hour)),
			"validUntil": tdate(f.now.Add(time.Hour)),
		},
		"deviceKeyInfo": map[any]any{"deviceKey": map[any]any{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := encodeCBOR(cborTag{num: 24, value: mso})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

// sign builds the issuerAuth COSE_Sign1 structure over payload.
func (f *fixture) sign(t *testing.T, payload []byte) any {
	t.Helper()
	protected, err := encodeCBOR(map[any]any{coseHeaderAlg: coseAlgES256})
	if err != nil {
		t.Fatal(err)
	}
	sigStructure, err := encodeCBOR([]any{"Signature1", protected, []byte{}, payload})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(sigStructure)
	r, s, err := ecdsa.Sign(rand.Reader, f.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return cborTag{num: 18, value: []any{
		protected,
		map[any]any{coseHeaderX5Chain: []any{f.certDER}},
		payload,
		sig,
	}}
}

// buildResponse assembles a DeviceResponse from an issuerAuth structure and
// the disclosed items.
func buildResponse(t *testing.T, issuerAuth any, items ...any) []byte {
	t.Helper()
	nsInner, err := encodeCBOR(map[any]any{testNamespace: items})
	if err != nil {
		t.Fatal(err)
	}
	response, err := encodeCBOR(map[any]any{
		"version": "1.0",
		"documents": []any{
			map[any]any{
				"docType": testDocType,
				"issuerSigned": map[any]any{
					"nameSpaces": cborTag{num: 24, value: nsInner},
					"issuerAuth": issuerAuth,
				},
			},
		},
		"status": uint64(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func mustEncode(t *testing.T, v any) []byte {
	t.Helper()
	b, err := encodeCBOR(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestVerifyValidResponse(t *testing.T) {
	f := newFixture(t)
	itemA, bytesA := makeItem(t, 0, "family_name", "Doe")
	itemB, bytesB := makeItem(t, 1, "given_name", "John")
	payload := f.buildMSO(t, bytesA, bytesB)
	response := buildResponse(t, f.sign(t, payload), itemA, itemB)

	for name, opts := range map[string]*Options{
		"pinned issuer key": {IssuerKey: &f.key.PublicKey, Now: f.now},
		"x5chain and roots": {Roots: f.roots, Now: f.now},
	} {
		t.Run(name, func(t *testing.T) {
			elements, err := VerifyDeviceResponse(response, opts)
			if err != nil {
				t.Fatalf("VerifyDeviceResponse: %v", err)
			}
			if len(elements) != 2 {
				t.Fatalf("got %d elements, want 2", len(elements))
			}
			got := map[string]any{}
			for _, e := range elements {
				if e.Namespace != testNamespace {
					t.Errorf("unexpected namespace %q", e.Namespace)
				}
				got[e.Identifier] = e.Value
			}
			if got["family_name"] != "Doe" || got["given_name"] != "John" {
				t.Errorf("unexpected elements: %v", got)
			}
		})
	}
}

// The core attack: the attacker keeps the signed MSO untouched and replaces
// one disclosed value. The digest of the replacement no longer equals the
// MSO entry, so verification must fail even though the MSO signature is
// still valid.
func TestReplacedValueRejected(t *testing.T) {
	f := newFixture(t)
	itemA, bytesA := makeItem(t, 0, "family_name", "Doe")
	itemB, bytesB := makeItem(t, 1, "given_name", "John")
	payload := f.buildMSO(t, bytesA, bytesB)
	issuerAuth := f.sign(t, payload)

	if _, err := VerifyDeviceResponse(buildResponse(t, issuerAuth, itemA, itemB),
		&Options{Roots: f.roots, Now: f.now}); err != nil {
		t.Fatalf("sanity check failed: %v", err)
	}

	// Same MSO, same digestID, different disclosed bytes.
	forged, _ := makeItem(t, 1, "given_name", "Eve")
	_, err := VerifyDeviceResponse(buildResponse(t, issuerAuth, itemA, forged),
		&Options{Roots: f.roots, Now: f.now})
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("got %v, want ErrDigestMismatch", err)
	}
}

// The attacker replaces a value and also rewrites the MSO digest to match.
// The MSO payload no longer matches the signature, so verification must
// fail at the signature step.
func TestReplacedValueAndEditedMSORejected(t *testing.T) {
	f := newFixture(t)
	itemA, bytesA := makeItem(t, 0, "family_name", "Doe")
	_, bytesB := makeItem(t, 1, "given_name", "John")
	payload := f.buildMSO(t, bytesA, bytesB)
	issuerAuth := f.sign(t, payload) // signature over the original MSO

	forged, forgedBytes := makeItem(t, 1, "given_name", "Eve")
	forgedPayload := f.buildMSO(t, bytesA, forgedBytes) // digests match the forgery
	// Reuse the original signature with the edited payload.
	orig, err := parseCOSESign1(mustEncode(t, issuerAuth))
	if err != nil {
		t.Fatal(err)
	}
	editedAuth := cborTag{num: 18, value: []any{
		orig.protected,
		map[any]any{coseHeaderX5Chain: []any{f.certDER}},
		forgedPayload,
		orig.signature,
	}}
	_, err = VerifyDeviceResponse(buildResponse(t, editedAuth, itemA, forged),
		&Options{Roots: f.roots, Now: f.now})
	if !errors.Is(err, ErrMSOSignature) {
		t.Fatalf("got %v, want ErrMSOSignature", err)
	}
}

func TestUnknownDigestIDRejected(t *testing.T) {
	f := newFixture(t)
	itemA, bytesA := makeItem(t, 0, "family_name", "Doe")
	payload := f.buildMSO(t, bytesA)
	issuerAuth := f.sign(t, payload)

	// An extra disclosed item whose digestID is not in the MSO.
	extra, _ := makeItem(t, 7, "given_name", "John")
	_, err := VerifyDeviceResponse(buildResponse(t, issuerAuth, itemA, extra),
		&Options{Roots: f.roots, Now: f.now})
	if !errors.Is(err, ErrUnknownDigestID) {
		t.Fatalf("got %v, want ErrUnknownDigestID", err)
	}
}

func TestForgedSignatureRejected(t *testing.T) {
	f := newFixture(t)
	attacker := newFixture(t)
	itemA, bytesA := makeItem(t, 0, "family_name", "Doe")
	payload := f.buildMSO(t, bytesA)
	// Signed by the attacker, verified against the real issuer's roots.
	response := buildResponse(t, attacker.sign(t, payload), itemA)
	_, err := VerifyDeviceResponse(response, &Options{Roots: f.roots, Now: f.now})
	if err == nil {
		t.Fatal("expected verification to fail")
	}
	if errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("got %v, want a trust/signature failure", err)
	}
}

func TestExpiredMSORejected(t *testing.T) {
	f := newFixture(t)
	itemA, bytesA := makeItem(t, 0, "family_name", "Doe")
	payload := f.buildMSO(t, bytesA)
	response := buildResponse(t, f.sign(t, payload), itemA)
	_, err := VerifyDeviceResponse(response,
		&Options{Roots: f.roots, Now: f.now.Add(2 * time.Hour)})
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("got %v, want ErrExpired", err)
	}
}
