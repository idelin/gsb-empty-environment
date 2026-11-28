package pkcs12verify

import (
	"encoding/asn1"
	"errors"
	"testing"
)

func TestValidLegacyPFX_AES256CBC(t *testing.T) {
	der, _ := newBuilder().addCert().addShroudedKey(false).Build()

	report, err := Verify(der, "correct horse battery staple", Options{})
	if err != nil {
		t.Fatalf("valid PFX rejected: %v", err)
	}
	if report.MACMode != MACLegacy || report.CertBagCount != 1 || report.ShroudedKeyCount != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if len(report.Ciphers) != 1 || report.Ciphers[0].Algorithm != CipherAES256CBC {
		t.Fatalf("unexpected cipher info: %+v", report.Ciphers)
	}
	if report.Ciphers[0].KDFIterations != DefaultMinIterations {
		t.Fatalf("KDF iterations not propagated: %+v", report.Ciphers[0])
	}
}

func TestValidLegacyPFX_AES256GCM(t *testing.T) {
	der, _ := newBuilder().addCert().addShroudedKey(true).Build()

	report, err := Verify(der, "correct horse battery staple", Options{})
	if err != nil {
		t.Fatalf("valid GCM PFX rejected: %v", err)
	}
	if report.Ciphers[0].Algorithm != CipherAES256GCM {
		t.Fatalf("expected AES-256-GCM, got %+v", report.Ciphers[0])
	}
}

func TestValidPBMAC1(t *testing.T) {
	b := newBuilder().addCert().addShroudedKey(false)
	b.pbmac1 = true
	der, _ := b.Build()

	report, err := Verify(der, "correct horse battery staple", Options{})
	if err != nil {
		t.Fatalf("valid PBMAC1 PFX rejected: %v", err)
	}
	if report.MACMode != MACPBMAC1 || report.MACDigestOID != oidPBMAC1.String() {
		t.Fatalf("unexpected MAC mode: %+v", report)
	}
}

func TestValidEncryptedDataEnvelope(t *testing.T) {
	b := newBuilder().addCert().addShroudedKey(false)
	b.withEncData = true
	der, _ := b.Build()

	report, err := Verify(der, b.password, Options{})
	if err != nil {
		t.Fatalf("EncryptedData envelope rejected: %v", err)
	}
	if !report.EncryptedData {
		t.Fatalf("EncryptedData flag not set: %+v", report)
	}
}

func TestMissingMacData(t *testing.T) {
	b := newBuilder().addCert()
	b.omitMac = true
	der, _ := b.Build()

	_, err := Verify(der, "anything", Options{})
	if !errors.Is(err, ErrMissingMacData) {
		t.Fatalf("expected ErrMissingMacData, got %v", err)
	}
}

func TestTamperedSafeContentsCAInjection(t *testing.T) {
	b := newBuilder().addCert().addShroudedKey(false)
	b.tamperAuthSafe = true
	der, _ := b.Build()

	_, err := Verify(der, b.password, Options{})
	if !errors.Is(err, ErrIntegrity) {
		t.Fatalf("injected CA must fail MAC, got %v", err)
	}
}

func TestIterationDoSAndWeakIterations(t *testing.T) {
	cases := []struct {
		name string
		iter int
	}{
		{"one iteration weak", 1},
		{"below minimum", DefaultMinIterations - 1},
		{"hundred million DoS", 100_000_000},
		{"above maximum", DefaultMaxIterations + 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newBuilder().addCert()
			b.iterations = DefaultMinIterations
			der, _ := b.Build()
			der = rewriteLegacyIterations(t, der, tc.iter)

			_, err := Verify(der, b.password, Options{})
			if !errors.Is(err, ErrIterations) {
				t.Fatalf("iter=%d: expected ErrIterations, got %v", tc.iter, err)
			}
		})
	}
}

func TestWeakMACDigestDowngrade(t *testing.T) {
	for _, oid := range []asn1.ObjectIdentifier{oidSHA1, oidMD5} {
		b := newBuilder().addCert()
		b.macOID = oid
		der := rebuildWithLegacyDigestOID(b, oid)

		_, err := Verify(der, b.password, Options{})
		if !errors.Is(err, ErrDigestAlgorithm) {
			t.Fatalf("oid %s: expected ErrDigestAlgorithm, got %v", oid, err)
		}
	}
}

func TestWeakShroudedAlgorithms(t *testing.T) {
	weakOIDs := []asn1.ObjectIdentifier{
		oidPbeWithSHAAnd40BitRC2,
		oidPbeWithSHAAnd128RC4,
		oidPbeWithSHAAnd3Key3DES,
		oidPbeWithMD5AndDES,
		oidPbeWithSHA1AndDES,
		oid3DESCBC,
		oidRC2CBC,
		oidRC4,
	}
	for _, oid := range weakOIDs {
		b := newBuilder().addCert().addWeakShroudedKey(oid)
		der, _ := b.Build()

		_, err := Verify(der, b.password, Options{})
		if !errors.Is(err, ErrWeakCipher) {
			t.Fatalf("oid %s: expected ErrWeakCipher, got %v", oid, err)
		}
	}
}

func TestPlaintextKeyBagRejected(t *testing.T) {
	b := newBuilder().addCert().addPlainKey()
	der, _ := b.Build()

	_, err := Verify(der, b.password, Options{})
	if !errors.Is(err, ErrUnencryptedKey) {
		t.Fatalf("expected ErrUnencryptedKey, got %v", err)
	}
}

func TestWrongPasswordFailsConstantTimeMAC(t *testing.T) {
	der, _ := newBuilder().addCert().addShroudedKey(false).Build()

	_, err := Verify(der, "wrong-password", Options{})
	if !errors.Is(err, ErrIntegrity) {
		t.Fatalf("expected ErrIntegrity on wrong password, got %v", err)
	}
}

func TestShortSaltRejected(t *testing.T) {
	b := newBuilder().addCert()
	b.salt = patternBytes(15, 0x11)
	der, _ := b.Build()

	_, err := Verify(der, b.password, Options{})
	if !errors.Is(err, ErrSaltTooShort) {
		t.Fatalf("expected ErrSaltTooShort, got %v", err)
	}
}

func TestUnsupportedVersion(t *testing.T) {
	der, _ := newBuilder().addCert().Build()
	var pfx pfxPdu
	if _, err := asn1.Unmarshal(der, &pfx); err != nil {
		t.Fatal(err)
	}
	pfx.Version = 999
	rebad := mustMarshal(pfx)

	_, err := Verify(rebad, "correct horse battery staple", Options{})
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("expected ErrUnsupportedVersion, got %v", err)
	}
}

func TestTrailingBytes(t *testing.T) {
	der, _ := newBuilder().addCert().Build()
	withTrailing := append(append([]byte{}, der...), 0x00, 0x01, 0x02)

	_, err := Verify(withTrailing, "correct horse battery staple", Options{})
	if !errors.Is(err, ErrTrailingBytes) {
		t.Fatalf("expected ErrTrailingBytes, got %v", err)
	}
}
