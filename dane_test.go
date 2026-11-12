package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"math/big"
	"testing"
	"time"
)

func testLeafAndRoot(t *testing.T) (*x509.Certificate, *x509.Certificate) {
	t.Helper()
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rootTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "DANE test root"},
		NotBefore:             time.Now().Add(-48 * time.Hour),
		NotAfter:              time.Now().Add(48 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "wrong name"},
		DNSNames:     []string{"wrong.example"},
		NotBefore:    time.Now().Add(-48 * time.Hour),
		NotAfter:     time.Now().Add(-24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, root, &leafKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(leafDER)
	if err != nil {
		t.Fatal(err)
	}
	return leaf, root
}

func sha256Bytes(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}

func sha512Bytes(data []byte) []byte {
	sum := sha512.Sum512(data)
	return sum[:]
}

func TestDANEEESelectorAndMatchingType(t *testing.T) {
	leaf, _ := testLeafAndRoot(t)
	if !time.Now().After(leaf.NotAfter) {
		t.Fatal("test leaf unexpectedly valid")
	}
	if err := leaf.VerifyHostname("mail.example"); err == nil {
		t.Fatal("test leaf unexpectedly matches hostname")
	}

	tests := []struct {
		name        string
		selector    uint8
		matching    uint8
		content     []byte
		association []byte
	}{
		{"full/raw", SelectorFullCert, MatchingTypeFull, leaf.Raw, leaf.Raw},
		{"spki/raw", SelectorSPKI, MatchingTypeFull, leaf.RawSubjectPublicKeyInfo, leaf.RawSubjectPublicKeyInfo},
		{"full/sha256", SelectorFullCert, MatchingTypeSHA256, leaf.Raw, sha256Bytes(leaf.Raw)},
		{"spki/sha256", SelectorSPKI, MatchingTypeSHA256, leaf.RawSubjectPublicKeyInfo, sha256Bytes(leaf.RawSubjectPublicKeyInfo)},
		{"full/sha512", SelectorFullCert, MatchingTypeSHA512, leaf.Raw, sha512Bytes(leaf.Raw)},
		{"spki/sha512", SelectorSPKI, MatchingTypeSHA512, leaf.RawSubjectPublicKeyInfo, sha512Bytes(leaf.RawSubjectPublicKeyInfo)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := NewTLSA(UsageDANEEE, test.selector, test.matching, test.association)
			if err := VerifyDANEEE(leaf, record); err != nil {
				t.Fatalf("VerifyDANEEE: %v", err)
			}
			if err := VerifyConnection(&tls.ConnectionState{
				PeerCertificates: []*x509.Certificate{leaf},
			}, record); err != nil {
				t.Fatalf("VerifyConnection: %v", err)
			}
			if got := string(test.content); got == "" {
				t.Fatal("empty selected content")
			}
		})
	}
}

func TestDANEEECrossSelectorSameBytes(t *testing.T) {
	leaf, _ := testLeafAndRoot(t)
	tests := []struct {
		name     string
		selector uint8
		data     []byte
	}{
		{"full bytes under SPKI selector", SelectorSPKI, leaf.Raw},
		{"SPKI bytes under full selector", SelectorFullCert, leaf.RawSubjectPublicKeyInfo},
		{"full SHA-256 under SPKI selector", SelectorSPKI, sha256Bytes(leaf.Raw)},
		{"SPKI SHA-256 under full selector", SelectorFullCert, sha256Bytes(leaf.RawSubjectPublicKeyInfo)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := VerifyDANEEE(leaf, NewTLSA(UsageDANEEE, test.selector, MatchingTypeFull, test.data))
			if !errors.Is(err, ErrNoMatch) {
				t.Fatalf("got %v, want ErrNoMatch", err)
			}
		})
	}
}

func TestDANEEECrossMatchingTypeSameBytes(t *testing.T) {
	leaf, _ := testLeafAndRoot(t)
	data := leaf.Raw
	digest256 := sha256Bytes(data)
	digest512 := sha512Bytes(data)

	tests := []struct {
		name     string
		matching uint8
		data     []byte
	}{
		{"raw cert bytes as SHA-256", MatchingTypeSHA256, data},
		{"SHA-256 digest as SHA-512", MatchingTypeSHA512, digest256},
		{"SHA-512 digest as SHA-256", MatchingTypeSHA256, digest512},
		{"SHA-256 digest as raw", MatchingTypeFull, digest256},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := VerifyDANEEE(leaf, NewTLSA(UsageDANEEE, SelectorFullCert, test.matching, test.data))
			if !errors.Is(err, ErrNoMatch) {
				t.Fatalf("got %v, want ErrNoMatch", err)
			}
		})
	}
}

func TestDANETARequiresMatchingChainCertificate(t *testing.T) {
	leaf, root := testLeafAndRoot(t)
	chain := []*x509.Certificate{leaf, root}

	rootRecord := NewTLSA(UsageDANETA, SelectorFullCert, MatchingTypeFull, root.Raw)
	if err := VerifyDANETA(chain, rootRecord); err != nil {
		t.Fatalf("DANE-TA root match: %v", err)
	}
	if err := VerifyConnection(&tls.ConnectionState{
		PeerCertificates: chain,
	}, rootRecord); err != nil {
		t.Fatalf("DANE-TA peer chain root match: %v", err)
	}
	if err := VerifyConnection(&tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{leaf},
		VerifiedChains:   [][]*x509.Certificate{chain},
	}, rootRecord); err != nil {
		t.Fatalf("DANE-TA verified chain root match: %v", err)
	}

	leafRecord := NewTLSA(UsageDANETA, SelectorFullCert, MatchingTypeFull, leaf.Raw)
	if err := VerifyDANETA(chain, leafRecord); err != nil {
		t.Fatalf("DANE-TA should accept any matching chain certificate: %v", err)
	}

	unrelated, unrelatedRoot := testLeafAndRoot(t)
	_ = unrelated
	unrelatedRecord := NewTLSA(UsageDANETA, SelectorFullCert, MatchingTypeFull, unrelatedRoot.Raw)
	if err := VerifyDANETA(chain, unrelatedRecord); !errors.Is(err, ErrNoMatch) {
		t.Fatalf("got %v, want ErrNoMatch for unrelated certificate", err)
	}
	if err := VerifyDANETA([]*x509.Certificate{leaf, unrelatedRoot}, unrelatedRecord); !errors.Is(err, ErrNoMatch) {
		t.Fatalf("got %v, want ErrNoMatch for an unrelated cert appended to the chain", err)
	}

	if err := VerifyDANETA(chain, NewTLSA(UsageDANEEE, SelectorFullCert, MatchingTypeFull, root.Raw)); !errors.Is(err, ErrUnsupportedUsage) {
		t.Fatalf("got %v, want ErrUnsupportedUsage", err)
	}
}

func TestDANEEEDoesNotUseChainMatch(t *testing.T) {
	leaf, root := testLeafAndRoot(t)
	chain := []*x509.Certificate{leaf, root}
	rootRecord := NewTLSA(UsageDANEEE, SelectorFullCert, MatchingTypeFull, root.Raw)
	if err := VerifyDANEEE(chain, rootRecord); !errors.Is(err, ErrNoMatch) {
		t.Fatalf("got %v, want ErrNoMatch for root-only DANE-EE", err)
	}
}

func TestDANETAEmptyChain(t *testing.T) {
	record := NewTLSA(UsageDANETA, SelectorFullCert, MatchingTypeFull, []byte{1, 2, 3})
	if err := VerifyDANETA([]*x509.Certificate{}, record); !errors.Is(err, ErrNoChain) {
		t.Fatalf("got %v, want ErrNoChain", err)
	}
}

func TestUnsupportedFields(t *testing.T) {
	leaf, _ := testLeafAndRoot(t)
	if err := VerifyDANEEE(leaf, NewTLSA(UsageDANEEE, 2, MatchingTypeFull, leaf.Raw)); !errors.Is(err, ErrUnsupportedSelector) {
		t.Fatalf("unsupported selector: got %v", err)
	}
	if err := VerifyDANEEE(leaf, NewTLSA(UsageDANEEE, SelectorFullCert, 3, leaf.Raw)); !errors.Is(err, ErrUnsupportedMatchType) {
		t.Fatalf("unsupported matching type: got %v", err)
	}
	if err := Verify(leaf, NewTLSA(UsagePKIXEE, SelectorFullCert, MatchingTypeFull, leaf.Raw)); !errors.Is(err, ErrUnsupportedUsage) {
		t.Fatalf("unsupported usage: got %v", err)
	}
}

func TestParseAndLooseArguments(t *testing.T) {
	leaf, _ := testLeafAndRoot(t)
	digest := sha256Bytes(leaf.RawSubjectPublicKeyInfo)
	record, err := ParseTLSA(UsageDANEEE, SelectorSPKI, MatchingTypeSHA256, hex.EncodeToString(digest))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyDANEEE(leaf, record); err != nil {
		t.Fatalf("parsed record: %v", err)
	}
	if err := VerifyDANEEE(SelectorSPKI, MatchingTypeSHA256, digest, leaf); err != nil {
		t.Fatalf("loose arguments: %v", err)
	}
	if err := Verify(leaf, UsageDANEEE, SelectorSPKI, MatchingTypeSHA256, digest); err != nil {
		t.Fatalf("generic loose arguments: %v", err)
	}
	if err := Verify(leaf, []TLSA{record}); err != nil {
		t.Fatalf("record slice: %v", err)
	}
}

func TestCertificateFieldAliases(t *testing.T) {
	leaf, _ := testLeafAndRoot(t)
	record := TLSA{
		Usage:        UsageDANEEE,
		Selector:     SelectorFullCert,
		MatchingType: MatchingTypeFull,
		Certificate:  leaf.Raw,
	}
	if err := VerifyDANEEE(leaf, record); err != nil {
		t.Fatalf("Certificate alias: %v", err)
	}

	stringRecord := TLSA{
		Usage:        UsageDANEEE,
		Selector:     SelectorSPKI,
		MatchingType: MatchingTypeSHA256,
		Certificate:  hex.EncodeToString(sha256Bytes(leaf.RawSubjectPublicKeyInfo)),
	}
	if err := VerifyDANEEE(leaf, stringRecord); err != nil {
		t.Fatalf("hex string Certificate alias: %v", err)
	}
}
