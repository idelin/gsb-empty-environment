package ldapfilter

import (
	"reflect"
	"testing"
)

// injectionPayloads are classic LDAP filter injection attempts. When placed in
// an assertion value they must survive escaping and parsing as literal data.
var injectionPayloads = []string{
	"*)(objectClass=*",
	")(|(objectClass=*",
	"admin)(|(password=*))",
	"*()",
	"((|(cn=*",
	"\\2a\\28\\29",
	"*\x00)",
	"a)(uid=*))(|(uid=*",
	"(&(givenName=*",
	"~=)(>=",
	":dn:=*",
	"*(|(objectClass=*)(userPassword=*",
	")\x00(cn=*",
}

func TestInjectionPayloadsAreLiteral(t *testing.T) {
	for _, payload := range injectionPayloads {
		filter, err := NewEquality("cn", payload)
		if err != nil {
			t.Fatalf("constructor error for %q: %v", payload, err)
		}
		rendered, err := Build(filter)
		if err != nil {
			t.Fatalf("build error for %q: %v", payload, err)
		}

		parsed, err := Parse(rendered)
		if err != nil {
			t.Fatalf("parse error for payload %q (rendered %q): %v", payload, rendered, err)
		}
		eq, ok := parsed.(*EqualityFilter)
		if !ok {
			t.Fatalf("payload %q parsed as %T, want *EqualityFilter", payload, parsed)
		}
		if eq.Attribute != "cn" {
			t.Fatalf("payload %q altered attribute to %q", payload, eq.Attribute)
		}
		if eq.Value != payload {
			t.Fatalf("payload %q decoded as %q", payload, eq.Value)
		}

		// No structural metacharacter may appear unescaped in the value span.
		valueSpan := rendered[len("(cn=") : len(rendered)-1]
		for _, ch := range []byte{'(', ')', '*'} {
			if containsRawByte(valueSpan, ch) {
				t.Fatalf("rendered value %q contains unescaped %q for payload %q", valueSpan, ch, payload)
			}
		}
	}
}

func TestInjectionPayloadsInSubstrings(t *testing.T) {
	for _, payload := range injectionPayloads {
		filter, err := NewSubstrings("cn", payload, []string{payload, payload}, payload)
		if err != nil {
			t.Fatalf("constructor error for %q: %v", payload, err)
		}
		rendered, err := Build(filter)
		if err != nil {
			t.Fatalf("build error for %q: %v", payload, err)
		}
		parsed, err := Parse(rendered)
		if err != nil {
			t.Fatalf("parse error for payload %q (rendered %q): %v", payload, rendered, err)
		}
		got, ok := parsed.(*SubstringsFilter)
		if !ok {
			t.Fatalf("payload %q parsed as %T, want *SubstringsFilter", payload, parsed)
		}
		want := &SubstringsFilter{
			Attribute: "cn",
			Initial:   payload,
			Any:       []string{payload, payload},
			Final:     payload,
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("substring payload %q round trip mismatch:\n got  %#v\n want %#v", payload, got, want)
		}
	}
}

func TestInjectionPayloadsInExtensible(t *testing.T) {
	for _, payload := range injectionPayloads {
		filter, err := NewExtensible("cn", "caseExactMatch", true, payload)
		if err != nil {
			t.Fatalf("constructor error for %q: %v", payload, err)
		}
		rendered, err := Build(filter)
		if err != nil {
			t.Fatalf("build error for %q: %v", payload, err)
		}
		parsed, err := Parse(rendered)
		if err != nil {
			t.Fatalf("parse error for payload %q (rendered %q): %v", payload, rendered, err)
		}
		ext, ok := parsed.(*ExtensibleFilter)
		if !ok {
			t.Fatalf("payload %q parsed as %T, want *ExtensibleFilter", payload, parsed)
		}
		if ext.Value != payload {
			t.Fatalf("payload %q decoded as %q", payload, ext.Value)
		}
		if !ext.DNAttributes || ext.MatchingRule != "caseExactMatch" || ext.Attribute != "cn" {
			t.Fatalf("payload %q altered extensible header: %#v", payload, ext)
		}
	}
}

// TestInjectionRenderedIsSingleFilter guarantees the rendered filter cannot be
// read as a multi-component structure: a second top-level filter is rejected.
func TestInjectionRenderedIsSingleFilter(t *testing.T) {
	payload := "x)(objectClass=*"
	filter, err := NewEquality("cn", payload)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := Build(filter)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(rendered)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := parsed.(*EqualityFilter); !ok {
		t.Fatalf("injection parsed as %T", parsed)
	}
}

func containsRawByte(value string, target byte) bool {
	for index := 0; index < len(value); index++ {
		if value[index] == '\\' {
			index += 2
			continue
		}
		if value[index] == target {
			return true
		}
	}
	return false
}
