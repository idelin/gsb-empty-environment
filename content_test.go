package netsec

import (
	"errors"
	"testing"
)

func TestVerifySignedData_DigestCoversDifferentContent(t *testing.T) {
	for _, kind := range allKeyKinds() {
		t.Run(kindName(kind), func(t *testing.T) {
			tc := mustCert(t, kind)
			der, err := buildSignedData(tc, kind, testContent, builderOptions{
				signWrongContent: true,
			})
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if _, err := VerifySignedData(der, nil); !errors.Is(err, ErrDigestMismatch) {
				t.Fatalf("err = %v, want ErrDigestMismatch", err)
			}
		})
	}
}

func TestVerifySignedData_NoSignedAttrsTamperedContent(t *testing.T) {
	for _, kind := range allKeyKinds() {
		t.Run(kindName(kind), func(t *testing.T) {
			tc := mustCert(t, kind)
			der, err := buildSignedData(tc, kind, testContent, builderOptions{
				omitSignedAttrs: true,
				contentOverride: []byte("content replaced after signing"),
			})
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if _, err := VerifySignedData(der, nil); !errors.Is(err, ErrSignature) {
				t.Fatalf("err = %v, want ErrSignature", err)
			}
		})
	}
}

func TestVerifySignedData_AttachedRejectsExternalContent(t *testing.T) {
	tc := mustCert(t, keyRSA)
	der, err := buildSignedData(tc, keyRSA, testContent, builderOptions{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, err := VerifySignedData(der, testContent); !errors.Is(err, ErrContentMismatch) {
		t.Fatalf("err = %v, want ErrContentMismatch", err)
	}
}

func TestVerifySignedData_GarbageInput(t *testing.T) {
	if _, err := VerifySignedData([]byte{0x30, 0x03, 0x02, 0x01, 0x01}, nil); !errors.Is(err, ErrInvalidSignedData) {
		t.Fatalf("err = %v, want ErrInvalidSignedData", err)
	}
	if _, err := VerifySignedData(nil, nil); !errors.Is(err, ErrInvalidSignedData) {
		t.Fatalf("nil input: err = %v, want ErrInvalidSignedData", err)
	}
}
