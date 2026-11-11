package ldapfilter_test

import (
	"testing"

	ldapfilter "example.org/ldapfilter"
)

func TestExtraParseErrors(t *testing.T) {
	invalid := []string{
		"(cn~a)",       // '~' not followed by '='
		"(cn>a)",       // '>' not followed by '='
		"(cn<a)",       // '<' not followed by '='
		"((cn=a))",     // nested parentheses without an operator
		"(!(cn=a)",     // unclosed not
		"(&(cn=a)",     // unclosed and
		"(|(cn=a)",     // unclosed or
		"(cn=abc(def)", // raw '(' inside value
		"(cn~=a*b)",    // wildcard in approximate match
		"(cn>=a*b)",    // wildcard in ordering match
		"(cn:dn:2.5.13.5=a*b)",
		"(~=x)", // no attribute, no colon
		"(>=x)",
		"(<=x)",
		"(cn;=x)",     // empty attribute option
		"(cn;1bad=x)", // option starting with a digit
		"(=)",
		"()",
		"(cn=a)x", // trailing data
	}
	for _, input := range invalid {
		filter, err := ldapfilter.Parse(input)
		if err == nil {
			t.Errorf("Parse(%q) must error, got %#v", input, filter)
		}
	}
}

func TestPanicOnMalformedString(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("String() on a malformed node must panic")
		}
	}()
	_ = (&ldapfilter.EqualityFilter{Attribute: "", Value: "x"}).String()
}

func TestValidAttributeDescriptions(t *testing.T) {
	valid := []string{
		"cn", "CN", "a", "a-b-c", "x1", "2.5.4.3", "1.2",
		"cn;lang-en;binary", "objectClass",
	}
	for _, attribute := range valid {
		if _, err := ldapfilter.NewPresent(attribute); err != nil {
			t.Errorf("attribute %q rejected: %v", attribute, err)
		}
	}

	invalid := []string{"", "1", "1.2.", ".1.2", "cn;", ";cn", "a b", "cn=dn"}
	for _, attribute := range invalid {
		if _, err := ldapfilter.NewPresent(attribute); err == nil {
			t.Errorf("attribute %q must be rejected", attribute)
		}
	}
}

func TestExtensibleRoundTrip(t *testing.T) {
	filters := []*ldapfilter.ExtensibleFilter{
		{Attribute: "cn", DNAttributes: true, Value: "a*b"},
		{MatchingRule: "2.5.13.5", Value: "x(y)"},
		{Attribute: "sn", MatchingRule: "caseExactMatch", DNAttributes: true, Value: "z\\z"},
	}
	for _, filter := range filters {
		rendered, err := ldapfilter.Build(filter)
		if err != nil {
			t.Fatalf("build error: %v", err)
		}
		parsed, err := ldapfilter.Parse(rendered)
		if err != nil {
			t.Fatalf("parse error for %q: %v", rendered, err)
		}
		if rendered != parsed.String() {
			t.Fatalf("round trip mismatch: %q vs %q", rendered, parsed.String())
		}
	}
}

func TestWildcardRejectedInNonSubstring(t *testing.T) {
	inputs := []string{
		"(cn~=a*)",
		"(cn>=*)",
		"(cn<=a*b)",
		"(cn:dn:=*)",
		"(:1.2.3:=a*b)",
	}
	for _, input := range inputs {
		if _, err := ldapfilter.Parse(input); err == nil {
			t.Errorf("Parse(%q) must reject wildcard", input)
		}
	}
}

func TestEdgeCases(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"(cn=\\2a\\28\\29)", "(cn=\\2a\\28\\29)"},
		{"(cn=a\\2a*b\\28c)", "(cn=a\\2a*b\\28c)"},
		{"(&(cn=a)(cn=b)(cn=c))", "(&(cn=a)(cn=b)(cn=c))"},
		{"(!(!(cn=a)))", "(!(!(cn=a)))"},
		{"(cn=*)", "(cn=*)"},
		{"(cn=***)", "(cn=***)"},
		{"(cn=a**b)", "(cn=a**b)"},
	}
	for _, tc := range cases {
		filter, err := ldapfilter.Parse(tc.input)
		if err != nil {
			t.Errorf("Parse(%q) error: %v", tc.input, err)
			continue
		}
		if filter.String() != tc.want {
			t.Errorf("Parse(%q).String() = %q, want %q", tc.input, filter.String(), tc.want)
		}
		again, err := ldapfilter.Parse(filter.String())
		if err != nil {
			t.Errorf("re-parse(%q) error: %v", tc.input, err)
			continue
		}
		if again.String() != filter.String() {
			t.Errorf("re-parse mismatch for %q", tc.input)
		}
	}
}
