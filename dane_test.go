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
	"errors"
	"math/big"
	"testing"
	"time"
)

type testPKI struct {
	rootA       *x509.Certificate
	inter       *x509.Certificate
	leaf        *x509.Certificate
	rootB       *x509.Certificate
	leafB       *x509.Certificate
	expiredLeaf *x509.Certificate
	selfLeaf    *x509.Certificate

	chainA        []*x509.Certificate
	chainB        []*x509.Certificate
	chainBWithA   []*x509.Certificate
	expiredChain  []*x509.Certificate
	selfChain     []*x509.Certificate
	leafOnlyChain []*x509.Certificate
}

func generateCert(t *testing.T, template, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("generate serial: %v", err)
	}
	template.SerialNumber = serial

	signer := key
	if parentKey != nil {
		signer = parentKey
	} else {
		parent = template
	}

	der, err := x509.CreateCertificate(rand.Reader, template, parent, key.Public(), signer)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return cert, key
}

func caTemplate(commonName string) *x509.Certificate {
	return &x509.Certificate{
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * 365 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
}

func leafTemplate(commonName string, notAfter time.Time) *x509.Certificate {
	return &x509.Certificate{
		Subject:        pkix.Name{CommonName: commonName},
		DNSNames:       []string{commonName},
		NotBefore:      time.Now().Add(-time.Hour),
		NotAfter:       notAfter,
		KeyUsage:       x509.KeyUsageDigitalSignature,
		ExtKeyUsage:    []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		EmailAddresses: []string{"postmaster@" + commonName},
	}
}

func buildPKI(t *testing.T) testPKI {
	t.Helper()

	rootA, rootAKey := generateCert(t, caTemplate("Root A"), nil, nil)
	inter, interKey := generateCert(t, caTemplate("Intermediate A"), rootA, rootAKey)
	leaf, _ := generateCert(t, leafTemplate("mail.example.com", time.Now().Add(24*time.Hour)), inter, interKey)

	rootB, rootBKey := generateCert(t, caTemplate("Root B"), nil, nil)
	leafB, _ := generateCert(t, leafTemplate("mail.attacker.example", time.Now().Add(24*time.Hour)), rootB, rootBKey)

	expiredLeaf, _ := generateCert(t, leafTemplate("expired.example.com", time.Now().Add(-2*time.Hour)), inter, interKey)
	selfLeaf, _ := generateCert(t, leafTemplate("self.example.com", time.Now().Add(24*time.Hour)), nil, nil)

	return testPKI{
		rootA:       rootA,
		inter:       inter,
		leaf:        leaf,
		rootB:       rootB,
		leafB:       leafB,
		expiredLeaf: expiredLeaf,
		selfLeaf:    selfLeaf,

		chainA:        []*x509.Certificate{leaf, inter, rootA},
		chainB:        []*x509.Certificate{leafB, rootB},
		chainBWithA:   []*x509.Certificate{leafB, rootB, rootA},
		expiredChain:  []*x509.Certificate{expiredLeaf, inter, rootA},
		selfChain:     []*x509.Certificate{selfLeaf},
		leafOnlyChain: []*x509.Certificate{leaf},
	}
}

func record(usage TLSAUsage, selector TLSASelector, matching TLSAMatching, data []byte) TLSARecord {
	return TLSARecord{Usage: usage, Selector: selector, MatchingType: matching, Data: data}
}

func allRecordsFor(usage TLSAUsage, cert *x509.Certificate) []TLSARecord {
	certSHA256 := sha256.Sum256(cert.Raw)
	certSHA512 := sha512.Sum512(cert.Raw)
	spkiSHA256 := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	spkiSHA512 := sha512.Sum512(cert.RawSubjectPublicKeyInfo)

	return []TLSARecord{
		record(usage, SelectorCert, MatchingFull, cert.Raw),
		record(usage, SelectorCert, MatchingSHA256, certSHA256[:]),
		record(usage, SelectorCert, MatchingSHA512, certSHA512[:]),
		record(usage, SelectorSPKI, MatchingFull, cert.RawSubjectPublicKeyInfo),
		record(usage, SelectorSPKI, MatchingSHA256, spkiSHA256[:]),
		record(usage, SelectorSPKI, MatchingSHA512, spkiSHA512[:]),
	}
}

// A selector must fully determine which certificate bytes are selected. The
// same association bytes must not match under a different selector, and a
// digest selector must never collapse into a raw byte comparison.
func TestMatchCertificateSelectorStrictness(t *testing.T) {
	pki := buildPKI(t)
	cert := pki.leaf

	certSHA256 := sha256.Sum256(cert.Raw)
	spkiSHA256 := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	spkiSHA512 := sha512.Sum512(cert.RawSubjectPublicKeyInfo)

	tests := []struct {
		name   string
		record TLSARecord
		want   bool
	}{
		{
			name:   "full cert selector with full-cert bytes matches",
			record: record(UsageDANEEE, SelectorCert, MatchingFull, cert.Raw),
			want:   true,
		},
		{
			name:   "SPKI selector fed full-cert bytes must not match",
			record: record(UsageDANEEE, SelectorSPKI, MatchingFull, cert.Raw),
			want:   false,
		},
		{
			name:   "full cert selector fed SPKI bytes must not match",
			record: record(UsageDANEEE, SelectorCert, MatchingFull, cert.RawSubjectPublicKeyInfo),
			want:   false,
		},
		{
			name:   "SPKI selector fed full-cert SHA-256 must not match",
			record: record(UsageDANEEE, SelectorSPKI, MatchingSHA256, certSHA256[:]),
			want:   false,
		},
		{
			name:   "cert selector fed SPKI SHA-256 must not match",
			record: record(UsageDANEEE, SelectorCert, MatchingSHA256, spkiSHA256[:]),
			want:   false,
		},
		{
			name:   "SPKI SHA-256 matches with correct selector",
			record: record(UsageDANEEE, SelectorSPKI, MatchingSHA256, spkiSHA256[:]),
			want:   true,
		},
		{
			name:   "SPKI SHA-512 matches with correct selector",
			record: record(UsageDANEEE, SelectorSPKI, MatchingSHA512, spkiSHA512[:]),
			want:   true,
		},
		{
			name:   "unknown matching type is rejected",
			record: record(UsageDANEEE, SelectorCert, TLSAMatching(3), cert.Raw),
			want:   false,
		},
		{
			name:   "unknown selector is rejected",
			record: record(UsageDANEEE, TLSASelector(2), MatchingFull, cert.Raw),
			want:   false,
		},
		{
			name:   "SHA-256 record with wrong digest length is unusable",
			record: record(UsageDANEEE, SelectorCert, MatchingSHA256, cert.Raw[:16]),
			want:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := MatchCertificate(cert, tc.record); got != tc.want {
				t.Fatalf("MatchCertificate() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDANEEAllSelectorMatchingCombinations(t *testing.T) {
	pki := buildPKI(t)

	for i, rec := range allRecordsFor(UsageDANEEE, pki.leaf) {
		if err := VerifyChain(pki.chainA, []TLSARecord{rec}); err != nil {
			t.Fatalf("EE record %d (selector=%d matching=%d): unexpected error: %v",
				i, rec.Selector, rec.MatchingType, err)
		}
	}
}

func TestDANEESkipsNameAndValidity(t *testing.T) {
	pki := buildPKI(t)

	// Expired leaf: DANE-EE ignores the validity period.
	for _, rec := range allRecordsFor(UsageDANEEE, pki.expiredLeaf) {
		if err := VerifyChain(pki.expiredChain, []TLSARecord{rec}); err != nil {
			t.Fatalf("expired leaf with selector=%d matching=%d: %v",
				rec.Selector, rec.MatchingType, err)
		}
	}

	// Self-signed leaf with no chain: DANE-EE needs no trust anchor.
	for _, rec := range allRecordsFor(UsageDANEEE, pki.selfLeaf) {
		if err := VerifyChain(pki.selfChain, []TLSARecord{rec}); err != nil {
			t.Fatalf("self-signed leaf with selector=%d matching=%d: %v",
				rec.Selector, rec.MatchingType, err)
		}
	}
}

func TestDANEEOnlyMatchesLeaf(t *testing.T) {
	pki := buildPKI(t)

	// Pinning the intermediate under usage 3 must fail: EE pins bind the leaf.
	for _, rec := range allRecordsFor(UsageDANEEE, pki.inter) {
		err := VerifyChain(pki.chainA, []TLSARecord{rec})
		if !errors.Is(err, ErrNoMatch) {
			t.Fatalf("EE pin on intermediate (selector=%d matching=%d): got %v, want ErrNoMatch",
				rec.Selector, rec.MatchingType, err)
		}
	}

	// A pin for an unrelated leaf also fails.
	for _, rec := range allRecordsFor(UsageDANEEE, pki.leafB) {
		if err := VerifyChain(pki.chainA, []TLSARecord{rec}); !errors.Is(err, ErrNoMatch) {
			t.Fatalf("foreign EE pin (selector=%d matching=%d): got %v, want ErrNoMatch",
				rec.Selector, rec.MatchingType, err)
		}
	}
}

func TestDANETAAcceptsChainsToPinnedAnchor(t *testing.T) {
	pki := buildPKI(t)

	// Pinning the root, across every selector/matching combination.
	for _, rec := range allRecordsFor(UsageDANETA, pki.rootA) {
		if err := VerifyChain(pki.chainA, []TLSARecord{rec}); err != nil {
			t.Fatalf("TA root pin selector=%d matching=%d: %v",
				rec.Selector, rec.MatchingType, err)
		}
	}

	// Pinning the intermediate CA is also valid: the leaf chains to it.
	for _, rec := range allRecordsFor(UsageDANETA, pki.inter) {
		if err := VerifyChain(pki.chainA, []TLSARecord{rec}); err != nil {
			t.Fatalf("TA intermediate pin selector=%d matching=%d: %v",
				rec.Selector, rec.MatchingType, err)
		}
	}
}

func TestDANETABypassAttempts(t *testing.T) {
	pki := buildPKI(t)

	t.Run("pinned anchor absent from presented chain", func(t *testing.T) {
		// leafB chains to rootB, but the TLSA record pins rootA.
		for _, rec := range allRecordsFor(UsageDANETA, pki.rootA) {
			err := VerifyChain(pki.chainB, []TLSARecord{rec})
			if !errors.Is(err, ErrNoMatch) {
				t.Fatalf("selector=%d matching=%d: got %v, want ErrNoMatch",
					rec.Selector, rec.MatchingType, err)
			}
		}
	})

	t.Run("pinned anchor present but leaf does not chain to it", func(t *testing.T) {
		// rootA is smuggled into the bundle, but leafB is signed by rootB.
		// Matching the anchor is not enough: the path must terminate at it.
		for _, rec := range allRecordsFor(UsageDANETA, pki.rootA) {
			err := VerifyChain(pki.chainBWithA, []TLSARecord{rec})
			if !errors.Is(err, ErrNoMatch) {
				t.Fatalf("selector=%d matching=%d: got %v, want ErrNoMatch",
					rec.Selector, rec.MatchingType, err)
			}
		}
	})

	t.Run("TA record pinning the leaf cannot act as trust anchor", func(t *testing.T) {
		// A leaf certificate is not a CA, so usage-2 matching on it fails.
		for _, rec := range allRecordsFor(UsageDANETA, pki.leaf) {
			err := VerifyChain(pki.chainA, []TLSARecord{rec})
			if !errors.Is(err, ErrNoMatch) {
				t.Fatalf("selector=%d matching=%d: got %v, want ErrNoMatch",
					rec.Selector, rec.MatchingType, err)
			}
		}
	})

	t.Run("expired leaf is rejected under DANE-TA", func(t *testing.T) {
		// DANE-TA runs normal PKIX path validation, including validity.
		for _, rec := range allRecordsFor(UsageDANETA, pki.rootA) {
			err := VerifyChain(pki.expiredChain, []TLSARecord{rec})
			if !errors.Is(err, ErrNoMatch) {
				t.Fatalf("selector=%d matching=%d: got %v, want ErrNoMatch",
					rec.Selector, rec.MatchingType, err)
			}
		}
	})

	t.Run("leaf alone cannot satisfy a TA pin", func(t *testing.T) {
		for _, rec := range allRecordsFor(UsageDANETA, pki.inter) {
			err := VerifyChain(pki.leafOnlyChain, []TLSARecord{rec})
			if !errors.Is(err, ErrNoMatch) {
				t.Fatalf("selector=%d matching=%d: got %v, want ErrNoMatch",
					rec.Selector, rec.MatchingType, err)
			}
		}
	})
}

// Multiple TLSA records for one name: any single match passes; the chain is
// rejected only when every usable record fails.
func TestMultipleRecordsAnyMatch(t *testing.T) {
	pki := buildPKI(t)

	leafRecords := allRecordsFor(UsageDANEEE, pki.leaf)
	foreignRecords := allRecordsFor(UsageDANEEE, pki.leafB)
	rootRecords := allRecordsFor(UsageDANETA, pki.rootA)

	t.Run("several records, one valid hit passes", func(t *testing.T) {
		records := []TLSARecord{
			foreignRecords[0],
			foreignRecords[1],
			leafRecords[1], // valid leaf SHA-256 pin
			foreignRecords[2],
		}
		if err := VerifyChain(pki.chainA, records); err != nil {
			t.Fatalf("partial hit should pass, got: %v", err)
		}
	})

	t.Run("EE and TA records mixed, TA hit passes", func(t *testing.T) {
		records := []TLSARecord{
			foreignRecords[0],
			rootRecords[1], // valid root anchor SHA-256 pin
		}
		if err := VerifyChain(pki.chainA, records); err != nil {
			t.Fatalf("TA hit among failing EE records should pass, got: %v", err)
		}
	})

	t.Run("all records miss is rejected", func(t *testing.T) {
		records := []TLSARecord{
			leafRecords[0], // pins leafA, but chainB presents leafB
			leafRecords[1],
			rootRecords[2], // pins rootA but chainB never reaches it
		}
		err := VerifyChain(pki.chainB, records)
		if !errors.Is(err, ErrNoMatch) {
			t.Fatalf("all-miss should be ErrNoMatch, got: %v", err)
		}
	})

	t.Run("unusable records alongside a valid hit still pass", func(t *testing.T) {
		records := []TLSARecord{
			record(TLSAUsage(0), SelectorCert, MatchingFull, pki.leaf.Raw),     // unsupported usage
			record(UsageDANEEE, TLSASelector(9), MatchingFull, pki.leaf.Raw),   // bad selector
			record(UsageDANEEE, SelectorCert, MatchingSHA256, []byte("short")), // bad digest length
			leafRecords[0],
		}
		if err := VerifyChain(pki.chainA, records); err != nil {
			t.Fatalf("valid hit with unusable siblings should pass, got: %v", err)
		}
	})

	t.Run("only unusable records returns ErrNoTLSARecords", func(t *testing.T) {
		records := []TLSARecord{
			record(TLSAUsage(0), SelectorCert, MatchingFull, pki.leaf.Raw),
			record(UsageDANEEE, TLSASelector(9), MatchingFull, pki.leaf.Raw),
		}
		err := VerifyChain(pki.chainA, records)
		if !errors.Is(err, ErrNoTLSARecords) {
			t.Fatalf("got %v, want ErrNoTLSARecords", err)
		}
	})
}

func TestEmptyInputs(t *testing.T) {
	pki := buildPKI(t)

	if err := VerifyChain(nil, allRecordsFor(UsageDANEEE, pki.leaf)); err == nil {
		t.Fatal("empty chain should be rejected")
	}
	if err := VerifyChain(pki.chainA, nil); !errors.Is(err, ErrNoTLSARecords) {
		t.Fatalf("empty records: got %v, want ErrNoTLSARecords", err)
	}
}

func TestVerifyConnection(t *testing.T) {
	pki := buildPKI(t)

	state := tls.ConnectionState{PeerCertificates: pki.chainA}
	leafSHA256 := sha256.Sum256(pki.leaf.Raw)
	good := record(UsageDANEEE, SelectorCert, MatchingSHA256, leafSHA256[:])
	if err := VerifyConnection(state, []TLSARecord{good}); err != nil {
		t.Fatalf("VerifyConnection with valid pin: %v", err)
	}

	badLeafSHA256 := sha256.Sum256(pki.leafB.Raw)
	bad := record(UsageDANEEE, SelectorCert, MatchingSHA256, badLeafSHA256[:])
	if err := VerifyConnection(state, []TLSARecord{bad}); !errors.Is(err, ErrNoMatch) {
		t.Fatalf("VerifyConnection with bad pin: got %v, want ErrNoMatch", err)
	}
}
