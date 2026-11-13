package netsec

import (
	"errors"
	"testing"
	"time"
)

const testTenant = "team-a"

func assertTenantEnforced(t *testing.T, selector *VectorSelector) {
	t.Helper()
	matcher, ok := selector.TenantMatcher()
	if !ok {
		t.Fatalf("selector %v has no tenant matcher", selector)
	}
	if matcher.Op != OpEqual || matcher.Value != testTenant {
		t.Fatalf("tenant matcher = %+v, want tenant=%q exact match", matcher, testTenant)
	}
}

func TestRewrite_ReplacesExistingTenantMatchers(t *testing.T) {
	// 攻击者预置的各种 tenant 匹配器都必须被删除，只保留精确匹配。
	matchers := []Matcher{
		{Label: TenantLabel, Op: OpRegex, Value: ".+"},
		{Label: TenantLabel, Op: OpNotEqual, Value: "victim"},
		{Label: TenantLabel, Op: OpNotRegex, Value: "victim"},
		{Label: TenantLabel, Op: OpEqual, Value: "victim"},
		{Label: "job", Op: OpEqual, Value: "api"},
	}
	query := &VectorSelector{Name: "http_requests_total", Matchers: matchers}

	got, err := Rewrite(query, testTenant)
	if err != nil {
		t.Fatalf("Rewrite returned error: %v", err)
	}

	selector := got.(*VectorSelector)
	tenantCount := 0
	for _, matcher := range selector.Matchers {
		if matcher.Label == TenantLabel {
			tenantCount++
			if matcher.Op != OpEqual || matcher.Value != testTenant {
				t.Fatalf("tenant matcher = %+v, want exact %q", matcher, testTenant)
			}
		}
	}
	if tenantCount != 1 {
		t.Fatalf("got %d tenant matchers, want exactly 1", tenantCount)
	}
	if len(selector.Matchers) != 2 {
		t.Fatalf("got %d matchers, want 2 (job + tenant): %+v", len(selector.Matchers), selector.Matchers)
	}
}

func TestRewrite_RangeAndPlainSelectors(t *testing.T) {
	plain := &VectorSelector{Name: "up"}
	ranged := &RangeSelector{
		Vector: &VectorSelector{Name: "http_requests_total"},
		Range:  5 * time.Minute,
	}
	query := &BinaryExpr{Op: "/", LHS: plain, RHS: ranged}

	if _, err := Rewrite(query, testTenant); err != nil {
		t.Fatalf("Rewrite returned error: %v", err)
	}
	assertTenantEnforced(t, plain)
	assertTenantEnforced(t, ranged.Vector)
}

func TestRewrite_SelectorInsideSubquery(t *testing.T) {
	// 藏在子查询里的选择器同样必须被约束。
	inner := &VectorSelector{
		Name:     "http_requests_total",
		Matchers: []Matcher{{Label: TenantLabel, Op: OpRegex, Value: ".+"}},
	}
	query := &Subquery{
		Expr:  &FunctionCall{Name: "rate", Args: []Node{&RangeSelector{Vector: inner, Range: 5 * time.Minute}}},
		Range: time.Hour,
		Step:  time.Minute,
	}

	if _, err := Rewrite(query, testTenant); err != nil {
		t.Fatalf("Rewrite returned error: %v", err)
	}
	assertTenantEnforced(t, inner)
}

func TestRewrite_SelectorInsideNestedFunctionArgs(t *testing.T) {
	deepest := &VectorSelector{Name: "latency_bucket"}
	query := &FunctionCall{
		Name: "histogram_quantile",
		Args: []Node{
			&NumberLiteral{Value: 0.99},
			&FunctionCall{
				Name: "sum",
				Args: []Node{
					&FunctionCall{
						Name: "rate",
						Args: []Node{
							&RangeSelector{Vector: deepest, Range: 5 * time.Minute},
						},
					},
				},
			},
		},
	}

	if _, err := Rewrite(query, testTenant); err != nil {
		t.Fatalf("Rewrite returned error: %v", err)
	}
	assertTenantEnforced(t, deepest)
}

func TestRewrite_EmptyAndBareSelectorsStillConstrained(t *testing.T) {
	// 指标名缺省、匹配器为空或全部可匹配空串的选择器不能被漏掉。
	cases := []*VectorSelector{
		{Matchers: nil},
		{Matchers: []Matcher{}},
		{Matchers: []Matcher{{Label: "job", Op: OpNotEqual, Value: "none"}}},
		{Matchers: []Matcher{{Label: "job", Op: OpRegex, Value: ".*"}}},
	}

	for _, selector := range cases {
		if !selector.MatchesEmpty() {
			t.Fatalf("selector %+v should be treated as matching empty series", selector)
		}
		if _, err := Rewrite(selector, testTenant); err != nil {
			t.Fatalf("Rewrite returned error: %v", err)
		}
		assertTenantEnforced(t, selector)
	}
}

func TestRewrite_RejectsTenantRelabeling(t *testing.T) {
	// label_replace 与 label_join 改写 tenant 标签时必须整条拒绝，
	// 即使参数里还嵌套着选择器也不能改写后放行。
	build := func(name string) Node {
		return &FunctionCall{
			Name: name,
			Args: []Node{
				&VectorSelector{Name: "up"},
				&StringLiteral{Value: TenantLabel},
				&StringLiteral{Value: "$1"},
				&StringLiteral{Value: "job"},
				&StringLiteral{Value: "(.+)"},
			},
		}
	}

	for _, name := range []string{"label_replace", "label_join"} {
		query := build(name)
		if _, err := Rewrite(query, testTenant); !errors.Is(err, ErrTenantLabelForbidden) {
			t.Fatalf("%s target=tenant: err = %v, want ErrTenantLabelForbidden", name, err)
		}
	}
}

func TestRewrite_AllowsRelabelingOtherLabels(t *testing.T) {
	selector := &VectorSelector{Name: "up"}
	query := &FunctionCall{
		Name: "label_replace",
		Args: []Node{
			selector,
			&StringLiteral{Value: "zone"},
			&StringLiteral{Value: "$1"},
			&StringLiteral{Value: "job"},
			&StringLiteral{Value: "(.+)"},
		},
	}

	if _, err := Rewrite(query, testTenant); err != nil {
		t.Fatalf("Rewrite returned error: %v", err)
	}
	assertTenantEnforced(t, selector)
}

func TestRewrite_RejectsTenantInGroupingAndMatchClauses(t *testing.T) {
	selector := func() Node { return &VectorSelector{Name: "up"} }

	cases := map[string]Node{
		"by":         &Aggregation{Op: "sum", Expr: selector(), Grouping: []string{"job", TenantLabel}},
		"without":    &Aggregation{Op: "sum", Expr: selector(), Grouping: []string{TenantLabel}, Without: true},
		"on":         &BinaryExpr{Op: "*", LHS: selector(), RHS: selector(), On: []string{TenantLabel}},
		"ignoring":   &BinaryExpr{Op: "*", LHS: selector(), RHS: selector(), Ignoring: []string{TenantLabel}},
		"group_left": &BinaryExpr{Op: "*", LHS: selector(), RHS: selector(), GroupLeft: []string{TenantLabel}},
	}

	for name, query := range cases {
		if _, err := Rewrite(query, testTenant); !errors.Is(err, ErrTenantLabelForbidden) {
			t.Fatalf("%s clause with tenant: err = %v, want ErrTenantLabelForbidden", name, err)
		}
	}
}

func TestRewrite_PassthroughWithoutVectorSelector(t *testing.T) {
	// 纯数字运算没有任何向量选择器，原样放行且返回同一棵树。
	query := &BinaryExpr{
		Op:  "+",
		LHS: &NumberLiteral{Value: 1},
		RHS: &BinaryExpr{Op: "*", LHS: &NumberLiteral{Value: 2}, RHS: &NumberLiteral{Value: 3}},
	}

	got, err := Rewrite(query, testTenant)
	if err != nil {
		t.Fatalf("Rewrite returned error: %v", err)
	}
	if got != Node(query) {
		t.Fatalf("query without selectors should be returned as-is")
	}

	// 但纯字面量查询里仍不允许操纵 tenant 标签的语义保护列表。
	forbidden := &Aggregation{Op: "sum", Expr: &NumberLiteral{Value: 1}, Grouping: []string{TenantLabel}}
	if _, err := Rewrite(forbidden, testTenant); !errors.Is(err, ErrTenantLabelForbidden) {
		t.Fatalf("by(tenant) on literal: err = %v, want ErrTenantLabelForbidden", err)
	}
}

func TestRewrite_EmptyTenantRejected(t *testing.T) {
	query := &VectorSelector{Name: "up"}
	if _, err := Rewrite(query, ""); !errors.Is(err, ErrEmptyTenant) {
		t.Fatalf("empty tenant: err = %v, want ErrEmptyTenant", err)
	}
}

func TestRewrite_BothBinarySidesConstrained(t *testing.T) {
	// 二元运算两侧的选择器都要注入，防止借右值读到其他租户。
	left := &VectorSelector{Name: "requests"}
	right := &VectorSelector{Name: "capacity"}
	query := &BinaryExpr{
		Op:        "/",
		LHS:       left,
		RHS:       right,
		Ignoring:  []string{"instance"},
		GroupLeft: []string{},
	}

	if _, err := Rewrite(query, testTenant); err != nil {
		t.Fatalf("Rewrite returned error: %v", err)
	}
	assertTenantEnforced(t, left)
	assertTenantEnforced(t, right)
}

func TestMatcher_MatchesEmpty(t *testing.T) {
	cases := []struct {
		matcher Matcher
		want    bool
	}{
		{Matcher{Op: OpEqual, Value: ""}, true},
		{Matcher{Op: OpEqual, Value: "x"}, false},
		{Matcher{Op: OpNotEqual, Value: "x"}, true},
		{Matcher{Op: OpNotEqual, Value: ""}, false},
		{Matcher{Op: OpRegex, Value: ".*"}, true},
		{Matcher{Op: OpRegex, Value: ".+"}, false},
		{Matcher{Op: OpRegex, Value: "a|b?"}, true},
		{Matcher{Op: OpNotRegex, Value: ".+"}, true},
		{Matcher{Op: OpNotRegex, Value: ".*"}, false},
		{Matcher{Op: OpRegex, Value: "["}, false},
		{Matcher{Op: OpNotRegex, Value: "["}, true},
	}
	for _, tc := range cases {
		if got := tc.matcher.MatchesEmpty(); got != tc.want {
			t.Fatalf("%+v MatchesEmpty = %v, want %v", tc.matcher, got, tc.want)
		}
	}
}

func TestRewrite_NilAndEmptySelectorSafe(t *testing.T) {
	got, err := Rewrite(nil, testTenant)
	if err != nil || got != nil {
		t.Fatalf("Rewrite(nil) = (%v, %v), want (nil, nil)", got, err)
	}

	// 范围选择器与子查询内部为空时不应 panic。
	if _, err := Rewrite(&RangeSelector{}, testTenant); err != nil {
		t.Fatalf("empty RangeSelector: %v", err)
	}
	if _, err := Rewrite(&Subquery{}, testTenant); err != nil {
		t.Fatalf("empty Subquery: %v", err)
	}

	// 指标名缺省且没有任何匹配器时 MatchesEmpty 成立，改写后被约束。
	selector := &VectorSelector{}
	if !selector.MatchesEmpty() {
		t.Fatal("empty selector should match empty series before rewrite")
	}
	if _, err := Rewrite(selector, testTenant); err != nil {
		t.Fatalf("Rewrite empty selector: %v", err)
	}
	assertTenantEnforced(t, selector)
}
