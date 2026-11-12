package netsec

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"testing"
)

func TestVerifySignedData_SignatureMustUseSetTag(t *testing.T) {
	// A signature computed over the on-wire A0 encoding must fail: the
	// verifier re-tags signedAttrs to 0x31 before checking the signature.
	for _, kind := range allKeyKinds() {
		t.Run(kindName(kind), func(t *testing.T) {
			tc := mustCert(t, kind)
			der, err := buildSignedData(tc, kind, testContent, builderOptions{
				signOverUntagged: true,
			})
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			_, err = VerifySignedData(der, nil)
			if !errors.Is(err, ErrSignature) {
				t.Fatalf("err = %v, want ErrSignature", err)
			}
		})
	}
}

func TestVerifySignedData_TamperedSignature(t *testing.T) {
	for _, kind := range allKeyKinds() {
		t.Run(kindName(kind), func(t *testing.T) {
			tc := mustCert(t, kind)
			der, err := buildSignedData(tc, kind, testContent, builderOptions{})
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			tampered := flipSignatureByte(t, der)
			_, err = VerifySignedData(tampered, nil)
			if !errors.Is(err, ErrSignature) {
				t.Fatalf("err = %v, want ErrSignature", err)
			}
		})
	}
}

func TestVerifySignedData_DetachedContent(t *testing.T) {
	for _, kind := range allKeyKinds() {
		t.Run(kindName(kind), func(t *testing.T) {
			tc := mustCert(t, kind)
			der, err := buildSignedData(tc, kind, testContent, builderOptions{detached: true})
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if _, err := VerifySignedData(der, nil); !errors.Is(err, ErrContentMismatch) {
				t.Fatalf("missing detached content: err = %v, want ErrContentMismatch", err)
			}
			certs, err := VerifySignedData(der, testContent)
			if err != nil {
				t.Fatalf("verify detached: %v", err)
			}
			if len(certs) != 1 {
				t.Fatalf("got %d certificates, want 1", len(certs))
			}
			if _, err := VerifySignedData(der, []byte("wrong detached content")); !errors.Is(err, ErrDigestMismatch) {
				t.Fatalf("wrong detached content: err = %v, want ErrDigestMismatch", err)
			}
		})
	}
}

func TestVerifySignedData_NoSignedAttrsSignsContentDirectly(t *testing.T) {
	for _, kind := range allKeyKinds() {
		t.Run(kindName(kind), func(t *testing.T) {
			tc := mustCert(t, kind)
			der, err := buildSignedData(tc, kind, testContent, builderOptions{omitSignedAttrs: true})
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if _, err := VerifySignedData(der, nil); err != nil {
				t.Fatalf("verify: %v", err)
			}
		})
	}
}

func TestVerifySignedData_RejectsTrailingBytes(t *testing.T) {
	tc := mustCert(t, keyRSA)
	der, err := buildSignedData(tc, keyRSA, testContent, builderOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, err := VerifySignedData(append(der, 0x00), nil); !errors.Is(err, ErrInvalidSignedData) {
		t.Fatalf("err = %v, want ErrInvalidSignedData", err)
	}
}

func TestVerifySignedData_RejectsWrongOuterType(t *testing.T) {
	tc := mustCert(t, keyRSA)
	der, err := buildSignedData(tc, keyRSA, testContent, builderOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var ci ContentInfo
	if _, err := asn1.Unmarshal(der, &ci); err != nil {
		t.Fatal(err)
	}
	ci.ContentType = oidData
	rewritten, err := asn1.Marshal(ci)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifySignedData(rewritten, nil); !errors.Is(err, ErrInvalidSignedData) {
		t.Fatalf("err = %v, want ErrInvalidSignedData", err)
	}
}

func TestVerifySignedDataWithRoots_TrustedSelfSigned(t *testing.T) {
	tc := mustCert(t, keyRSA)
	der, err := buildSignedData(tc, keyRSA, testContent, builderOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(tc.cert)
	if _, err := VerifySignedDataWithRoots(der, nil, pool); err != nil {
		t.Fatalf("verify with roots: %v", err)
	}
	empty := x509.NewCertPool()
	if _, err := VerifySignedDataWithRoots(der, nil, empty); !errors.Is(err, ErrSignature) {
		t.Fatalf("untrusted root: err = %v, want ErrSignature", err)
	}
}

func TestCanonicalSignedAttrs_RetagsA0To31(t *testing.T) {
	tc := mustCert(t, keyRSA)
	der, err := buildSignedData(tc, keyRSA, testContent, builderOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	_, sd := mustParse(t, der)
	raw := sd.SignerInfos[0].SignedAttrs
	if raw.FullBytes[0] != 0xA0 {
		t.Fatalf("wire tag = %#x, want 0xA0", raw.FullBytes[0])
	}
	_, encoded, err := canonicalSignedAttrs(raw)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	if encoded[0] != 0x31 {
		t.Fatalf("canonical tag = %#x, want 0x31", encoded[0])
	}
	if len(encoded) != len(raw.FullBytes) {
		t.Fatal("re-tagging must preserve the full TLV length")
	}
	if !bytes.Equal(encoded[1:], raw.FullBytes[1:]) {
		t.Fatal("re-tagging must preserve length and contents")
	}
}

func TestDigestForOID_RejectsLegacyAlgorithms(t *testing.T) {
	sha1OID := asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}
	md5OID := asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 5}
	for _, oid := range []asn1.ObjectIdentifier{sha1OID, md5OID} {
		if _, err := digestForOID(oid); !errors.Is(err, ErrUnsupportedAlgorithm) {
			t.Fatalf("oid %s: err = %v, want ErrUnsupportedAlgorithm", oid, err)
		}
	}
	hashFunc, err := digestForOID(oidSHA256)
	if err != nil || hashFunc != crypto.SHA256 {
		t.Fatalf("SHA-256 OID: hash=%v err=%v", hashFunc, err)
	}
}
