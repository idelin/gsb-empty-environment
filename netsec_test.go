package netsec

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func mustToken(t *testing.T, sources ...string) Token {
	t.Helper()
	token, err := ParseToken(sources...)
	if err != nil {
		t.Fatalf("ParseToken failed: %v", err)
	}
	return token
}

func mustAuthorizer(t *testing.T, src string) Authorizer {
	t.Helper()
	auth, err := ParseAuthorizer(src)
	if err != nil {
		t.Fatalf("ParseAuthorizer failed: %v", err)
	}
	return auth
}

// 场景 1：衰减块直接伪造 right 事实。该事实只能让本块 check 通过，
// 服务端 allow 策略看不到它，因此默认拒绝而非提权成功。
func TestForgeRightFact_Explicit(t *testing.T) {
	authority := `
right("alice", "read", "file1")
check if right("alice", "read", "file1")
`
	attenuation := `
right("alice", "write", "file1")
check if right("alice", "write", "file1")
`
	token := mustToken(t, authority, attenuation)

	forged := mustAuthorizer(t, `allow if right("alice", "write", "file1")`)
	res := Evaluate(token, forged)
	if res.Allowed() {
		t.Fatalf("forged right fact must not grant access, got allow")
	}
	if !errors.Is(res.Reason, ErrNoPolicyMatched) {
		t.Fatalf("expected ErrNoPolicyMatched, got %v", res.Reason)
	}
	if res.Block != -1 {
		t.Fatalf("policy-stage failure must report block -1, got %d", res.Block)
	}

	// authority 块自己的 right 事实仍然在策略集合中，合法路径不受影响。
	legit := mustAuthorizer(t, `allow if right("alice", "read", "file1")`)
	if res := Evaluate(token, legit); !res.Allowed() {
		t.Fatalf("legitimate authority right fact should match, got %+v", res)
	}
}

// 场景 1b：衰减块用与 authority 同名谓词的规则“推导” right 事实。
// 推导结果留在块私有作用域，绝不写回 authority 作用域。
func TestForgeRightFact_RuleDerived(t *testing.T) {
	authority := `
right("alice", "read", "file1")
`
	attenuation := `
grant("write")
right("alice", $a, "file1") :- grant($a)
check if right("alice", "write", "file1")
`
	token := mustToken(t, authority, attenuation)

	forged := mustAuthorizer(t, `allow if right("alice", "write", "file1")`)
	res := Evaluate(token, forged)
	if res.Allowed() || !errors.Is(res.Reason, ErrNoPolicyMatched) {
		t.Fatalf("rule-derived forged right fact must stay out of authority scope, got %+v", res)
	}

	legit := mustAuthorizer(t, `allow if right($u, $a, $r)`)
	res = Evaluate(token, legit)
	if !res.Allowed() {
		t.Fatalf("authority-derived policy should still match, got %+v", res)
	}
	if res.MatchedPolicy != 0 {
		t.Fatalf("expected matched policy 0, got %d", res.MatchedPolicy)
	}
}

// 场景 2：某个块的 check 推不出任何绑定即整体拒绝。
func TestCheckFailure(t *testing.T) {
	authority := `
right("alice", "read", "file1")
`
	attenuation := `
check if admin("bob")
`
	token := mustToken(t, authority, attenuation)
	auth := mustAuthorizer(t, `allow if right($u, $a, $r)`)

	res := Evaluate(token, auth)
	if res.Allowed() {
		t.Fatalf("failing check must deny")
	}
	if !errors.Is(res.Reason, ErrCheckFailed) {
		t.Fatalf("expected ErrCheckFailed, got %v", res.Reason)
	}
	if res.Block != 1 {
		t.Fatalf("expected failing block index 1, got %d", res.Block)
	}
}

// authority 块自己的 check 失败也要被拦截，块索引为 0。
func TestCheckFailure_AuthorityBlock(t *testing.T) {
	token := mustToken(t, `check if missing("x")`)
	auth := mustAuthorizer(t, `allow if true()`)

	res := Evaluate(token, auth)
	if res.Allowed() || !errors.Is(res.Reason, ErrCheckFailed) || res.Block != 0 {
		t.Fatalf("authority check failure expected, got %+v", res)
	}
}

// 场景 3：allow 与 deny 同时匹配时，按书写顺序第一条匹配者决定。
func TestDenyFirstMatchWins(t *testing.T) {
	token := mustToken(t, `right("alice", "read", "file1")`)

	denyFirst := mustAuthorizer(t, `
deny if right($u, "read", $r)
allow if right($u, $a, $r)
`)
	res := Evaluate(token, denyFirst)
	if res.Allowed() {
		t.Fatalf("deny listed first must win")
	}
	if !errors.Is(res.Reason, ErrPolicyDenied) || res.MatchedPolicy != 0 {
		t.Fatalf("expected deny policy 0, got %+v", res)
	}

	allowFirst := mustAuthorizer(t, `
allow if right($u, $a, $r)
deny if right($u, "read", $r)
`)
	res = Evaluate(token, allowFirst)
	if !res.Allowed() || res.MatchedPolicy != 0 {
		t.Fatalf("allow listed first must win, got %+v", res)
	}
}

// 场景 4：规则迭代超过 100 轮仍未不动点时拒绝；短链正常收敛。
func TestFixedPointLimit(t *testing.T) {
	longChain := buildChain(120)
	token := mustToken(t, ``, longChain)
	auth := mustAuthorizer(t, `allow if path("n0", $z)`)

	res := Evaluate(token, auth)
	if res.Allowed() {
		t.Fatalf("non-converging rules must deny")
	}
	if !errors.Is(res.Reason, ErrFixedPointLimit) {
		t.Fatalf("expected ErrFixedPointLimit, got %v", res.Reason)
	}
	if res.Block != 1 {
		t.Fatalf("expected overflow block index 1, got %d", res.Block)
	}

	// 短链放在 authority 块：推导结果属于 authority 作用域，策略可见且正常收敛。
	shortToken := mustToken(t, buildChain(10))
	if res := Evaluate(shortToken, auth); !res.Allowed() {
		t.Fatalf("short chain should converge and match, got %+v", res)
	}
}

func buildChain(nodes int) string {
	var b strings.Builder
	for i := 0; i+1 < nodes; i++ {
		fmt.Fprintf(&b, "edge(\"n%d\", \"n%d\")\n", i, i+1)
	}
	b.WriteString(`path($x, $z) :- edge($x, $z)
path($x, $z) :- edge($x, $y), path($y, $z)
check if path("n0", "n` + fmt.Sprint(nodes-1) + `")
`)
	return b.String()
}

// 场景 5：变量绑定，包括多谓词连接、重复变量一致性与常量过滤。
func TestVariableBinding(t *testing.T) {
	authority := `
parent("a", "b")
parent("b", "c")
grandparent($x, $z) :- parent($x, $y), parent($y, $z)
check if grandparent("a", "c")
`
	token := mustToken(t, authority)
	auth := mustAuthorizer(t, `allow if grandparent($x, $z)`)
	if res := Evaluate(token, auth); !res.Allowed() {
		t.Fatalf("joined variable binding should derive grandparent, got %+v", res)
	}

	// 常量参数参与过滤，只有 age 第二参数为 30 的对象满足。
	filtered := mustToken(t, `
age("alice", 30)
age("bob", 17)
adult($x) :- age($x, 30)
check if adult("alice")
`)
	if res := Evaluate(filtered, mustAuthorizer(t, `allow if adult($x)`)); !res.Allowed() {
		t.Fatalf("constant-filtered binding should match alice, got %+v", res)
	}

	noMatch := mustToken(t, `
age("bob", 17)
adult($x) :- age($x, 30)
check if adult("bob")
`)
	res := Evaluate(noMatch, mustAuthorizer(t, `allow if adult($x)`))
	if !errors.Is(res.Reason, ErrCheckFailed) {
		t.Fatalf("non-matching constant filter should fail check, got %+v", res)
	}
}

// 重复变量要求同一变量在所有位置绑定到同一个常量。
func TestRepeatedVariableJoinsOnEquality(t *testing.T) {
	token := mustToken(t, `
node("n1")
same($x, $x) :- node($x)
check if same("n1", "n1")
`)
	auth := mustAuthorizer(t, `allow if same($x, $x)`)
	if res := Evaluate(token, auth); !res.Allowed() {
		t.Fatalf("repeated variable should unify to the same value, got %+v", res)
	}

	bad := mustToken(t, `
node("n1")
same($x, $x) :- node($x)
check if same("n1", "n2")
`)
	if res := Evaluate(bad, auth); !errors.Is(res.Reason, ErrCheckFailed) {
		t.Fatalf("distinct constants must not satisfy repeated variable, got %+v", res)
	}
}

// 没有任何策略匹配时默认拒绝；authority 规则推导结果对策略可见。
func TestNoPolicyMatchedAndAuthorityDerivation(t *testing.T) {
	token := mustToken(t, `
edge("a", "b")
reachable($x, $y) :- edge($x, $y)
`)
	res := Evaluate(token, Authorizer{})
	if res.Allowed() || !errors.Is(res.Reason, ErrNoPolicyMatched) {
		t.Fatalf("empty authorizer must default-deny, got %+v", res)
	}

	auth := mustAuthorizer(t, `allow if reachable("a", $y)`)
	if res := Evaluate(token, auth); !res.Allowed() {
		t.Fatalf("authority-derived fact must be visible to policies, got %+v", res)
	}
}

// 环境事实（如请求资源）只在策略阶段参与匹配。
func TestAmbientFacts(t *testing.T) {
	token := mustToken(t, `right("alice", "read", "file1")`)
	auth := mustAuthorizer(t, `allow if right($u, $a, $r), resource($r)`)
	auth.Ambient = []Predicate{{Name: "resource", Args: []Term{String("file1")}}}
	if res := Evaluate(token, auth); !res.Allowed() {
		t.Fatalf("ambient resource should complete the policy join, got %+v", res)
	}

	auth.Ambient = []Predicate{{Name: "resource", Args: []Term{String("file2")}}}
	if res := Evaluate(token, auth); res.Allowed() {
		t.Fatalf("non-matching ambient fact must not allow")
	}
}

func TestParseAndSafetyErrors(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		target error
	}{
		{"fact with variable", `right($x, "read")`, ErrSafety},
		{"unbound head variable", `p($x) :- q($y)`, ErrSafety},
		{"policy inside block", `allow if right($x)`, ErrParse},
		{"rule inside authorizer", `p($x) :- q($x)`, ErrParse},
		{"check inside authorizer", `check if q($x)`, ErrParse},
		{"unterminated predicate", `right("alice"`, ErrParse},
		{"bad token", `right(@)`, ErrParse},
		{"empty token", ``, ErrEmptyToken},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			if tc.name == "rule inside authorizer" || tc.name == "check inside authorizer" {
				_, err = ParseAuthorizer(tc.src)
			} else if tc.name == "empty token" {
				_, err = ParseToken()
			} else {
				_, err = ParseBlock(0, tc.src)
			}
			if !errors.Is(err, tc.target) {
				t.Fatalf("expected %v, got %v", tc.target, err)
			}
		})
	}
}

// 分号与换行都可以作为语句终止符，括号内换行不截断语句。
func TestTerminatorsAndMultiline(t *testing.T) {
	token := mustToken(t, `
right("alice", "read", "file1");
check if right(
  "alice",
  "read",
  "file1"
);
`)
	auth := mustAuthorizer(t, `allow if right($u, $a, $r);`)
	if res := Evaluate(token, auth); !res.Allowed() {
		t.Fatalf("multiline predicate and semicolons should parse, got %+v", res)
	}
}
