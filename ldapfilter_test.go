package ldapfilter

import (
	"reflect"
	"strings"
	"testing"
)

func TestEscapeValue(t *testing.T) {
	cases := map[string]string{
		"plain":             "plain",
		"a*b":               "a\\2ab",
		"(cn=x)":            "\\28cn=x\\29",
		"back\\slash":       "back\\5cslash",
		"mix*()\\end":       "mix\\2a\\28\\29\\5cend",
		"nul\x00byte":       "nul\\00byte",
		"héllo":             "héllo",
		"":                  "",
		"2a":                "2a",
		"*":                 "\\2a",
		"(":                 "\\28",
		")":                 "\\29",
		"\\":                "\\5c",
		"star*):(\\nul\x00": "star\\2a\\29:\\28\\5cnul\\00",
	}
	for input, want := range cases {
		if got := EscapeValue(input); got != want {
			t.Errorf("EscapeValue(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestBuild(t *testing.T) {
	eq, _ := NewEquality("cn", "alice")
	present, _ := NewPresent("member")
	approx, _ := NewApprox("sn", "smith")
	gte, _ := NewGreaterOrEqual("uidNumber", "1000")
	lte, _ := NewLessOrEqual("uidNumber", "2000")
	sub, _ := NewSubstrings("mail", "a", []string{"b", "c"}, "d")
	ext1, _ := NewExtensible("cn", "", true, "alice")
	ext2, _ := NewExtensible("", "2.5.13.5", false, "smith")
	ext3, _ := NewExtensible("sn", "caseExactMatch", true, "smith")
	and, _ := NewAnd(eq, present)
	or, _ := NewOr(gte, lte)
	not, _ := NewNot(approx)

	cases := []struct {
		filter Filter
		want   string
	}{
		{eq, "(cn=alice)"},
		{present, "(member=*)"},
		{approx, "(sn~=smith)"},
		{gte, "(uidNumber>=1000)"},
		{lte, "(uidNumber<=2000)"},
		{sub, "(mail=a*b*c*d)"},
		{ext1, "(cn:dn:=alice)"},
		{ext2, "(:2.5.13.5:=smith)"},
		{ext3, "(sn:dn:caseExactMatch:=smith)"},
		{and, "(&(cn=alice)(member=*))"},
		{or, "(|(uidNumber>=1000)(uidNumber<=2000))"},
		{not, "(!(sn~=smith))"},
	}
	for _, tc := range cases {
		got, err := Build(tc.filter)
		if err != nil {
			t.Fatalf("Build(%v) returned error: %v", tc.want, err)
		}
		if got != tc.want {
			t.Errorf("Build = %q, want %q", got, tc.want)
		}
		if tc.filter.String() != tc.want {
			t.Errorf("String() = %q, want %q", tc.filter.String(), tc.want)
		}
	}
}

func TestBuildEscapesMetacharacters(t *testing.T) {
	eq, err := NewEquality("cn", "admin)(|(objectClass=*))")
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := Build(eq)
	if err != nil {
		t.Fatal(err)
	}
	want := "(cn=admin\\29\\28|\\28objectClass=\\2a\\29\\29)"
	if rendered != want {
		t.Fatalf("Build = %q, want %q", rendered, want)
	}

	sub, err := NewSubstrings("cn", "a*b", []string{"c(d", "e)f"}, "g\\h")
	if err != nil {
		t.Fatal(err)
	}
	rendered, err = Build(sub)
	if err != nil {
		t.Fatal(err)
	}
	want = "(cn=a\\2ab*c\\28d*e\\29f*g\\5ch)"
	if rendered != want {
		t.Fatalf("Build = %q, want %q", rendered, want)
	}
}

func TestConstructorValidation(t *testing.T) {
	if _, err := NewAnd(); err == nil {
		t.Error("NewAnd() with no children must error")
	}
	if _, err := NewOr(); err == nil {
		t.Error("NewOr() with no children must error")
	}
	if _, err := NewNot(nil); err == nil {
		t.Error("NewNot(nil) must error")
	}
	if _, err := NewEquality("", "x"); err == nil {
		t.Error("empty attribute must error")
	}
	if _, err := NewEquality("bad name", "x"); err == nil {
		t.Error("attribute with space must error")
	}
	if _, err := NewPresent("1.2."); err == nil {
		t.Error("malformed OID must error")
	}
	if _, err := NewExtensible("", "", false, "x"); err == nil {
		t.Error("extensible match without attribute or rule must error")
	}
	if _, err := NewExtensible("cn", "bad rule", false, "x"); err == nil {
		t.Error("invalid matching rule must error")
	}
}

func TestBuildRejectsMalformedTrees(t *testing.T) {
	bad := []Filter{
		nil,
		&AndFilter{},
		&OrFilter{Children: []Filter{nil}},
		&NotFilter{},
		&EqualityFilter{Attribute: "", Value: "x"},
		&PresentFilter{Attribute: "cn="},
		&ExtensibleFilter{Value: "x"},
	}
	for _, filter := range bad {
		if _, err := Build(filter); err == nil {
			t.Errorf("Build(%T) must return an error", filter)
		}
	}
}

func TestParseKnown(t *testing.T) {
	cases := []struct {
		input string
		want  Filter
	}{
		{"(cn=alice)", &EqualityFilter{Attribute: "cn", Value: "alice"}},
		{"(cn=)", &EqualityFilter{Attribute: "cn", Value: ""}},
		{"(member=*)", &PresentFilter{Attribute: "member"}},
		{"(sn~=smith)", &ApproxFilter{Attribute: "sn", Value: "smith"}},
		{"(uidNumber>=1000)", &GreaterOrEqualFilter{Attribute: "uidNumber", Value: "1000"}},
		{"(uidNumber<=2000)", &LessOrEqualFilter{Attribute: "uidNumber", Value: "2000"}},
		{"(cn=a*b*c)", &SubstringsFilter{Attribute: "cn", Initial: "a", Any: []string{"b"}, Final: "c"}},
		{"(cn=*)", &PresentFilter{Attribute: "cn"}},
		{"(cn=**)", &SubstringsFilter{Attribute: "cn", Initial: "", Any: []string{""}, Final: ""}},
		{"(cn=*a*)", &SubstringsFilter{Attribute: "cn", Initial: "", Any: []string{"a"}, Final: ""}},
		{"(cn=a*)", &SubstringsFilter{Attribute: "cn", Initial: "a", Final: ""}},
		{"(cn=*a)", &SubstringsFilter{Attribute: "cn", Initial: "", Final: "a"}},
		{"(cn=\\2a)", &EqualityFilter{Attribute: "cn", Value: "*"}},
		{"(cn=a\\2ab)", &EqualityFilter{Attribute: "cn", Value: "a*b"}},
		{"(cn=\\28\\29)", &EqualityFilter{Attribute: "cn", Value: "()"}},
		{"(cn=\\5c)", &EqualityFilter{Attribute: "cn", Value: "\\"}},
		{"(cn=\\00)", &EqualityFilter{Attribute: "cn", Value: "\x00"}},
		{"(cn=\\2A)", &EqualityFilter{Attribute: "cn", Value: "*"}},
		{"(cn=héllo)", &EqualityFilter{Attribute: "cn", Value: "héllo"}},
		{"(2.5.4.3=alice)", &EqualityFilter{Attribute: "2.5.4.3", Value: "alice"}},
		{"(cn;lang-en=alice)", &EqualityFilter{Attribute: "cn;lang-en", Value: "alice"}},
		{"(cn:dn:=alice)", &ExtensibleFilter{Attribute: "cn", DNAttributes: true, Value: "alice"}},
		{"(cn:caseExactMatch:=alice)", &ExtensibleFilter{Attribute: "cn", MatchingRule: "caseExactMatch", Value: "alice"}},
		{"(cn:dn:2.5.13.5:=alice)", &ExtensibleFilter{Attribute: "cn", MatchingRule: "2.5.13.5", DNAttributes: true, Value: "alice"}},
		{"(:2.5.13.5:=alice)", &ExtensibleFilter{MatchingRule: "2.5.13.5", Value: "alice"}},
	}

	for _, tc := range cases {
		got, err := Parse(tc.input)
		if err != nil {
			t.Errorf("Parse(%q) error: %v", tc.input, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Parse(%q) = %#v, want %#v", tc.input, got, tc.want)
		}
	}

	nested := "(&(cn=alice)(|(sn~=smith)(member=*))(!(uidNumber<=9)))"
	filter, err := Parse(nested)
	if err != nil {
		t.Fatalf("Parse(%q) error: %v", nested, err)
	}
	if filter.String() != nested {
		t.Fatalf("nested round trip = %q, want %q", filter.String(), nested)
	}
	and, ok := filter.(*AndFilter)
	if !ok || len(and.Children) != 3 {
		t.Fatalf("nested filter shape wrong: %#v", filter)
	}
}

func TestParseErrors(t *testing.T) {
	invalid := []string{
		"",
		"cn=alice",
		"(cn=alice",
		"((cn=alice))",
		"(&(cn=alice))trailing",
		"(&)",
		"(|)",
		"(!)",
		"(!(cn=a)(cn=b))",
		"(=alice)",
		"(cn)",
		"(cn=\\)",
		"(cn=\\2)",
		"(cn=\\gg)",
		"(cn=\\2g)",
		"(cn=ab\\",
		"(cn=ab\x00c)",
		"(cn=ab(c)",
		"(cn:dn:dn:=x)",
		"(cn:rule1:rule2:=x)",
		"(cn::=x)",
		"(:dn:=x)",
		"(:=x)",
		"(:dn)",
		"(bad attr=x)",
		"(1.2.=x)",
		"(~=x)",
		"(>=x)",
	}
	for _, input := range invalid {
		filter, err := Parse(input)
		if err == nil {
			t.Errorf("Parse(%q) must error, got %#v", input, filter)
		}
	}
}

func TestParseIdempotent(t *testing.T) {
	input := "(&(cn=a\\2ab)(|(mail=*x*)(!(sn~=s\\28mith))))"
	first, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Parse(first.String())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("re-parse mismatch:\n first  = %#v\n second = %#v", first, second)
	}
}

func TestParseTrailingData(t *testing.T) {
	if _, err := Parse("(cn=a)(cn=b)"); err == nil {
		t.Fatal("multiple top-level filters must be rejected")
	}
	if _, err := Parse("(cn=a) "); err == nil {
		t.Fatal("trailing space must be rejected")
	}
}

func TestSubstringRoundTripEdgeCases(t *testing.T) {
	filters := []*SubstringsFilter{
		{Attribute: "cn", Initial: "a", Any: []string{""}, Final: "b"},
		{Attribute: "cn", Initial: "a", Any: []string{"b", "c", "d"}, Final: "e"},
		{Attribute: "cn", Initial: "*", Any: []string{"(x)", "\\"}, Final: ""},
	}
	for _, filter := range filters {
		rendered, err := Build(filter)
		if err != nil {
			t.Fatalf("Build error: %v", err)
		}
		parsed, err := Parse(rendered)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", rendered, err)
		}
		if !reflect.DeepEqual(parsed, filter) {
			t.Fatalf("round trip mismatch for %q:\n got  %#v\n want %#v", rendered, parsed, filter)
		}
		if !strings.Contains(rendered, "*") {
			t.Fatalf("substring rendering missing wildcard: %q", rendered)
		}
	}
}
