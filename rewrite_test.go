package netsec

import (
	"errors"
	"regexp"
	"testing"
	"time"
)

func eq(label, value string) *Matcher {
	return &Matcher{Label: label, Op: OpEqual, Value: value}
}

func neq(label, value string) *Matcher {
	return &Matcher{Label: label, Op: OpNotEqual, Value: value}
}

func re(label, value string) *Matcher {
	return &Matcher{Label: label, Op: OpRegexMatch, Value: value}
}

func nre(label, value string) *Matcher {
	return &Matcher{Label: label, Op: OpRegexNoMatch, Value: value}
}

func tenantMatcher(selector *VectorSelector) []*Matcher {
	var matches []*Matcher
	for _, matcher := range selector.Matchers {
		if matcher.Label == TenantLabel {
			matches = append(matches, matcher)
		}
	}
	return matches
}

func collectSelectors(expr Expr) []*VectorSelector {
	var selectors []*VectorSelector
	walk(expr, func(node Expr) {
		if selector, ok := node.(*VectorSelector); ok {
			selectors = append(selectors, selector)
		}
	})
	return selectors
}

// assertTenantConstrained verifies the selector carries exactly one enforced
// tenant equality matcher for tenantID.
func assertTenantConstrained(t *testing.T, selector *VectorSelector, tenantID string) {
	t.Helper()
	matches := tenantMatcher(selector)
	if len(matches) != 1 {
		t.Fatalf("selector %s: want exactly one tenant matcher, got %d", selector.String(), len(matches))
	}
	if matches[0].Op != OpEqual || matches[0].Value != tenantID {
		t.Fatalf("selector %s: want tenant=%q, got %s", selector.String(), tenantID, matches[0].String())
	}
}

func TestRewriteReplacesExistingTenantMatchers(t *testing.T) {
	// The classic bypass: a regex such as .+ admits every tenant. Equality,
	// negation and regex forms must all be replaced by one exact matcher.
	selectors := []*VectorSelector{
		{Name: "http_requests", Matchers: []*Matcher{re(TenantLabel, ".+"), eq("code", "200")}},
		{Name: "latency", Matchers: []*Matcher{eq(TenantLabel, "victim"), neq("zone", "cn")}},
		{Name: "errors", Matchers: []*Matcher{nre(TenantLabel, "blocked")}},
		{Name: "dupes", Matchers: []*Matcher{
			re(TenantLabel, ".*"),
			neq(TenantLabel, "other"),
			eq(TenantLabel, "victim"),
		}},
	}

	for _, selector := range selectors {
		original := selector.Matchers
		got, err := Rewrite(selector, "acme")
		if err != nil {
			t.Fatalf("Rewrite(%s): %v", selector.String(), err)
		}
		rewritten := got.(*VectorSelector)
		assertTenantConstrained(t, rewritten, "acme")

		// The caller's original matcher slice must be left untouched.
		originalTenantCount := 0
		for _, matcher := range original {
			if matcher.Label == TenantLabel {
				originalTenantCount++
			}
		}
		if originalTenantCount == 0 {
			t.Fatalf("original slice for %s was mutated", selector.String())
		}
	}
}

func TestRewriteCoversSubqueryRangeAndNestedFunctions(t *testing.T) {
	// Selectors hidden inside function arguments, range vectors and subqueries
	// must not escape the enforced constraint.
	inner := &VectorSelector{Name: "cpu", Matchers: []*Matcher{re(TenantLabel, ".+")}}
	rate := &Call{
		Func: "rate",
		Args: []Expr{
			&RangeSelector{Vector: inner, Range: 5 * time.Minute},
		},
	}
	hist := &Call{
		Func: "histogram_quantile",
		Args: []Expr{
			&NumberLiteral{Value: 0.95},
			&SubqueryExpr{Expr: rate, Range: time.Hour, Step: time.Minute},
		},
	}
	topLevel := &VectorSelector{Name: "memory", Matchers: []*Matcher{eq("host", "db-1")}}
	expr := &BinaryExpr{
		Op:  "and",
		LHS: hist,
		RHS: topLevel,
	}

	got, err := Rewrite(expr, "acme")
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}

	selectors := collectSelectors(got)
	if len(selectors) != 2 {
		t.Fatalf("want 2 vector selectors, got %d", len(selectors))
	}
	for _, selector := range selectors {
		assertTenantConstrained(t, selector, "acme")
	}
}

// matchesEmpty reports whether every matcher admits the empty label value.
func matchesEmpty(matchers []*Matcher) bool {
	for _, matcher := range matchers {
		switch matcher.Op {
		case OpEqual:
			if matcher.Value != "" {
				return false
			}
		case OpRegexMatch:
			if !regexp.MustCompile("^(?:" + matcher.Value + ")$").MatchString("") {
				return false
			}
		case OpNotEqual:
			if matcher.Value == "" {
				return false
			}
		case OpRegexNoMatch:
			if regexp.MustCompile("^(?:" + matcher.Value + ")$").MatchString("") {
				return false
			}
		}
	}
	return true
}

func TestRewriteConstrainsLooseAndEmptySelectors(t *testing.T) {
	// Metric-name-less selectors whose matchers all admit the empty string
	// still receive the tenant constraint, as does the bare {} selector.
	loose := &VectorSelector{
		Matchers: []*Matcher{
			eq("code", ""),
			re("path", ".*"),
			neq("env", "prod"),
			nre("region", "cn.*"),
		},
	}
	if !matchesEmpty(loose.Matchers) {
		t.Fatal("test precondition: loose matchers must all match the empty string")
	}
	empty := &VectorSelector{}

	for _, selector := range []*VectorSelector{loose, empty} {
		got, err := Rewrite(selector, "acme")
		if err != nil {
			t.Fatalf("Rewrite(%s): %v", selector.String(), err)
		}
		assertTenantConstrained(t, got.(*VectorSelector), "acme")
	}

	if rendered := empty.String(); rendered != `{tenant="acme"}` {
		t.Fatalf("bare selector rendered as %q, want %q", rendered, `{tenant="acme"}`)
	}
}

func TestRewriteRejectsTenantWritesAndGrouping(t *testing.T) {
	destTenant := func(fn string) Expr {
		return &Call{
			Func: fn,
			Args: []Expr{
				&VectorSelector{Name: "metric"},
				&StringLiteral{Value: TenantLabel},
				&StringLiteral{Value: "value"},
				&StringLiteral{Value: "(.*)"},
			},
		}
	}

	cases := map[string]Expr{
		"label_replace writes tenant": destTenant("label_replace"),
		"label_join writes tenant":    destTenant("label_join"),
		"aggregate by tenant": &AggregateExpr{
			Op: AggSum, Grouping: []string{TenantLabel},
			Expr: &VectorSelector{Name: "metric"},
		},
		"aggregate without tenant": &AggregateExpr{
			Op: AggMax, Without: true, Grouping: []string{"region", TenantLabel},
			Expr: &VectorSelector{Name: "metric"},
		},
		"binary on tenant": &BinaryExpr{
			Op: "*", LHS: &VectorSelector{Name: "a"}, RHS: &VectorSelector{Name: "b"},
			Matching: &VectorMatching{On: []string{TenantLabel}},
		},
		"binary ignoring tenant": &BinaryExpr{
			Op: "/", LHS: &VectorSelector{Name: "a"}, RHS: &VectorSelector{Name: "b"},
			Matching: &VectorMatching{Ignoring: []string{TenantLabel}},
		},
		"binary group_left tenant": &BinaryExpr{
			Op: "*", LHS: &VectorSelector{Name: "a"}, RHS: &VectorSelector{Name: "b"},
			Matching: &VectorMatching{On: []string{"app"}, GroupLeft: []string{TenantLabel}},
		},
		"binary group_right tenant": &BinaryExpr{
			Op: "*", LHS: &VectorSelector{Name: "a"}, RHS: &VectorSelector{Name: "b"},
			Matching: &VectorMatching{On: []string{"app"}, GroupRight: []string{TenantLabel}},
		},
	}

	for name, expr := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Rewrite(expr, "acme")
			var violation *ViolationError
			if !errors.As(err, &violation) {
				t.Fatalf("want ViolationError, got %v", err)
			}

			// Rejected trees must not have been partially rewritten.
			for _, selector := range collectSelectors(expr) {
				for _, matcher := range selector.Matchers {
					if matcher.Label == TenantLabel && matcher.Op == OpEqual && matcher.Value == "acme" {
						t.Fatalf("rejected query was partially rewritten: %s", selector.String())
					}
				}
			}
		})
	}
}

func TestRewriteAllowsSafeLabelFunctionsAndGrouping(t *testing.T) {
	replace := &Call{
		Func: "label_replace",
		Args: []Expr{
			&VectorSelector{Name: "metric", Matchers: []*Matcher{re(TenantLabel, ".+")}},
			&StringLiteral{Value: "service"},
			&StringLiteral{Value: "$1"},
			&StringLiteral{Value: "job"},
			&StringLiteral{Value: "(.*)"},
		},
	}
	agg := &AggregateExpr{
		Op: AggSum, Grouping: []string{"service"},
		Expr: &BinaryExpr{
			Op:  "and",
			LHS: &VectorSelector{Name: "a"},
			RHS: &VectorSelector{Name: "b"},
			Matching: &VectorMatching{
				On:        []string{"service"},
				GroupLeft: []string{"version"},
			},
		},
	}

	for _, expr := range []Expr{replace, agg} {
		got, err := Rewrite(expr, "acme")
		if err != nil {
			t.Fatalf("safe query rejected: %v", err)
		}
		for _, selector := range collectSelectors(got) {
			assertTenantConstrained(t, selector, "acme")
		}
	}
}

func TestRewritePassesThroughSelectorFreeQueries(t *testing.T) {
	// Pure arithmetic over literals contains no vector selector and is
	// returned as-is.
	literal := &NumberLiteral{Value: 42}
	got, err := Rewrite(literal, "acme")
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if got != literal {
		t.Fatalf("selector-free query was not returned unchanged")
	}

	arithmetic := &BinaryExpr{
		Op: "+", LHS: &NumberLiteral{Value: 1}, RHS: &NumberLiteral{Value: 2},
	}
	got, err = Rewrite(arithmetic, "acme")
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if got != arithmetic || len(collectSelectors(got)) != 0 {
		t.Fatalf("arithmetic query altered or scanned as a selector")
	}
}

func TestRewriteRejectsEmptyTenantAndNil(t *testing.T) {
	if _, err := Rewrite(&VectorSelector{Name: "metric"}, ""); err == nil {
		t.Fatal("empty tenant identifier must be rejected")
	}
	got, err := Rewrite(nil, "acme")
	if err != nil || got != nil {
		t.Fatalf("Rewrite(nil) = (%v, %v), want (nil, nil)", got, err)
	}
}

func TestRendering(t *testing.T) {
	selector := &VectorSelector{
		Name:     "http_requests",
		Matchers: []*Matcher{re(TenantLabel, ".+"), eq("code", "200")},
	}
	rewritten, err := Rewrite(selector, "acme")
	if err != nil {
		t.Fatal(err)
	}
	want := `http_requests{code="200", tenant="acme"}`
	if rendered := rewritten.String(); rendered != want {
		t.Fatalf("String() = %q, want %q", rendered, want)
	}

	rangeExpr := &RangeSelector{
		Vector: &VectorSelector{Name: "cpu"},
		Range:  90 * time.Second,
	}
	if rendered := rangeExpr.String(); rendered != "cpu{}[1m30s]" {
		t.Fatalf("range rendering = %q", rendered)
	}
}

func TestStringRenderingAcrossNodes(t *testing.T) {
	cases := map[string]struct {
		expr Expr
		want string
	}{
		"subquery with step": {
			expr: &SubqueryExpr{Expr: &VectorSelector{Name: "cpu"}, Range: time.Hour, Step: time.Minute},
			want: `cpu{}[1h:1m]`,
		},
		"subquery without step": {
			expr: &SubqueryExpr{Expr: &VectorSelector{Name: "cpu"}, Range: time.Hour},
			want: `cpu{}[1h:]`,
		},
		"string literal": {
			expr: &StringLiteral{Value: "tenant"},
			want: `"tenant"`,
		},
		"call": {
			expr: &Call{Func: "rate", Args: []Expr{
				&RangeSelector{Vector: &VectorSelector{Name: "cpu"}, Range: 5 * time.Minute},
			}},
			want: "rate(cpu{}[5m])",
		},
		"aggregation with param": {
			expr: &AggregateExpr{
				Op: AggTopk, Param: &NumberLiteral{Value: 3},
				Expr: &VectorSelector{Name: "cpu"},
			},
			want: "topk (3, cpu{})",
		},
		"binary on ignoring group_left": {
			expr: &BinaryExpr{
				Op: "*", LHS: &VectorSelector{Name: "a"}, RHS: &VectorSelector{Name: "b"},
				Matching: &VectorMatching{
					Ignoring:  []string{"code"},
					GroupLeft: []string{"version"},
				},
			},
			want: "(a{} * b{}) ignoring (code) group_left (version)",
		},
		"binary one-to-one": {
			expr: &BinaryExpr{
				Op: "+", LHS: &NumberLiteral{Value: 1}, RHS: &NumberLiteral{Value: 2},
			},
			want: "(1 + 2)",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := tc.expr.String(); got != tc.want {
				t.Fatalf("String() = %q, want %q", got, tc.want)
			}
		})
	}

	if rendered := formatDuration(1500 * time.Millisecond); rendered != "1s500ms" {
		t.Fatalf("duration = %q", rendered)
	}
	if rendered := formatDuration(0); rendered != "0s" {
		t.Fatalf("zero duration = %q", rendered)
	}
}

type fakeExpr struct{}

func (*fakeExpr) astNode()       {}
func (*fakeExpr) String() string { return "fake" }

func TestUnsupportedExpressionPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("unsupported expression type must panic to fail closed")
		}
	}()
	walk(&fakeExpr{}, func(Expr) {})
}

func TestEdgeCases(t *testing.T) {
	if rendered := (&ViolationError{Reason: "boom"}).Error(); rendered != "tenant rewrite refused: boom" {
		t.Fatalf("error rendering = %q", rendered)
	}
	if rendered := (MatchOp(99)).String(); rendered != "?" {
		t.Fatalf("unknown operator rendering = %q", rendered)
	}
	if rendered := (*Matcher)(nil).String(); rendered != "" {
		t.Fatalf("nil matcher rendering = %q", rendered)
	}
}
