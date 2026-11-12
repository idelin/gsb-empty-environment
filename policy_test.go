package netsec

import "testing"

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern string
		value   string
		want    bool
	}{
		{"github", "github", true},
		{"github", "gitlab", false},
		{"*", "anything", true},
		{"*", "", true},
		{"com.*", "com.acme", true},
		{"com.*", "org.acme", false},
		{"*.so", "lib.so", true},
		{"*.so", "lib.sox", false},
		{"a*z", "abcz", true},
		{"a*z", "abc", false},
	}
	for _, tc := range cases {
		if got := globMatch(tc.pattern, tc.value); got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tc.pattern, tc.value, got, tc.want)
		}
	}
}

var testPolicies = []Policy{
	{
		ID:       "ban-github-evil",
		Severity: SeverityFAIL,
		Operator: OpALL,
		Conditions: []Condition{
			{Field: FieldType, Pattern: "github"},
			{Field: FieldNamespace, Pattern: "acme/corp"},
			{Field: FieldName, Pattern: "evil/pkg"},
		},
	},
	{
		ID:       "warn-vcs-hosts",
		Severity: SeverityWARN,
		Operator: OpANY,
		Conditions: []Condition{
			{Field: FieldType, Pattern: "github"},
			{Field: FieldType, Pattern: "bitbucket"},
		},
	},
	{
		ID:       "info-linux-classifier",
		Severity: SeverityINFO,
		Operator: OpALL,
		Conditions: []Condition{
			{Field: "qualifiers:classifier", Pattern: "linux*"},
		},
	},
}

func TestCheckComponentCaseVariantEvadesExactMatch(t *testing.T) {
	// Uppercase type/namespace/name would dodge a case-sensitive exact
	// match; normalization makes the FAIL policy hit.
	finding, err := CheckComponent("pkg:GITHUB/Acme/Corp/Evil%2fPkg@v1", testPolicies)
	if err != nil {
		t.Fatal(err)
	}
	if finding == nil {
		t.Fatal("expected FAIL finding, got nil")
	}
	if finding.Severity != SeverityFAIL || finding.PolicyID != "ban-github-evil" {
		t.Fatalf("finding = %+v", finding)
	}
}

func TestCheckComponentDecodesBeforeCompare(t *testing.T) {
	// "%2F" decodes to "/" and the decoded name matches the literal
	// pattern; the same pattern written encoded works too.
	encodedName := "pkg:github/acme/corp/Evil%2fPkg"
	finding, err := CheckComponent(encodedName, testPolicies)
	if err != nil {
		t.Fatal(err)
	}
	if finding == nil || finding.Severity != SeverityFAIL {
		t.Fatalf("expected FAIL for encoded name, got %+v", finding)
	}

	encodedPattern := []Policy{{
		ID:       "encoded",
		Severity: SeverityFAIL,
		Operator: OpALL,
		Conditions: []Condition{
			{Field: FieldName, Pattern: "evil%2Fpkg"},
		},
	}}
	finding, err = CheckComponent(encodedName, encodedPattern)
	if err != nil {
		t.Fatal(err)
	}
	if finding == nil {
		t.Fatal("encoded pattern should match decoded field")
	}
}

func TestCheckComponentQualifierOrderIrrelevant(t *testing.T) {
	first := "pkg:maven/com.acme/lib@1?classifier=linux-x86&arch=arm"
	second := "pkg:maven/com.acme/lib@1?arch=arm&classifier=linux-x86"
	for _, purl := range []string{first, second} {
		finding, err := CheckComponent(purl, testPolicies)
		if err != nil {
			t.Fatal(err)
		}
		if finding == nil || finding.Severity != SeverityINFO {
			t.Fatalf("purl %q: expected INFO finding, got %+v", purl, finding)
		}
	}
}

func TestCheckComponentMavenCaseSensitive(t *testing.T) {
	policies := []Policy{{
		ID:       "maven-upper",
		Severity: SeverityFAIL,
		Operator: OpALL,
		Conditions: []Condition{
			{Field: FieldType, Pattern: "maven"},
			{Field: FieldNamespace, Pattern: "com.acme"},
			{Field: FieldName, Pattern: "Foo"},
		},
	}}

	finding, err := CheckComponent("pkg:maven/com.acme/Foo@1.0", policies)
	if err != nil {
		t.Fatal(err)
	}
	if finding == nil {
		t.Fatal("exact-case maven component should match")
	}

	finding, err = CheckComponent("pkg:maven/com.acme/foo@1.0", policies)
	if err != nil {
		t.Fatal(err)
	}
	if finding != nil {
		t.Fatalf("lowercase maven variant must not match, got %+v", finding)
	}
}

func TestCheckComponentMiss(t *testing.T) {
	// github type would hit the WARN ANY-policy; use a fully non-matching
	// component and a github component outside the banned namespace/name.
	finding, err := CheckComponent("pkg:npm/acme/lib", testPolicies)
	if err != nil {
		t.Fatal(err)
	}
	if finding != nil {
		t.Fatalf("expected no finding for npm component, got %+v", finding)
	}

	finding, err = CheckComponent("pkg:github/other-org/other-pkg", testPolicies)
	if err != nil {
		t.Fatal(err)
	}
	// The github type still matches the WARN ANY-policy, but the FAIL
	// ban must not fire.
	if finding == nil || finding.Severity != SeverityWARN {
		t.Fatalf("expected only WARN for non-banned github component, got %+v", finding)
	}
}

func TestCheckComponentSubpathNotMatched(t *testing.T) {
	// No condition references subpath, so two components that differ only
	// by subpath produce the same result.
	without := "pkg:github/acme/corp/evil%2fpkg"
	with := without + "#vendor/embedded/evil/pkg"
	for _, purl := range []string{without, with} {
		finding, err := CheckComponent(purl, testPolicies)
		if err != nil {
			t.Fatal(err)
		}
		if finding == nil || finding.Severity != SeverityFAIL {
			t.Fatalf("purl %q: expected FAIL regardless of subpath, got %+v", purl, finding)
		}
	}
}

func TestHighestSeverityWins(t *testing.T) {
	// The banned github component hits FAIL, WARN and (with qualifier)
	// INFO policies; FAIL must win.
	purl := "pkg:github/acme/corp/evil%2fpkg@v1?classifier=linux-arm"
	finding, err := CheckComponent(purl, testPolicies)
	if err != nil {
		t.Fatal(err)
	}
	if finding == nil || finding.Severity != SeverityFAIL {
		t.Fatalf("expected FAIL, got %+v", finding)
	}

	// A non-banned github component only hits WARN and INFO; WARN wins.
	finding, err = CheckComponent("pkg:github/acme/other?classifier=linux-x86", testPolicies)
	if err != nil {
		t.Fatal(err)
	}
	if finding == nil || finding.Severity != SeverityWARN {
		t.Fatalf("expected WARN, got %+v", finding)
	}
}

func TestALLAndANYOperators(t *testing.T) {
	all := Policy{
		ID: "all", Severity: SeverityFAIL, Operator: OpALL,
		Conditions: []Condition{
			{Field: FieldType, Pattern: "maven"},
			{Field: FieldName, Pattern: "lib"},
		},
	}
	any := Policy{
		ID: "any", Severity: SeverityFAIL, Operator: OpANY,
		Conditions: all.Conditions,
	}
	p, err := Parse("pkg:npm/acme/lib")
	if err != nil {
		t.Fatal(err)
	}
	p.Normalize()

	matched, err := all.Match(p)
	if err != nil || matched {
		t.Fatalf("ALL should fail on mismatched type, got matched=%v err=%v", matched, err)
	}
	matched, err = any.Match(p)
	if err != nil || !matched {
		t.Fatalf("ANY should pass on matching name, got matched=%v err=%v", matched, err)
	}

	// Empty condition sets never match.
	empty := Policy{ID: "empty", Severity: SeverityINFO, Operator: OpALL}
	if matched, err := empty.Match(p); err != nil || matched {
		t.Fatalf("empty policy should not match, got matched=%v err=%v", matched, err)
	}
}

func TestWildcardAndMissingField(t *testing.T) {
	policies := []Policy{{
		ID: "wild", Severity: SeverityWARN, Operator: OpALL,
		Conditions: []Condition{
			{Field: FieldType, Pattern: "*"},
			{Field: FieldVersion, Pattern: "*"},
		},
	}}

	// A component without version: "*" matches the empty field too.
	finding, err := CheckComponent("pkg:npm/acme/lib", policies)
	if err != nil {
		t.Fatal(err)
	}
	if finding == nil {
		t.Fatal("wildcard should match missing/empty version field")
	}
}

func TestValidateRejectsBadPolicy(t *testing.T) {
	bad := []Policy{
		{ID: "sev", Severity: "BOGUS", Operator: OpALL},
		{ID: "op", Severity: SeverityINFO, Operator: "OR"},
		{ID: "field", Severity: SeverityINFO, Operator: OpALL,
			Conditions: []Condition{{Field: "unknown", Pattern: "x"}}},
		{ID: "pattern", Severity: SeverityINFO, Operator: OpALL,
			Conditions: []Condition{{Field: FieldName, Pattern: ""}}},
	}
	for _, policy := range bad {
		if err := policy.Validate(); err == nil {
			t.Errorf("policy %q expected validation error", policy.ID)
		}
	}
}
