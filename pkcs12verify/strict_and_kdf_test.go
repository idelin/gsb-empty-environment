package pkcs12verify

import (
	"crypto/sha256"
	"encoding/asn1"
	"encoding/hex"
	"errors"
	"testing"
)

func TestStrictDERScanner(t *testing.T) {
	valid := mustMarshal(3)

	t.Run("canonical", func(t *testing.T) {
		if err := requireSingleDER(valid); err != nil {
			t.Fatalf("valid DER rejected: %v", err)
		}
	})
	t.Run("non-minimal length", func(t *testing.T) {
		nonCanonical := []byte{0x02, 0x81, 0x01, 0x03}
		if err := requireSingleDER(nonCanonical); !errors.Is(err, ErrNonCanonicalDER) {
			t.Fatalf("expected ErrNonCanonicalDER, got %v", err)
		}
	})
	t.Run("indefinite length", func(t *testing.T) {
		indef := []byte{0x30, 0x80, 0x02, 0x01, 0x01, 0x00, 0x00}
		if err := requireSingleDER(indef); !errors.Is(err, ErrNonCanonicalDER) {
			t.Fatalf("expected ErrNonCanonicalDER, got %v", err)
		}
	})
	t.Run("constructed octet string", func(t *testing.T) {
		constructed := []byte{0x24, 0x03, 0x04, 0x01, 0x09}
		if err := requireSingleDER(constructed); !errors.Is(err, ErrNonCanonicalDER) {
			t.Fatalf("expected ErrNonCanonicalDER, got %v", err)
		}
	})
	t.Run("truncated content", func(t *testing.T) {
		truncated := []byte{0x04, 0x05, 0x01, 0x02}
		if err := requireSingleDER(truncated); !errors.Is(err, ErrMalformed) {
			t.Fatalf("expected ErrMalformed, got %v", err)
		}
	})
	t.Run("nested non-canonical inside sequence", func(t *testing.T) {
		// SEQUENCE containing a non-minimal-length INTEGER.
		nested := []byte{0x30, 0x04, 0x02, 0x81, 0x01, 0x03}
		if err := requireSingleDER(nested); !errors.Is(err, ErrNonCanonicalDER) {
			t.Fatalf("expected ErrNonCanonicalDER, got %v", err)
		}
	})
}

func TestNonCanonicalAuthSafeRejectedBeforeMAC(t *testing.T) {
	der, authSafeContent := newBuilder().addCert().Build()

	var pfx pfxPdu
	if _, err := asn1.Unmarshal(der, &pfx); err != nil {
		t.Fatal(err)
	}
	badInner := nonMinimalWrap(authSafeContent)
	pfx.AuthSafe.Content = explicitContext(0, badInner)
	rebad := mustMarshal(pfx)

	_, err := Verify(rebad, "correct horse battery staple", Options{})
	if !errors.Is(err, ErrNonCanonicalDER) {
		t.Fatalf("expected ErrNonCanonicalDER, got %v", err)
	}
}

func TestEmptyAndGarbageInput(t *testing.T) {
	if _, err := Verify(nil, "x", Options{}); !errors.Is(err, ErrMalformed) {
		t.Fatalf("nil input: expected ErrMalformed, got %v", err)
	}
	if _, err := Verify([]byte{0x41, 0x01, 0x00, 0xff}, "x", Options{}); err == nil {
		t.Fatal("garbage input unexpectedly accepted")
	}
}

func TestPBES2PolicyDetails(t *testing.T) {
	t.Run("missing PRF rejected", func(t *testing.T) {
		algo := pbes2Algo(DefaultMinIterations, patternBytes(16, 0x44), patternBytes(16, 0x55), false, oidHMACWithSHA256, false)
		b := newBuilder().addCert()
		b.bags = append(b.bags, shroudedKeyBagDER(algo, patternBytes(48, 0x66)))
		der, _ := b.Build()

		_, err := Verify(der, b.password, Options{})
		if !errors.Is(err, ErrDigestAlgorithm) {
			t.Fatalf("expected ErrDigestAlgorithm, got %v", err)
		}
	})
	t.Run("SHA1 PRF rejected", func(t *testing.T) {
		algo := pbes2Algo(DefaultMinIterations, patternBytes(16, 0x44), patternBytes(16, 0x55), false, oidHMACWithSHA1, true)
		b := newBuilder().addCert()
		b.bags = append(b.bags, shroudedKeyBagDER(algo, patternBytes(48, 0x66)))
		der, _ := b.Build()

		_, err := Verify(der, b.password, Options{})
		if !errors.Is(err, ErrDigestAlgorithm) {
			t.Fatalf("expected ErrDigestAlgorithm, got %v", err)
		}
	})
	t.Run("AES-128 not allowed", func(t *testing.T) {
		kdf := algorithmIdentifier{
			Algorithm: oidPBKDF2,
			Parameters: rawFull(mustMarshal(pbkdf2Params{
				Salt:           octetRaw(patternBytes(16, 0x44)),
				IterationCount: DefaultMinIterations,
				PRF:            algorithmIdentifier{Algorithm: oidHMACWithSHA256, Parameters: nullRaw()},
			})),
		}
		scheme := algorithmIdentifier{Algorithm: oidAES128CBC, Parameters: rawFull(mustMarshal(patternBytes(16, 0x55)))}
		algo := algorithmIdentifier{Algorithm: oidPBES2, Parameters: rawFull(mustMarshal(pbes2Params{KeyDerivationFunc: kdf, EncryptionScheme: scheme}))}

		if err := verifyPBES2(algo, Options{}.withDefaults()); !errors.Is(err, ErrUnsupportedCipher) {
			t.Fatalf("expected ErrUnsupportedCipher, got %v", err)
		}
	})
	t.Run("bad GCM nonce length", func(t *testing.T) {
		algo := pbes2Algo(DefaultMinIterations, patternBytes(16, 0x44), patternBytes(8, 0x55), true, oidHMACWithSHA256, true)
		if err := verifyPBES2(algo, Options{}.withDefaults()); !errors.Is(err, ErrMalformed) {
			t.Fatalf("expected ErrMalformed, got %v", err)
		}
	})
}

func TestBMPStringEncoding(t *testing.T) {
	if got := bmpStringZeroTerminated(""); len(got) != 2 || got[0] != 0 || got[1] != 0 {
		t.Fatalf("empty password must be double zero, got %x", got)
	}
	got := bmpStringZeroTerminated("A")
	want := []byte{0x00, 0x41, 0x00, 0x00}
	if hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Fatalf("BMP encoding mismatch: got %x want %x", got, want)
	}
	// "abc" -> UTF-16-BE units then U+0000 terminator.
	got = bmpStringZeroTerminated("abc")
	want = []byte{0, 'a', 0, 'b', 0, 'c', 0, 0}
	if hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Fatalf("BMP encoding mismatch: got %x want %x", got, want)
	}
}

// TestPBKDF2KnownAnswer uses the RFC 7914 PBKDF2-HMAC-SHA256 vector
// (P="password", S="salt", c=2, dkLen=32, first 32 bytes of the 64-byte KAT).
func TestPBKDF2KnownAnswer(t *testing.T) {
	want := "ae4d0c95af6b46d32d0adff928f06dd02a303f8ef3c251dfd6e2d85a95474c43"
	got := pbkdf2HMAC([]byte("password"), []byte("salt"), 2, 32, sha256.New)
	if hex.EncodeToString(got) != want {
		t.Fatalf("PBKDF2 mismatch:\ngot  %x\nwant %s", got, want)
	}
}

func TestPKCS12KDFDeterministicAndLengths(t *testing.T) {
	k1 := pkcs12KDF("pw", patternBytes(20, 1), 3, 10000, 32, sha256.New)
	k2 := pkcs12KDF("pw", patternBytes(20, 1), 3, 10000, 32, sha256.New)
	if !equalBytes(k1, k2) {
		t.Fatal("PKCS12 KDF not deterministic")
	}
	if len(k1) != 32 {
		t.Fatalf("expected 32 byte MAC key, got %d", len(k1))
	}
	longer := pkcs12KDF("pw", patternBytes(20, 1), 3, 10000, 48, sha256.New)
	if len(longer) != 48 {
		t.Fatalf("expected 48 byte output, got %d", len(longer))
	}
	// Different purpose IDs must produce independent keys.
	keyID := pkcs12KDF("pw", patternBytes(20, 1), 3, 10000, 32, sha256.New)
	ivID := pkcs12KDF("pw", patternBytes(20, 1), 2, 10000, 32, sha256.New)
	if equalBytes(keyID, ivID) {
		t.Fatal("ID=2 and ID=3 produced identical derivations")
	}
}

func TestPBMAC1WrongPasswordAndShortSalt(t *testing.T) {
	b := newBuilder().addCert()
	b.pbmac1 = true
	der, _ := b.Build()

	if _, err := Verify(der, "nope", Options{}); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("PBMAC1 wrong password: expected ErrIntegrity, got %v", err)
	}

	b2 := newBuilder().addCert()
	b2.pbmac1 = true
	b2.salt = patternBytes(12, 0x11)
	der2, _ := b2.Build()
	if _, err := Verify(der2, b2.password, Options{}); !errors.Is(err, ErrSaltTooShort) {
		t.Fatalf("PBMAC1 short salt: expected ErrSaltTooShort, got %v", err)
	}
}

// rebuildWithLegacyDigestOID constructs a PFX whose DigestInfo names a
// forbidden digest (SHA-1/MD5). No valid MAC is computed: the policy gate must
// reject the OID before any KDF work happens.
func rebuildWithLegacyDigestOID(b *pfxBuilder, oid asn1.ObjectIdentifier) []byte {
	b.addCert()
	safeContents := mustMarshal(b.bags)
	authSafeContent := mustMarshal([]contentInfo{{ContentType: oidData, Content: octetRaw(safeContents)}})

	fakeDigest := make([]byte, 20)
	pfx := pfxPdu{
		Version: 3,
		AuthSafe: contentInfo{
			ContentType: oidData,
			Content:     explicitOctet(authSafeContent),
		},
		MacData: macData{
			Mac: digestInfo{
				Algorithm: algorithmIdentifier{Algorithm: oid, Parameters: nullRaw()},
				Digest:    fakeDigest,
			},
			MacSalt:    b.salt,
			Iterations: b.iterations,
		},
	}
	return mustMarshal(pfx)
}

// nonMinimalWrap re-encodes an OCTET STRING payload using a non-minimal
// long-form length, producing deliberately invalid DER.
func nonMinimalWrap(payload []byte) []byte {
	return append([]byte{0x04, 0x81, byte(len(payload))}, payload...)
}

func equalBytes(a, b []byte) bool {
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
