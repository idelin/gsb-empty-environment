package netsec

import (
	"errors"
	"testing"
)

func TestVerifySignedData_DuplicateAnyAttributeRejected(t *testing.T) {
	tc := mustCert(t, keyRSA)
	der, err := buildSignedData(tc, keyRSA, testContent, builderOptions{
		attrMutator: func(attrs []Attribute) []Attribute {
			return duplicateAttr(attrs, oidSigningTime)
		},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, err := VerifySignedData(der, nil); !errors.Is(err, ErrDuplicateSignedAttribute) {
		t.Fatalf("err = %v, want ErrDuplicateSignedAttribute", err)
	}
}

func TestVerifySignedData_EmptySignedAttrsRejected(t *testing.T) {
	tc := mustCert(t, keyRSA)
	der, err := buildSignedData(tc, keyRSA, testContent, builderOptions{
		attrMutator: func(attrs []Attribute) []Attribute {
			return nil
		},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, err := VerifySignedData(der, nil); !errors.Is(err, ErrMissingSignedAttribute) {
		t.Fatalf("err = %v, want ErrMissingSignedAttribute", err)
	}
}

func TestVerifySignedData_AttributeWithoutValueRejected(t *testing.T) {
	tc := mustCert(t, keyRSA)
	der, err := buildSignedData(tc, keyRSA, testContent, builderOptions{
		attrMutator: func(attrs []Attribute) []Attribute {
			attrs[0].Values = nil
			return attrs
		},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, err := VerifySignedData(der, nil); !errors.Is(err, ErrMissingSignedAttribute) {
		t.Fatalf("err = %v, want ErrMissingSignedAttribute", err)
	}
}
