package scim

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestInjection_ValueRemainsData 验证把恶意输入作为 AST 字面量构造时，
// 引号/OR/括号无法逃逸为语法。
func TestInjection_ValueRemainsData(t *testing.T) {
	payloads := []string{
		`x" or "1" eq "1`,
		`bjensen" or userName pr or "a" eq "b`,
		`" or "" eq "`,
		`\`,
	}
	for _, payload := range payloads {
		path, err := ParseAttrPath("userName")
		if err != nil {
			t.Fatal(err)
		}
		expr := &AttrExpr{Path: path, Op: OpEq, Value: StringLiteral(payload)}
		rendered := expr.String()

		parsed, err := ParseFilter(rendered)
		if err != nil {
			t.Fatalf("escaped filter %q should parse: %v", rendered, err)
		}
		ae, ok := parsed.(*AttrExpr)
		if !ok {
			t.Fatalf("injection %q changed AST shape to %T", payload, parsed)
		}
		if ae.Op != OpEq {
			t.Fatalf("injection changed operator to %s", ae.Op)
		}
		if got := ae.Value.Value.(string); got != payload {
			t.Fatalf("round trip value mismatch: got %q want %q", got, payload)
		}
	}
}

// TestInjection_EscapedQuotesDoNotBreakToken 验证 filter 文本中的转义引号
// 只会成为字符串内容，不会闭合字面量并引入 OR 子句。
func TestInjection_EscapedQuotesDoNotBreakToken(t *testing.T) {
	raw := `userName eq "a\" or roles pr or \"x"`
	expr := mustParseFilterExpr(t, raw)
	ae, ok := expr.(*AttrExpr)
	if !ok {
		t.Fatalf("escaped-quote injection must stay a single comparison, got %T: %s", expr, expr)
	}
	want := `a" or roles pr or "x`
	if ae.Value.Value != want {
		t.Fatalf("decoded value = %q, want %q", ae.Value.Value, want)
	}
}

// TestInjection_ClassicConcatenationContained 模拟宿主曾经用字符串拼接
// 构造 filter 的场景：即使攻击者注入出永真 OR，隐式租户谓词仍把结果
// 限制在调用方租户内，无法枚举全局用户。
func TestInjection_ClassicConcatenationContained(t *testing.T) {
	e := newSecurityFixture(t)
	alice := Caller{TenantID: "tenant-a", SubjectID: "u-alice", TenantAdmin: true}

	// 等价于 fmt.Sprintf(`userName eq "%s"`, input) 且 input 为
	// nobody" or userName pr or userName eq "x 时得到的完整表达式。
	injected := `userName eq "nobody" or userName pr or userName eq "x"`

	secured, err := e.SecureFilter(alice, injected)
	if err != nil {
		t.Fatalf("syntactically valid injection should still be secured: %v", err)
	}
	if !strings.Contains(secured.String(), "tenantId") {
		t.Fatalf("secured filter must bind tenant predicate: %s", secured)
	}

	users, err := e.List(context.Background(), alice, "User", injected)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("tautology injection must return exactly tenant-a's 2 users, got %d", len(users))
	}
	for _, u := range users {
		if u["tenantId"] != "tenant-a" {
			t.Fatalf("tenant isolation broken, leaked resource: %v", u)
		}
	}

	// 同样的注入在无租户绑定时会命中全部 4 个用户，证明谓词确实是安全关键。
	raw := mustParseFilterExpr(t, injected)
	all := e.Store.List("User")
	globalHits := 0
	for _, u := range all {
		if ok, _ := Match(raw, u); ok {
			globalHits++
		}
	}
	if globalHits != 5 {
		t.Fatalf("unsecured tautology should hit all 5 seeded users, got %d", globalHits)
	}
}

// TestInjection_SensitiveClauseRejected 验证注入 roles/groups/externalId
// 等敏感子句时，策略引擎在执行前直接拒收，OR/NOT 嵌套均不能绕过。
func TestInjection_SensitiveClauseRejected(t *testing.T) {
	e := newSecurityFixture(t)
	alice := Caller{TenantID: "tenant-a", SubjectID: "u-alice", TenantAdmin: true}

	attacks := []string{
		`userName eq "x" or roles pr`,
		`userName eq "x" or externalId co "eval-"`,
		`not(not(groups pr))`,
		`emails[type eq "work"] and roles co "admin"`,
		`userName eq "x" and (active eq true or roles.value eq "sysadmin")`,
	}
	for _, raw := range attacks {
		_, err := e.List(context.Background(), alice, "User", raw)
		if err == nil {
			t.Fatalf("expected sensitive-attribute rejection for %q", raw)
		}
		if !errors.Is(err, ErrSensitiveAttribute) {
			t.Fatalf("%q: want ErrSensitiveAttribute, got %v", raw, err)
		}
	}
}

// TestInjection_TenantProbeRejected 禁止外部 filter 直接引用租户键探测他人租户。
func TestInjection_TenantProbeRejected(t *testing.T) {
	e := newSecurityFixture(t)
	alice := Caller{TenantID: "tenant-a", SubjectID: "u-alice", TenantAdmin: true}

	_, err := e.SecureFilter(alice, `tenantId eq "tenant-b"`)
	if !errors.Is(err, ErrProtectedAttribute) {
		t.Fatalf("want ErrProtectedAttribute, got %v", err)
	}
}

// TestInjection_DeepPayloadRejected 验证深度炸弹在解析阶段被拒。
func TestInjection_DeepPayloadRejected(t *testing.T) {
	e := newSecurityFixture(t)
	alice := Caller{TenantID: "tenant-a", SubjectID: "u-alice", TenantAdmin: true}

	payload := strings.Repeat("(", 500) + `userName eq "x"` + strings.Repeat(")", 500)
	_, err := e.List(context.Background(), alice, "User", payload)
	if !errors.Is(err, ErrFilterDepth) {
		t.Fatalf("want ErrFilterDepth, got %v", err)
	}
}
