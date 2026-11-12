package netsec_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"example.org/netsec/q0403"
)

func TestValidateResource(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"https://api.example.com/",
		"https://api.example.com/v1",
		"urn:example:resource-server",
		"https://api.example.com/path?query=value",
	} {
		if err := netsec.ValidateResource(value); err != nil {
			t.Errorf("ValidateResource(%q) = %v, want nil", value, err)
		}
	}

	for _, value := range []string{
		"",
		"/relative/path",
		"api.example.com",
		"https://api.example.com/#fragment",
		"https://api.example.com/#",
		"https://api.example.com/ path",
		"https://api.example.com/%zz",
		"https://[::1",
	} {
		err := netsec.ValidateResource(value)
		if !netsec.IsInvalidTarget(err) {
			t.Errorf("ValidateResource(%q) = %v, want invalid_target", value, err)
		}
	}
}

func TestParseResourcesDeduplicatesWithoutNormalizing(t *testing.T) {
	t.Parallel()

	input := []string{
		"https://api.example.com/",
		"https://api.example.com",
		"https://api.example.com/",
	}
	got, err := netsec.ParseResources(input)
	if err != nil {
		t.Fatalf("ParseResources() error = %v", err)
	}
	want := []string{"https://api.example.com/", "https://api.example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseResources() = %#v, want %#v", got, want)
	}
}

func TestAudienceJSONAndExactMatching(t *testing.T) {
	t.Parallel()

	one, err := json.Marshal(netsec.NewAudience([]string{"https://api.example.com/"}))
	if err != nil {
		t.Fatalf("marshal one audience: %v", err)
	}
	if string(one) != `"https://api.example.com/"` {
		t.Fatalf("one-element audience JSON = %s", one)
	}

	many, err := json.Marshal(netsec.NewAudience([]string{
		"https://api.example.com/",
		"https://files.example.com/",
	}))
	if err != nil {
		t.Fatalf("marshal many audience: %v", err)
	}
	if string(many) != `["https://api.example.com/","https://files.example.com/"]` {
		t.Fatalf("multi-element audience JSON = %s", many)
	}

	var decoded netsec.Audience
	if err := json.Unmarshal([]byte(`"https://api.example.com/"`), &decoded); err != nil {
		t.Fatalf("unmarshal string audience: %v", err)
	}
	if !decoded.Contains("https://api.example.com/") {
		t.Fatalf("decoded audience does not contain exact resource")
	}

	aud := netsec.NewAudience([]string{"https://api.example.com/"})
	if !netsec.ResourceServerAccepts("https://api.example.com/", aud) {
		t.Fatal("exact resource server identifier was rejected")
	}
	for _, lookalike := range []string{
		"https://api.example.com",
		"https://api.example.com/v1",
		"https://api.example.com.evil/",
		"https://api.example.com/?x=1",
	} {
		if netsec.ResourceServerAccepts(lookalike, aud) {
			t.Errorf("resource server %q accepted a non-exact audience", lookalike)
		}
	}
}

func TestScopeIntersection(t *testing.T) {
	t.Parallel()

	got := netsec.ScopeIntersection(
		[]string{"read", "write", "admin"},
		[]string{"write", "read"},
		[]string{"read", "write", "delete"},
	)
	want := []string{"read", "write"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ScopeIntersection() = %#v, want %#v", got, want)
	}
}

func TestErrorCode(t *testing.T) {
	t.Parallel()

	err := &netsec.Error{Code: netsec.InvalidTarget, Description: "test"}
	var oauthErr *netsec.Error
	if !errors.As(err, &oauthErr) || oauthErr.Code != netsec.InvalidTarget {
		t.Fatalf("error does not retain invalid_target code: %v", err)
	}
	if !errors.Is(err, netsec.ErrInvalidTarget) {
		t.Fatalf("errors.Is(err, ErrInvalidTarget) = false for %v", err)
	}
}
