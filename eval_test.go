package scim

import "testing"

func TestMatch_ComparisonOperators(t *testing.T) {
	r := Resource{
		"userName": "bjensen",
		"age":      float64(34),
		"active":   true,
		"tenantId": "tenant-a",
	}
	cases := []struct {
		filter string
		want   bool
	}{
		{`userName eq "bjensen"`, true},
		{`userName eq "other"`, false},
		{`userName ne "other"`, true},
		{`userName ne "bjensen"`, false},
		{`userName co "jens"`, true},
		{`userName sw "bjen"`, true},
		{`userName ew "sen"`, true},
		{`userName pr`, true},
		{`missing pr`, false},
		{`age gt 30`, true},
		{`age ge 34`, true},
		{`age lt 34`, false},
		{`age le 34`, true},
		{`active eq true`, true},
		{`active ne false`, true},
		{`tenantId eq "tenant-a"`, true},
	}
	for _, tc := range cases {
		expr := mustParseFilterExpr(t, tc.filter)
		got, err := Match(expr, r)
		if err != nil {
			t.Fatalf("%s: %v", tc.filter, err)
		}
		if got != tc.want {
			t.Fatalf("%s => %v, want %v", tc.filter, got, tc.want)
		}
	}
}

func TestMatch_LogicalAndNot(t *testing.T) {
	r := Resource{"userName": "bjensen", "active": true, "type": "Employee"}

	expr := mustParseFilterExpr(t, `active eq true and type eq "Employee"`)
	if ok, _ := Match(expr, r); !ok {
		t.Fatal("expected AND match")
	}
	expr = mustParseFilterExpr(t, `active eq false or userName eq "bjensen"`)
	if ok, _ := Match(expr, r); !ok {
		t.Fatal("expected OR match")
	}
	expr = mustParseFilterExpr(t, `not(active eq false)`)
	if ok, _ := Match(expr, r); !ok {
		t.Fatal("expected NOT match")
	}
	expr = mustParseFilterExpr(t, `not(active eq true)`)
	if ok, _ := Match(expr, r); ok {
		t.Fatal("expected NOT mismatch")
	}
}

func TestMatch_DottedAndValuePath(t *testing.T) {
	r := Resource{
		"name": Resource{"familyName": "Jensen", "givenName": "Barbara"},
		"emails": []any{
			Resource{"type": "work", "value": "bjensen@example.com"},
			Resource{"type": "home", "value": "barbara@home.example"},
		},
	}

	if expr := mustParseFilterExpr(t, `name.familyName eq "Jensen"`); !mustMatchResource(t, expr, r) {
		t.Fatal("dotted path should match")
	}

	work := mustParseFilterExpr(t, `emails[type eq "work"]`)
	if !mustMatchResource(t, work, r) {
		t.Fatal("value path work email should match")
	}

	combined := mustParseFilterExpr(t, `emails[type eq "work" and value ew "example.com"]`)
	if !mustMatchResource(t, combined, r) {
		t.Fatal("work email suffix should match")
	}

	wrongDomain := mustParseFilterExpr(t, `emails[type eq "work" and value ew "home.example"]`)
	if mustMatchResource(t, wrongDomain, r) {
		t.Fatal("work email should not match home domain")
	}

	nested := mustParseFilterExpr(t, `emails[value co "barbara"]`)
	if !mustMatchResource(t, nested, r) {
		t.Fatal("home email substring should match")
	}
}

func mustMatchResource(t *testing.T, expr Expr, r Resource) bool {
	t.Helper()
	ok, err := Match(expr, r)
	if err != nil {
		t.Fatalf("Match error: %v", err)
	}
	return ok
}
