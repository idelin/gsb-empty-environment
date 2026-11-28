package pkcs12verify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/asn1"
	"errors"
	"hash"
	"testing"
)

func sha256New() hash.Hash { return sha256.New() }

func hmacSHA256(key, payload []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(payload)
	return m.Sum(nil)
}

func TestIsPolicyError(t *testing.T) {
	if IsPolicyError(nil) {
		t.Fatal("nil must not be a policy error")
	}
	if !IsPolicyError(wrap(ErrWeakCipher, "x")) {
		t.Fatal("wrapped sentinel must be classified")
	}
	if IsPolicyError(errors.New("unrelated")) {
		t.Fatal("unrelated error must not be classified")
	}
}

func TestPolicyWindowAndSaltCustomization(t *testing.T) {
	b := newBuilder().addCert()
	b.iterations = 20000
	der, _ := b.Build()

	// Same vector accepted under a tighter window, rejected under a wider
	// minimum that 20k cannot meet.
	if _, err := Verify(der, b.password, Options{MinIterations: 10000, MaxIterations: 50000, MinSaltLength: 16}); err != nil {
		t.Fatalf("vector inside custom window rejected: %v", err)
	}
	if _, err := Verify(der, b.password, Options{MinIterations: 30000, MaxIterations: 50000, MinSaltLength: 16}); !errors.Is(err, ErrIterations) {
		t.Fatalf("expected ErrIterations, got %v", err)
	}
}

func TestPBMAC1LegacyFieldsRejected(t *testing.T) {
	b := newBuilder().addCert()
	b.pbmac1 = true
	der, _ := b.Build()

	var pfx pfxPdu
	if _, err := asn1.Unmarshal(der, &pfx); err != nil {
		t.Fatal(err)
	}
	pfx.MacData.MacSalt = patternBytes(16, 0x01)
	rebad := mustMarshal(pfx)

	if _, err := Verify(rebad, b.password, Options{}); !errors.Is(err, ErrMalformed) {
		t.Fatalf("expected ErrMalformed for PBMAC1 legacy salt, got %v", err)
	}
}

func TestPBMAC1UnsupportedSchemeAndKeyLength(t *testing.T) {
	t.Run("HMAC-SHA384 scheme rejected", func(t *testing.T) {
		algo := buildPBMAC1Algo(oidHMACWithSHA384, DefaultMinIterations, 0)
		der := buildPFXWithMacAlgo(t, algo)
		if _, err := Verify(der, "pw", Options{}); !errors.Is(err, ErrDigestAlgorithm) {
			t.Fatalf("expected ErrDigestAlgorithm, got %v", err)
		}
	})
	t.Run("non-256 key length rejected", func(t *testing.T) {
		algo := buildPBMAC1Algo(oidHMACWithSHA256, DefaultMinIterations, 48)
		der := buildPFXWithMacAlgo(t, algo)
		if _, err := Verify(der, "pw", Options{}); !errors.Is(err, ErrUnsupportedCipher) {
			t.Fatalf("expected ErrUnsupportedCipher, got %v", err)
		}
	})
}

func TestPBES2RejectsNonPBKDF2KDFAndBadParams(t *testing.T) {
	t.Run("legacy PBE used as PBES2 KDF", func(t *testing.T) {
		kdf := algorithmIdentifier{Algorithm: oidPbeWithSHAAnd3Key3DES}
		scheme := algorithmIdentifier{Algorithm: oidAES256CBC, Parameters: rawFull(mustMarshal(patternBytes(16, 1)))}
		algo := algorithmIdentifier{Algorithm: oidPBES2, Parameters: rawFull(mustMarshal(pbes2Params{KeyDerivationFunc: kdf, EncryptionScheme: scheme}))}
		if err := verifyPBES2(algo, Options{}.withDefaults()); !errors.Is(err, ErrWeakCipher) {
			t.Fatalf("expected ErrWeakCipher, got %v", err)
		}
	})
	t.Run("malformed PBES2 parameters", func(t *testing.T) {
		algo := algorithmIdentifier{Algorithm: oidPBES2, Parameters: rawFull([]byte{0x04, 0x01, 0x00})}
		if err := verifyPBES2(algo, Options{}.withDefaults()); !errors.Is(err, ErrMalformed) {
			t.Fatalf("expected ErrMalformed, got %v", err)
		}
	})
	t.Run("CBC IV wrong length", func(t *testing.T) {
		algo := pbes2Algo(DefaultMinIterations, patternBytes(16, 0x44), patternBytes(8, 0x55), false, oidHMACWithSHA256, true)
		if err := verifyPBES2(algo, Options{}.withDefaults()); !errors.Is(err, ErrMalformed) {
			t.Fatalf("expected ErrMalformed, got %v", err)
		}
	})
}

func TestUnknownBagAndCertTypeRejected(t *testing.T) {
	t.Run("unknown SafeBag OID", func(t *testing.T) {
		b := newBuilder()
		b.bags = []safeBag{{
			ID:    oidSecretBag,
			Value: explicitContext(0, mustMarshal(octetRaw(patternBytes(8, 1)))),
		}}
		der, _ := b.Build()
		if _, err := Verify(der, b.password, Options{}); !errors.Is(err, ErrUnsupportedBag) {
			t.Fatalf("expected ErrUnsupportedBag, got %v", err)
		}
	})
	t.Run("SDSI certificate rejected", func(t *testing.T) {
		inner := mustMarshal(certBag{ID: oidSDSICertificate, Data: patternBytes(20, 1)})
		b := newBuilder()
		b.bags = []safeBag{{ID: oidCertBag, Value: explicitContext(0, inner)}}
		der, _ := b.Build()
		if _, err := Verify(der, b.password, Options{}); !errors.Is(err, ErrUnsupportedBag) {
			t.Fatalf("expected ErrUnsupportedBag, got %v", err)
		}
	})
}

func TestMacDigestLengthMismatch(t *testing.T) {
	b := newBuilder().addCert()
	der, _ := b.Build()

	var pfx pfxPdu
	if _, err := asn1.Unmarshal(der, &pfx); err != nil {
		t.Fatal(err)
	}
	pfx.MacData.Mac.Digest = pfx.MacData.Mac.Digest[:16]
	rebad := mustMarshal(pfx)

	if _, err := Verify(rebad, b.password, Options{}); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("expected ErrIntegrity for digest length mismatch, got %v", err)
	}
}

func TestEmptyAuthenticatedSafeRejected(t *testing.T) {
	b := newBuilder()
	// No bags at all: builder would fail on empty SafeContents, so construct
	// directly with an empty AuthenticatedSafe sequence.
	emptyAS := mustMarshal([]contentInfo{})
	macValue := computeLegacyMAC(t, b, emptyAS)
	pfx := pfxPdu{
		Version:  3,
		AuthSafe: contentInfo{ContentType: oidData, Content: explicitOctet(emptyAS)},
		MacData: macData{
			Mac: digestInfo{
				Algorithm: algorithmIdentifier{Algorithm: oidSHA256, Parameters: nullRaw()},
				Digest:    macValue,
			},
			MacSalt:    b.salt,
			Iterations: b.iterations,
		},
	}
	if _, err := Verify(mustMarshal(pfx), b.password, Options{}); !errors.Is(err, ErrMalformed) {
		t.Fatalf("expected ErrMalformed for empty AuthenticatedSafe, got %v", err)
	}
}

// buildPBMAC1Algo constructs a PBMAC1 AlgorithmIdentifier with configurable
// scheme and optional key length for negative-path tests.
func buildPBMAC1Algo(schemeOID asn1.ObjectIdentifier, iterations, keyLength int) algorithmIdentifier {
	params := pbkdf2Params{
		Salt:           octetRaw(patternBytes(16, 0x71)),
		IterationCount: iterations,
		PRF:            algorithmIdentifier{Algorithm: oidHMACWithSHA256, Parameters: nullRaw()},
	}
	if keyLength != 0 {
		params.KeyLength = keyLength
	}
	kdf := algorithmIdentifier{Algorithm: oidPBKDF2, Parameters: rawFull(mustMarshal(params))}
	scheme := algorithmIdentifier{Algorithm: schemeOID, Parameters: nullRaw()}
	return algorithmIdentifier{
		Algorithm:  oidPBMAC1,
		Parameters: rawFull(mustMarshal(pbmac1Params{KeyDerivationFunc: kdf, MessageAuthScheme: scheme})),
	}
}

func buildPFXWithMacAlgo(t *testing.T, algo algorithmIdentifier) []byte {
	t.Helper()
	b := newBuilder().addCert()
	safeContents := mustMarshal(b.bags)
	authSafeContent := mustMarshal([]contentInfo{{ContentType: oidData, Content: explicitOctet(safeContents)}})

	var digest []byte
	var kdf pbkdf2Params
	var pm pbmac1Params
	if err := unmarshalStrict(algo.Parameters.FullBytes, &pm); err != nil {
		t.Fatal(err)
	}
	if err := unmarshalStrict(pm.KeyDerivationFunc.Parameters.FullBytes, &kdf); err != nil {
		t.Fatal(err)
	}
	key := pbkdf2HMAC([]byte(b.password), kdf.Salt.Bytes, kdf.IterationCount, 32, sha256New)
	digest = hmacSHA256(key, authSafeContent)

	pfx := pfxPdu{
		Version:  3,
		AuthSafe: contentInfo{ContentType: oidData, Content: explicitOctet(authSafeContent)},
		MacData:  macData{Mac: digestInfo{Algorithm: algo, Digest: digest}},
	}
	return mustMarshal(pfx)
}

func computeLegacyMAC(t *testing.T, b *pfxBuilder, payload []byte) []byte {
	t.Helper()
	return hmacSHA256(
		pkcs12KDF(b.password, b.salt, 3, b.iterations, 32, sha256New),
		payload,
	)
}
