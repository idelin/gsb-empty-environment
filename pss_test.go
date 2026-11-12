package netsec

import (
	"errors"
	"testing"
)

func TestVerifySignedData_RSASSA_PSS(t *testing.T) {
	tc := mustCert(t, keyRSA)
	der, err := buildSignedData(tc, keyRSA, testContent, builderOptions{usePSS: true})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	certs, err := VerifySignedData(der, nil)
	if err != nil {
		t.Fatalf("verify PSS: %v", err)
	}
	if len(certs) != 1 {
		t.Fatalf("got %d certificates, want 1", len(certs))
	}
}

func TestVerifySignedData_RSASSA_PSS_TamperedDigest(t *testing.T) {
	tc := mustCert(t, keyRSA)
	badDigest := make([]byte, 32)
	for i := range badDigest {
		badDigest[i] = 0xCD
	}
	der, err := buildSignedData(tc, keyRSA, testContent, builderOptions{
		usePSS:         true,
		digestOverride: badDigest,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, err := VerifySignedData(der, nil); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("err = %v, want ErrDigestMismatch", err)
	}
}

func TestVerifySignedData_RSASSA_PSS_MissingParams(t *testing.T) {
	tc := mustCert(t, keyRSA)
	der, err := buildSignedData(tc, keyRSA, testContent, builderOptions{usePSS: true})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	stripped, err := stripPSSParams(t, der)
	if err != nil {
		t.Fatalf("strip params: %v", err)
	}
	if _, err := VerifySignedData(stripped, nil); !errors.Is(err, ErrUnsupportedAlgorithm) {
		t.Fatalf("err = %v, want ErrUnsupportedAlgorithm", err)
	}
}

func TestVerifySignedData_RSASSA_PSS_DefaultSaltLength(t *testing.T) {
	tc := mustCert(t, keyRSA)
	der, err := buildSignedData(tc, keyRSA, testContent, builderOptions{usePSS: true})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	rewritten, err := omitPSSSaltLength(t, der)
	if err != nil {
		t.Fatalf("omit salt length: %v", err)
	}
	if _, err := VerifySignedData(rewritten, nil); err != nil {
		t.Fatalf("verify with default salt length: %v", err)
	}
}
