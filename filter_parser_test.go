package scim

import (
	"errors"
	"strings"
	"testing"
)

func mustParseFilterExpr(t *testing.T, input string, opts ...Option) Expr {
	t.Helper()
	e, err := ParseFilter(input, opts...)
	if err != nil {
		t.Fatalf("ParseFilter(%q) unexpected error: %v", input, err)
	}
	return e
}

func TestParseFilter_AllOperators(t *testing.T) {
	binary := []string{"eq", "ne", "co", "sw", "ew", "gt", "ge", "lt", "le"}
	for _, op := range binary {
		e := mustParseFilterExpr(t, `userName `+op+` "bjensen"`)
		ae, ok := e.(*AttrExpr)
		if !ok {
			t.Fatalf("%s: want *AttrExpr, got %T", op, e)
		}
		if string(ae.Op) != op {
			t.Fatalf("want op %s, got %s", op, ae.Op)
		}
	}

	e := mustParseFilterExpr(t, "userName pr")
	if ae, ok := e.(*AttrExpr); !ok || ae.Op != OpPr || ae.Value != nil {
		t.Fatalf("pr expression malformed: %#v", e)
	}

	// 操作符大小写不敏感。
	e = mustParseFilterExpr(t, `userName EQ "bjensen"`)
	if ae := e.(*AttrExpr); ae.Op != OpEq {
		t.Fatalf("EQ should fold to eq, got %s", ae.Op)
	}
}

func TestParseFilter_LiteralKinds(t *testing.T) {
	cases := []struct {
		filter string
		kind   LiteralKind
	}{
		{`active eq true`, LitBool},
		{`active eq false`, LitBool},
		{`count gt 42`, LitNumber},
		{`ratio le -1.5e2`, LitNumber},
		{`manager eq null`, LitNull},
		{`userName eq "bjensen"`, LitString},
	}
	for _, tc := range cases {
		e := mustParseFilterExpr(t, tc.filter)
		ae := e.(*AttrExpr)
		if ae.Value.Kind != tc.kind {
			t.Fatalf("%s: want literal kind %d, got %d", tc.filter, tc.kind, ae.Value.Kind)
		}
	}
}

func TestParseFilter_DottedAndURNPaths(t *testing.T) {
	e := mustParseFilterExpr(t, `name.familyName eq "Jensen"`)
	ae := e.(*AttrExpr)
	if got := strings.Join(ae.Path.Parts, "."); got != "name.familyName" {
		t.Fatalf("dotted path wrong: %v", ae.Path.Parts)
	}

	urn := `urn:ietf:params:scim:schemas:core:2.0:User:userName eq "bjensen"`
	e = mustParseFilterExpr(t, urn)
	ae = e.(*AttrExpr)
	if ae.Path.Schema != "urn:ietf:params:scim:schemas:core:2.0:User" {
		t.Fatalf("URN schema wrong: %q", ae.Path.Schema)
	}
	if ae.Path.Root() != "userName" {
		t.Fatalf("URN attr wrong: %q", ae.Path.Root())
	}
}

func TestParseFilter_LogicalPrecedence(t *testing.T) {
	// and 优先于 or：a or b and c => a or (b and c)
	e := mustParseFilterExpr(t, `a eq "1" or b eq "2" and c eq "3"`)
	or, ok := e.(*LogicalExpr)
	if !ok || or.Op != LogicalOr {
		t.Fatalf("top node should be OR, got %T", e)
	}
	if _, ok := or.Right.(*LogicalExpr); !ok {
		t.Fatalf("right side should be AND grouping, got %T", or.Right)
	}

	// 括号改变优先级。
	e = mustParseFilterExpr(t, `(a eq "1" or b eq "2") and c eq "3"`)
	and, ok := e.(*LogicalExpr)
	if !ok || and.Op != LogicalAnd {
		t.Fatalf("top node should be AND, got %T", e)
	}
	if _, ok := and.Left.(*LogicalExpr); !ok {
		t.Fatalf("left side should be OR group, got %T", and.Left)
	}

	// not 前缀。
	e = mustParseFilterExpr(t, `not (userName eq "bjensen")`)
	n, ok := e.(*NotExpr)
	if !ok {
		t.Fatalf("want *NotExpr, got %T", e)
	}
	if _, ok := n.Inner.(*AttrExpr); !ok {
		t.Fatalf("not inner should be AttrExpr, got %T", n.Inner)
	}
}

func TestParseFilter_ValuePath(t *testing.T) {
	raw := `emails[type eq "work" and value co "example.com"]`
	e := mustParseFilterExpr(t, raw)
	vp, ok := e.(*ValuePathExpr)
	if !ok {
		t.Fatalf("want ValuePathExpr, got %T", e)
	}
	if vp.Path.Root() != "emails" {
		t.Fatalf("wrong value path attr: %s", vp.Path.Root())
	}
	and, ok := vp.Filter.(*LogicalExpr)
	if !ok || and.Op != LogicalAnd {
		t.Fatalf("inner should be AND expr, got %T", vp.Filter)
	}

	// 值路径内部可以包含复合条件。
	mustParseFilterExpr(t, `emails[type eq "work" and primary eq true]`)
}

func TestParseFilter_RoundTrip(t *testing.T) {
	filters := []string{
		`userName eq "bjensen"`,
		`name.familyName co "O'Malley"`,
		`userType eq "Employee" and (emails co "example.com" or emails.value co "example.org")`,
		`not (active eq false)`,
		`emails[type eq "work" and value co "example.com"]`,
		`meta.lastModified gt "2011-05-13T04:42:34Z"`,
	}
	for _, raw := range filters {
		e := mustParseFilterExpr(t, raw)
		reparsed, err := ParseFilter(e.String())
		if err != nil {
			t.Fatalf("rendered filter %q not parseable: %v", e.String(), err)
		}
		if reparsed.String() != e.String() {
			t.Fatalf("round trip mismatch:\n orig=%s\n render=%s\n again=%s",
				raw, e.String(), reparsed.String())
		}
	}
}

func TestParseFilter_Invalid(t *testing.T) {
	invalid := []string{
		`userName`,                   // 缺操作符
		`userName eq`,                // 缺值
		`userName "bjensen"`,         // 缺操作符
		`userName == "bjensen"`,      // 未知操作符
		`userName eq "x" and`,        // and 缺右值
		`userName eq "x" or or`,      // 连续连接符
		`(userName eq "x"`,           // 缺右括号
		`emails[type eq "work"`,      // 缺右方括号
		`userName eq "x" trailing`,   // 尾部多余 token
		`9userName eq "x"`,           // 非法属性名起始
		`userName eq "unterminated`,  // 未闭合字符串
		`userName eq "x\q"`,          // 非法转义
		`a eq "1" b eq "2"`,          // 缺少 and/or
		`userName eq 'single-quote'`, // SCIM/JSON 只接受双引号
	}
	for _, raw := range invalid {
		_, err := ParseFilter(raw)
		if err == nil {
			t.Fatalf("expected parse error for %q", raw)
		}
		if !errors.Is(err, ErrInvalidFilter) {
			t.Fatalf("%q: error should wrap ErrInvalidFilter, got %v", raw, err)
		}
	}
}

func TestParseFilter_MaxDepth(t *testing.T) {
	// not 嵌套：5 个 not => Depth 6。
	deep := strings.Repeat("not ", 5) + `userName eq "x"`
	if _, err := ParseFilter(deep, WithMaxASTDepth(5)); !errors.Is(err, ErrFilterDepth) {
		t.Fatalf("expected ErrFilterDepth for nested not, got %v", err)
	}
	if _, err := ParseFilter(deep, WithMaxASTDepth(8)); err != nil {
		t.Fatalf("depth 6 should fit limit 8, got %v", err)
	}

	// 括号嵌套攻击必须在递归下降阶段被拦截。
	parens := strings.Repeat("(", 200) + `userName eq "x"` + strings.Repeat(")", 200)
	_, err := ParseFilter(parens, WithMaxASTDepth(16))
	if !errors.Is(err, ErrFilterDepth) {
		t.Fatalf("expected depth error for parenthesis bomb, got %v", err)
	}

	// 值路径嵌套同样受限。
	nested := `a[b[c[d[e eq "x"]]]]`
	_, err = ParseFilter(nested, WithMaxASTDepth(2))
	if !errors.Is(err, ErrFilterDepth) {
		t.Fatalf("expected depth error for nested value paths, got %v", err)
	}
}

func TestParseFilter_NodeLimit(t *testing.T) {
	parts := make([]string, 0, 64)
	for i := 0; i < 64; i++ {
		parts = append(parts, `userName pr`)
	}
	raw := strings.Join(parts, " or ")
	_, err := ParseFilter(raw, WithMaxNodes(10), WithMaxASTDepth(100))
	if !errors.Is(err, ErrFilterComplexity) {
		t.Fatalf("expected complexity error, got %v", err)
	}
}
