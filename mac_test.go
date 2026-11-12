package netsec

import (
	"errors"
	"strings"
	"testing"
)

func TestVerifyMAC(t *testing.T) {
	values := []string{"database-password", "", "api-token", "17"}
	digest := ComputeMACHex(values)

	if err := VerifyMAC(digest, values); err != nil {
		t.Fatalf("VerifyMAC() error = %v", err)
	}
	if err := VerifyMAC(strings.ToUpper(digest), values); err != nil {
		t.Fatalf("VerifyMAC() rejected uppercase digest: %v", err)
	}
	if err := VerifyMACBytes(ComputeMAC(values), values); err != nil {
		t.Fatalf("VerifyMACBytes() error = %v", err)
	}
}

func TestVerifyMACRejectsChangedValues(t *testing.T) {
	values := []string{"database-password", "api-token"}
	digest := ComputeMACHex(values)
	values[0] = "attacker-password"

	if err := VerifyMAC(digest, values); !errors.Is(err, ErrMACMismatch) {
		t.Fatalf("VerifyMAC() error = %v, want %v", err, ErrMACMismatch)
	}
	if err := VerifyMAC("not-hex", values); !errors.Is(err, ErrMACMismatch) {
		t.Fatalf("VerifyMAC() malformed digest error = %v, want %v", err, ErrMACMismatch)
	}
}

func TestVerifyMACRejectsReorderedValues(t *testing.T) {
	values := []string{"first", "second"}
	digest := ComputeMACHex(values)
	if err := VerifyMAC(digest, []string{"second", "first"}); !errors.Is(err, ErrMACMismatch) {
		t.Fatalf("VerifyMAC() error = %v, want order sensitivity", err)
	}
}
