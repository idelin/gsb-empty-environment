package ctap2auth

import (
	"errors"
	"testing"
)

func sampleCredentials() []CredentialInfo {
	return []CredentialInfo{
		{CredentialID: []byte("rk-l1"), RPID: "acme.example", CredProtect: CredProtectUserVerificationOptional},
		{CredentialID: []byte("rk-l2"), RPID: "acme.example", CredProtect: CredProtectUserVerificationWithList},
		{CredentialID: []byte("rk-l3"), RPID: "acme.example", CredProtect: CredProtectUserVerificationRequired},
		{CredentialID: []byte("rk-other"), RPID: "other.example", CredProtect: CredProtectUserVerificationOptional},
	}
}

func ids(creds []CredentialInfo) []string {
	out := make([]string, 0, len(creds))
	for _, c := range creds {
		out = append(out, string(c.CredentialID))
	}
	return out
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// Without an allowList and without UV, an attacker must only see level-1
// credentials: level 2 and 3 resident keys are silently removed.
func TestCredProtect_NoAllowListNoUV_EnumerationBlocked(t *testing.T) {
	req := AssertionRequest{RPID: "acme.example", UserVerified: false}
	got := ids(FilterDiscoverableCredentials(sampleCredentials(), req))

	if len(got) != 1 || got[0] != "rk-l1" {
		t.Fatalf("only level-1 credential may leak, got %v", got)
	}
	if contains(got, "rk-l2") || contains(got, "rk-l3") {
		t.Fatalf("protected credential metadata leaked: %v", got)
	}
}

// With an allowList but still no UV, level 2 becomes reachable while level 3
// stays blocked (allowList does not satisfy level 3).
func TestCredProtect_AllowListNoUV(t *testing.T) {
	req := AssertionRequest{
		RPID:         "acme.example",
		AllowList:    [][]byte{[]byte("rk-l2"), []byte("rk-l3")},
		UserVerified: false,
	}
	got := ids(FilterDiscoverableCredentials(sampleCredentials(), req))
	if !contains(got, "rk-l1") || !contains(got, "rk-l2") {
		t.Fatalf("level 1 and allowListed level 2 expected, got %v", got)
	}
	if contains(got, "rk-l3") {
		t.Fatalf("level 3 must require UV even with an allowList, got %v", got)
	}

	l3 := CredentialInfo{CredentialID: []byte("rk-l3"), RPID: "acme.example", CredProtect: 3}
	if err := EnsureAssertionAccess(l3, req); !errors.Is(err, ErrOperationDenied) {
		t.Fatalf("level 3 without UV must be denied, got %v", err)
	}
}

// With valid UV all levels for the matching RP are returned; other RPs never are.
func TestCredProtect_WithUV(t *testing.T) {
	req := AssertionRequest{RPID: "acme.example", UserVerified: true}
	got := ids(FilterDiscoverableCredentials(sampleCredentials(), req))
	for _, want := range []string{"rk-l1", "rk-l2", "rk-l3"} {
		if !contains(got, want) {
			t.Fatalf("with UV, %s should be authorized, got %v", want, got)
		}
	}
	if contains(got, "rk-other") {
		t.Fatalf("cross-RP credential must never be returned, got %v", got)
	}
}

func TestCredProtect_Level3_AlwaysRequiresUV(t *testing.T) {
	l3 := CredentialInfo{CredentialID: []byte("rk-l3"), RPID: "acme.example", CredProtect: CredProtectUserVerificationRequired}

	withList := AssertionRequest{RPID: "acme.example", AllowList: [][]byte{[]byte("rk-l3")}, UserVerified: false}
	if AssertionAuthorized(l3, withList) {
		t.Fatal("level 3 must not authorize with allowList but no UV")
	}
	noList := AssertionRequest{RPID: "acme.example", UserVerified: false}
	if AssertionAuthorized(l3, noList) {
		t.Fatal("level 3 must not authorize without UV")
	}
	uv := AssertionRequest{RPID: "acme.example", UserVerified: true}
	if !AssertionAuthorized(l3, uv) {
		t.Fatal("level 3 must authorize with valid UV")
	}
}

func TestResolveCredProtect_DefaultAndMinimum(t *testing.T) {
	if got := ResolveCredProtect(0, 0); got != CredProtectUserVerificationOptional {
		t.Fatalf("absent extension must default to level 1, got %d", got)
	}
	if got := ResolveCredProtect(1, 3); got != 3 {
		t.Fatalf("authenticator minimum must clamp policy upward, got %d", got)
	}
	if got := ResolveCredProtect(3, 1); got != 3 {
		t.Fatalf("requested level 3 must be retained, got %d", got)
	}
}
