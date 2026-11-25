package evidence_test

import (
	"errors"
	"testing"

	. "prescreen.local/ps-c7499fbc67"
)

func TestPublicPolicySmoke(t *testing.T) {
	if _, err := ParsePublicIP("8.8.8.8"); err != nil {
		t.Fatalf("public address rejected: %v", err)
	}
	if _, err := ParsePublicIP("192.0.2.1"); !errors.Is(err, ErrReservedIP) {
		t.Fatalf("documentation address error = %v", err)
	}
}
