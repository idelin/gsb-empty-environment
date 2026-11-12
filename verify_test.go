package netsec

import (
	"bytes"
	"encoding/asn1"
	"errors"
	"testing"
)

var testContent = []byte("the quick brown fox jumps over the lazy dog; archive payload v1")

func allKeyKinds() []keyKind {
	return []keyKind{keyRSA, keyECDSA, keyEd25519}
}

func kindName(kind keyKind) string {
	switch kind {
	case keyRSA:
		return "RSA"
	case keyECDSA:
		return "ECDSA"
	case keyEd25519:
		return "Ed25519"
	default:
		return "unknown"
	}
}

func TestVerifySignedData_AcceptsWellFormed(t *testing.T) {
	for _, kind := range allKeyKinds() {
		t.Run(kindName(kind), func(t *testing.T) {
			tc := mustCert(t, kind)
			der, err := buildSignedData(tc, kind, testContent, builderOptions{})
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			certs, err := VerifySignedData(der, nil)
			if err != nil {
				t.Fatalf("verify: %v", err)
			}
			if len(certs) != 1 {
				t.Fatalf("got %d certificates, want 1", len(certs))
			}
			if !bytes.Equal(certs[0].Raw, tc.cert.Raw) {
				t.Fatal("returned certificate does not match signer certificate")
			}
		})
	}
}

func TestVerifySignedData_MissingContentType(t *testing.T) {
	for _, kind := range allKeyKinds() {
		t.Run(kindName(kind), func(t *testing.T) {
			tc := mustCert(t, kind)
			der, err := buildSignedData(tc, kind, testContent, builderOptions{
				attrMutator: func(attrs []Attribute) []Attribute {
					return removeAttr(attrs, oidContentType)
				},
			})
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			_, err = VerifySignedData(der, nil)
			if !errors.Is(err, ErrMissingSignedAttribute) {
				t.Fatalf("err = %v, want ErrMissingSignedAttribute", err)
			}
		})
	}
}

func TestVerifySignedData_MissingMessageDigest(t *testing.T) {
	for _, kind := range allKeyKinds() {
		t.Run(kindName(kind), func(t *testing.T) {
			tc := mustCert(t, kind)
			der, err := buildSignedData(tc, kind, testContent, builderOptions{
				attrMutator: func(attrs []Attribute) []Attribute {
					return removeAttr(attrs, oidMessageDigest)
				},
			})
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			_, err = VerifySignedData(der, nil)
			if !errors.Is(err, ErrMissingSignedAttribute) {
				t.Fatalf("err = %v, want ErrMissingSignedAttribute", err)
			}
		})
	}
}

func TestVerifySignedData_DuplicateContentType(t *testing.T) {
	for _, kind := range allKeyKinds() {
		t.Run(kindName(kind), func(t *testing.T) {
			tc := mustCert(t, kind)
			der, err := buildSignedData(tc, kind, testContent, builderOptions{
				attrMutator: func(attrs []Attribute) []Attribute {
					return duplicateAttr(attrs, oidContentType)
				},
			})
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			_, err = VerifySignedData(der, nil)
			if !errors.Is(err, ErrDuplicateSignedAttribute) {
				t.Fatalf("err = %v, want ErrDuplicateSignedAttribute", err)
			}
		})
	}
}

func TestVerifySignedData_DuplicateMessageDigest(t *testing.T) {
	for _, kind := range allKeyKinds() {
		t.Run(kindName(kind), func(t *testing.T) {
			tc := mustCert(t, kind)
			der, err := buildSignedData(tc, kind, testContent, builderOptions{
				attrMutator: func(attrs []Attribute) []Attribute {
					return duplicateAttr(attrs, oidMessageDigest)
				},
			})
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			_, err = VerifySignedData(der, nil)
			if !errors.Is(err, ErrDuplicateSignedAttribute) {
				t.Fatalf("err = %v, want ErrDuplicateSignedAttribute", err)
			}
		})
	}
}

func TestVerifySignedData_TamperedMessageDigest(t *testing.T) {
	for _, kind := range allKeyKinds() {
		t.Run(kindName(kind), func(t *testing.T) {
			tc := mustCert(t, kind)
			badDigest := bytes.Repeat([]byte{0xAB}, specForKind(kind).hash.Size())
			der, err := buildSignedData(tc, kind, testContent, builderOptions{
				digestOverride: badDigest,
			})
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			_, err = VerifySignedData(der, nil)
			if !errors.Is(err, ErrDigestMismatch) {
				t.Fatalf("err = %v, want ErrDigestMismatch", err)
			}
		})
	}
}

func TestVerifySignedData_TamperedContent(t *testing.T) {
	for _, kind := range allKeyKinds() {
		t.Run(kindName(kind), func(t *testing.T) {
			tc := mustCert(t, kind)
			der, err := buildSignedData(tc, kind, testContent, builderOptions{
				contentOverride: []byte("attacker appended unsigned payload"),
			})
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			_, err = VerifySignedData(der, nil)
			if !errors.Is(err, ErrDigestMismatch) {
				t.Fatalf("err = %v, want ErrDigestMismatch", err)
			}
		})
	}
}

func TestVerifySignedData_ContentTypeMismatch(t *testing.T) {
	for _, kind := range allKeyKinds() {
		t.Run(kindName(kind), func(t *testing.T) {
			tc := mustCert(t, kind)
			der, err := buildSignedData(tc, kind, testContent, builderOptions{
				contentTypeOverride: asn1.ObjectIdentifier{1, 2, 3, 4, 5},
			})
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			_, err = VerifySignedData(der, nil)
			if !errors.Is(err, ErrContentTypeMismatch) {
				t.Fatalf("err = %v, want ErrContentTypeMismatch", err)
			}
		})
	}
}
