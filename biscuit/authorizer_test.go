package biscuit

import (
	"strings"
	"testing"
)

func mustBlock(t *testing.T, source string) *Block {
	t.Helper()
	block, err := ParseBlock(source)
	if err != nil {
		t.Fatalf("ParseBlock(%q) error: %v", source, err)
	}
	return block
}

func mustPolicies(t *testing.T, source string) []Policy {
	t.Helper()
	policies, err := ParsePolicies(source)
	if err != nil {
		t.Fatalf("ParsePolicies(%q) error: %v", source, err)
	}
	return policies
}

func authorizeSources(t *testing.T, blockSources, policySources []string) *Result {
	t.Helper()
	blocks := make([]*Block, len(blockSources))
	for i, source := range blockSources {
		blocks[i] = mustBlock(t, source)
	}
	authorizer, err := New(blocks, mustPolicies(t, strings.Join(policySources, "\n")))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return authorizer.Authorize()
}

const policyAllowRead = `
allow if right($user, "read")
`

const policyAllowReadWithFallbackDeny = `
allow if right($user, "read")
deny if true
`

// 衰减块直接伪造 right 事实：本块 check 可以看到，但策略看不到，最终默认拒绝。
func TestAttenuatedForgedRightFact(t *testing.T) {
	authority := `
user("alice")
right("alice", "write")
check if user("alice")
`
	attenuated := `
right("alice", "read")
check if right("alice", "read")
`
	result := authorizeSources(t, []string{authority, attenuated}, []string{policyAllowRead})
	if result.Decision != DecisionDeny || result.Reason != ReasonNoPolicy {
		t.Fatalf("expected deny/no policy, got %s", result)
	}

	// 加上兜底 deny 后结论仍为拒绝，只是命中点变为 deny 策略。
	result = authorizeSources(t, []string{authority, attenuated}, []string{policyAllowReadWithFallbackDeny})
	if result.Decision != DecisionDeny || result.Reason != ReasonDenied {
		t.Fatalf("expected fallback deny, got %s", result)
	}

	// 对照：authority 块本身授予 read 时应当放行。
	authorityWithRead := `
user("alice")
right("alice", "read")
check if user("alice")
`
	result = authorizeSources(t, []string{authorityWithRead}, []string{policyAllowRead})
	if result.Decision != DecisionAllow {
		t.Fatalf("expected allow with authority-granted right, got %s", result)
	}
}

// 衰减块用与 authority 同名的 head 谓词推事实，不得写回 authority 作用域。
func TestAttenuatedRuleHeadDoesNotEnterAuthorityScope(t *testing.T) {
	authority := `
user("alice")
right("alice", "write")
check if user($u)
`
	attenuated := `
user("mallory")
right($u, "read") :- user($u)
check if right("mallory", "read")
`
	result := authorizeSources(t, []string{authority, attenuated}, []string{policyAllowRead})
	if result.Decision != DecisionDeny || result.Reason != ReasonNoPolicy {
		t.Fatalf("shadowed head must stay in block scope, got %s", result)
	}

	result = authorizeSources(t, []string{authority, attenuated}, []string{policyAllowReadWithFallbackDeny})
	if result.Decision != DecisionDeny || result.Reason != ReasonDenied {
		t.Fatalf("fallback deny should still deny, got %s", result)
	}

	// 本块 check 能在私有作用域里看到推出的 right 事实（上面的 check 已通过），
	// 这里再验证伪造事实确实可用于本块 check，而失败只会发生在策略阶段。
	policies := `deny if right($u, "read")`
	result = authorizeSources(t, []string{authority, attenuated}, []string{policies})
	if result.Decision != DecisionDeny || result.Reason != ReasonNoPolicy {
		t.Fatalf("policy must not observe block-derived right, got %s", result)
	}
}

func TestCheckFailure(t *testing.T) {
	authority := `
right("alice", "read")
check if right("alice", "read")
`
	attenuated := `
check if right("alice", "delete")
`
	result := authorizeSources(t, []string{authority, attenuated}, []string{policyAllowRead})
	if result.Decision != DecisionDeny || result.Reason != ReasonCheckFailed || result.BlockID != 1 {
		t.Fatalf("expected block 1 check failure, got %s", result)
	}

	// authority 块 check 失败同样拒绝。
	badAuthority := `
right("alice", "read")
check if right("alice", "delete")
`
	result = authorizeSources(t, []string{badAuthority}, []string{policyAllowRead})
	if result.Decision != DecisionDeny || result.Reason != ReasonCheckFailed || result.BlockID != 0 {
		t.Fatalf("expected block 0 check failure, got %s", result)
	}

	// check 的多个查询是 or 关系，任一命中即通过。
	orCheck := `
right("alice", "read")
check if right("alice", "delete") || right("alice", "read")
`
	result = authorizeSources(t, []string{orCheck}, []string{policyAllowRead})
	if result.Decision != DecisionAllow {
		t.Fatalf("or-check should pass, got %s", result)
	}
}

func TestPolicyFirstMatchWins(t *testing.T) {
	authority := `
right("alice", "read")
revoked("alice")
`
	denyFirst := `
deny if revoked($u)
allow if right($u, "read")
`
	result := authorizeSources(t, []string{authority}, []string{denyFirst})
	if result.Decision != DecisionDeny || result.Reason != ReasonDenied || result.PolicyIndex != 0 {
		t.Fatalf("deny must win when listed first, got %s", result)
	}

	allowFirst := `
allow if right($u, "read")
deny if revoked($u)
`
	result = authorizeSources(t, []string{authority}, []string{allowFirst})
	if result.Decision != DecisionAllow || result.PolicyIndex != 0 {
		t.Fatalf("allow must win when listed first, got %s", result)
	}

	// 前面的策略不命中时继续向后匹配。
	skipping := `
deny if right($u, "delete")
allow if right($u, "read")
deny if true
`
	result = authorizeSources(t, []string{authority}, []string{skipping})
	if result.Decision != DecisionAllow || result.PolicyIndex != 1 {
		t.Fatalf("expected second policy allow, got %s", result)
	}
}

func chainBlock(edges int) string {
	var builder strings.Builder
	for i := 0; i < edges; i++ {
		builder.WriteString("edge(\"n")
		builder.WriteString(itoa(i))
		builder.WriteString("\", \"n")
		builder.WriteString(itoa(i + 1))
		builder.WriteString("\")\n")
	}
	builder.WriteString("path($a, $b) :- edge($a, $b)\n")
	builder.WriteString("path($a, $c) :- edge($a, $b), path($b, $c)\n")
	return builder.String()
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

func TestFixpointOverflow(t *testing.T) {
	// 105 条边的传递闭包需要 105 轮才能收敛，超过 100 轮上限。
	overflowAuthority := chainBlock(105)
	result := authorizeSources(t, []string{overflowAuthority}, []string{`allow if true`})
	if result.Decision != DecisionDeny || result.Reason != ReasonFixpoint || result.BlockID != 0 {
		t.Fatalf("expected fixpoint overflow denial, got %s", result)
	}

	// 衰减块内超限同样拒绝，且报告对应块号。
	smallAuthority := `user("alice")`
	result = authorizeSources(t, []string{smallAuthority, chainBlock(105)}, []string{`allow if true`})
	if result.Decision != DecisionDeny || result.Reason != ReasonFixpoint || result.BlockID != 1 {
		t.Fatalf("expected block 1 fixpoint overflow, got %s", result)
	}

	// 短链在 100 轮内正常收敛。
	converging := chainBlock(10) + `check if path("n0", "n10")`
	result = authorizeSources(t, []string{converging}, []string{`allow if true`})
	if result.Decision != DecisionAllow {
		t.Fatalf("short chain should converge, got %s", result)
	}
}

func TestVariableBindingAndJoins(t *testing.T) {
	authority := `
parent("alice", "bob")
parent("bob", "carol")
parent("carol", "dave")
grandparent($g, $c) :- parent($g, $p), parent($p, $c)
check if grandparent("alice", "carol")
`
	result := authorizeSources(t, []string{authority}, []string{
		`allow if grandparent($g, "dave")`,
		`deny if true`,
	})
	if result.Decision != DecisionAllow {
		t.Fatalf("grandparent join should bind and allow, got %s", result)
	}

	// 同名变量在不同原子中必须绑定到同一个常量：carol 不是 dave 的祖母。
	result = authorizeSources(t, []string{authority}, []string{
		`allow if grandparent("carol", "dave")`,
		`deny if true`,
	})
	if result.Decision != DecisionDeny || result.Reason != ReasonDenied {
		t.Fatalf("carol is parent not grandparent of dave, got %s", result)
	}

	// 同一原子内重复变量要求两个参数相等。
	equality := `
node("a", "a")
node("a", "b")
check if node($x, $x)
`
	result = authorizeSources(t, []string{equality}, []string{`allow if true`})
	if result.Decision != DecisionAllow {
		t.Fatalf("repeated variable should match node(a,a), got %s", result)
	}

	// 跨谓词连接：只有共享 $user 绑定的行才会推出 grant 事实。
	joined := `
role("alice", "admin")
team("alice", "platform")
team("mallory", "platform")
grant($u) :- role($u, "admin"), team($u, "platform")
check if grant("alice")
`
	result = authorizeSources(t, []string{joined}, []string{
		`allow if grant("mallory")`,
		`deny if true`,
	})
	if result.Decision != DecisionDeny || result.Reason != ReasonDenied {
		t.Fatalf("mallory lacks admin role and must not bind, got %s", result)
	}
}

func TestAuthorityRulesVisibleToEveryBlock(t *testing.T) {
	authority := `
member("alice", "eng")
group("eng", "platform")
in_group($u, $g) :- member($u, $team), group($team, $g)
check if in_group("alice", "platform")
`
	attenuated := `
check if in_group("alice", "platform")
`
	result := authorizeSources(t, []string{authority, attenuated}, []string{
		`allow if in_group("alice", "platform")`,
		`deny if true`,
	})
	if result.Decision != DecisionAllow {
		t.Fatalf("authority-derived facts must be visible to block checks, got %s", result)
	}
}

func TestBlockRuleJoinsOverAuthorityFacts(t *testing.T) {
	authority := `
role("alice", "admin")
capability("admin", "manage")
`
	// 衰减块规则的两个 body 原子都落在 authority 作用域，
	// 推出的 can_manage 只存在于本块私有作用域。
	attenuated := `
can_manage($u, $c) :- role($u, $r), capability($r, $c)
check if can_manage("alice", "manage")
`
	result := authorizeSources(t, []string{authority, attenuated}, []string{
		`allow if can_manage($u, $c)`,
		`deny if true`,
	})
	if result.Decision != DecisionDeny || result.Reason != ReasonDenied {
		t.Fatalf("block-derived fact must stay invisible to policies, got %s", result)
	}
}

func TestFixpointRoundsBoundary(t *testing.T) {
	// 100 条边的闭包恰好 100 轮收敛，应当放行。
	converging := chainBlock(100) + `check if path("n0", "n100")`
	result := authorizeSources(t, []string{converging}, []string{`allow if true`})
	if result.Decision != DecisionAllow {
		t.Fatalf("100-round fixpoint should converge, got %s", result)
	}

	// 101 条边需要 101 轮，超过上限而拒绝。
	overflow := chainBlock(101)
	result = authorizeSources(t, []string{overflow}, []string{`allow if true`})
	if result.Decision != DecisionDeny || result.Reason != ReasonFixpoint {
		t.Fatalf("101-round fixpoint must be rejected, got %s", result)
	}
}

func TestParserAndSafetyErrors(t *testing.T) {
	t.Run("fact with variable", func(t *testing.T) {
		if _, err := ParseBlock(`right($u, "read")`); err == nil {
			t.Fatal("expected error for variable in fact")
		}
	})
	t.Run("unsafe head variable", func(t *testing.T) {
		if _, err := ParseBlock(`grant($u) :- role($x, "admin")`); err == nil {
			t.Fatal("expected unsafe rule error")
		}
	})
	t.Run("policy in block", func(t *testing.T) {
		if _, err := ParseBlock(`allow if true`); err == nil {
			t.Fatal("expected error for allow statement in block")
		}
	})
	t.Run("non policy text", func(t *testing.T) {
		if _, err := ParsePolicies(`right("alice", "read")`); err == nil {
			t.Fatal("expected error for non-policy statement")
		}
	})
	t.Run("empty input is valid block", func(t *testing.T) {
		block, err := ParseBlock("")
		if err != nil || len(block.Facts) != 0 {
			t.Fatalf("empty block should parse, got %v %v", block, err)
		}
	})
	t.Run("no blocks", func(t *testing.T) {
		if _, err := New(nil, nil); err == nil {
			t.Fatal("expected error when no blocks are supplied")
		}
	})
}

func TestTermRendering(t *testing.T) {
	fact := Fact{Predicate: "mix", Args: []Term{
		Variable("x"), Symbol("admin"), String("a,b"), Integer(-7), Bool(true), Bool(false),
	}}
	got := fact.String()
	want := `mix($x, admin, "a,b", -7, true, false)`
	if got != want {
		t.Fatalf("rendered %q, want %q", got, want)
	}
}
