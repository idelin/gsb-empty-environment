package netsec

import "testing"

func mustParse(t *testing.T, src string) []*Policy {
	t.Helper()
	policies, err := ParsePolicies(src)
	if err != nil {
		t.Fatalf("ParsePolicies error: %v", err)
	}
	return policies
}

func statuses(diags []PolicyDiagnostic) []PolicyStatus {
	out := make([]PolicyStatus, len(diags))
	for i, diag := range diags {
		out[i] = diag.Status
	}
	return out
}

func equalStatuses(got, want []PolicyStatus) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func diagErrors(diags []PolicyDiagnostic) []string {
	var out []string
	for _, diag := range diags {
		if diag.Status == StatusError {
			out = append(out, diag.Error)
		}
	}
	return out
}

// 攻击者故意省略上下文属性：出错的 forbid 在 fail-open 下被跳过，
// 但必须有求值成功且满足的 permit 才能放行。
func TestMissingAttributeForbidSkipped(t *testing.T) {
	policies := mustParse(t, `
		forbid principal action resource
			when principal.clearance_level < resource.required_level;
		permit principal action resource
			when action.name == "read";
	`)

	req := &Request{
		Principal: map[string]any{"name": "mallory"},
		Action:    map[string]any{"name": "read"},
		Resource:  map[string]any{"required_level": 3.0},
	}

	result := Evaluate(policies, req, false)
	if result.Decision != DecisionAllow {
		t.Fatalf("fail-open: expected allow when forbid errors and permit matches, got %s", result.Decision)
	}
	want := []PolicyStatus{StatusError, StatusMatched}
	if got := statuses(result.Diagnostics); !equalStatuses(got, want) {
		t.Fatalf("statuses = %v, want %v", got, want)
	}
	if errs := diagErrors(result.Diagnostics); len(errs) != 1 {
		t.Fatalf("expected exactly one error diagnostic, got %v", errs)
	}

	// 没有任何 permit 满足时，出错的 forbid 不能让请求蒙混放行：默认拒绝。
	denyReq := &Request{
		Principal: map[string]any{"name": "mallory"},
		Action:    map[string]any{"name": "write"},
		Resource:  map[string]any{"required_level": 3.0},
	}
	denyResult := Evaluate(policies, denyReq, false)
	if denyResult.Decision != DecisionDeny {
		t.Fatalf("expected default deny when no permit matches, got %s", denyResult.Decision)
	}
	wantStatuses := []PolicyStatus{StatusError, StatusNotMatched}
	if got := statuses(denyResult.Diagnostics); !equalStatuses(got, wantStatuses) {
		t.Fatalf("statuses = %v, want %v", got, wantStatuses)
	}
}

// 同一条缺属性 forbid，在 failClosed 下直接拒绝整个请求。
func TestFailClosedRejectsErroringForbid(t *testing.T) {
	policies := mustParse(t, `
		forbid principal action resource
			when principal.clearance_level < resource.required_level;
		permit principal action resource
			when action.name == "read";
	`)
	req := &Request{
		Principal: map[string]any{"name": "mallory"},
		Action:    map[string]any{"name": "read"},
		Resource:  map[string]any{"required_level": 3.0},
	}

	result := Evaluate(policies, req, true)
	if result.Decision != DecisionDeny {
		t.Fatalf("failClosed: expected deny, got %s", result.Decision)
	}
	if !result.FailClosed {
		t.Fatal("expected result.FailClosed to be true")
	}
	if result.Diagnostics[0].Status != StatusError {
		t.Fatalf("first policy status = %s, want error", result.Diagnostics[0].Status)
	}
	// failClosed 在第一条出错 forbid 处即短路，后续策略不再求值。
	if len(result.Diagnostics) != 1 {
		t.Fatalf("failClosed should short-circuit, got %d diagnostics", len(result.Diagnostics))
	}

	// 对照：属性齐全且 forbid 条件为假时，failClosed 不影响正常放行。
	okReq := &Request{
		Principal: map[string]any{"clearance_level": 5.0},
		Action:    map[string]any{"name": "read"},
		Resource:  map[string]any{"required_level": 3.0},
	}
	okResult := Evaluate(policies, okReq, true)
	if okResult.Decision != DecisionAllow {
		t.Fatalf("expected allow when forbid condition is false, got %s", okResult.Decision)
	}
}

// has 保护：缺失属性返回 false 而不是出错，配合 && 短路避免后续访问出错。
func TestHasGuardDoesNotError(t *testing.T) {
	policies := mustParse(t, `
		permit principal action resource
			when principal has "tenant" && principal.tenant == resource.tenant;
	`)

	missing := &Request{
		Principal: map[string]any{"name": "mallory"},
		Resource:  map[string]any{"tenant": "t-1"},
	}
	result := Evaluate(policies, missing, true)
	if result.Decision != DecisionDeny {
		t.Fatalf("expected deny, got %s", result.Decision)
	}
	if result.Diagnostics[0].Status != StatusNotMatched {
		t.Fatalf("has guard: expected not_matched, got %s (%q)",
			result.Diagnostics[0].Status, result.Diagnostics[0].Error)
	}

	present := &Request{
		Principal: map[string]any{"tenant": "t-1"},
		Resource:  map[string]any{"tenant": "t-1"},
	}
	if Evaluate(policies, present, false).Decision != DecisionAllow {
		t.Fatal("expected allow when guarded attribute exists and matches")
	}

	// 没有 has 保护时，同样的缺失属性访问必须被记为出错。
	unguarded := mustParse(t, `
		permit principal action resource
			when principal.tenant == resource.tenant;
	`)
	errResult := Evaluate(unguarded, missing, false)
	if errResult.Diagnostics[0].Status != StatusError {
		t.Fatalf("unguarded access: expected error, got %s", errResult.Diagnostics[0].Status)
	}
	if errResult.Decision != DecisionDeny {
		t.Fatal("permit 出错时不能放行")
	}
}

// 类型不符属于求值错误：在非记录上取属性、in 右侧不是集合、
// < 两侧类型不兼容、&& 操作数不是布尔。
func TestTypeMismatchIsEvalError(t *testing.T) {
	tests := []struct {
		name string
		expr string
		req  *Request
	}{
		{
			name: "attribute access on string",
			expr: `principal.name.owner == "x"`,
			req:  &Request{Principal: map[string]any{"name": "mallory"}},
		},
		{
			name: "in without set on right",
			expr: `action.name in "read"`,
			req:  &Request{Action: map[string]any{"name": "read"}},
		},
		{
			name: "less-than incompatible types",
			expr: `principal.age < resource.name`,
			req: &Request{
				Principal: map[string]any{"age": 30.0},
				Resource:  map[string]any{"name": "doc"},
			},
		},
		{
			name: "and non-bool operand",
			expr: `principal.age && resource.flag`,
			req: &Request{
				Principal: map[string]any{"age": 30.0},
				Resource:  map[string]any{"flag": true},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			policies := mustParse(t,
				"forbid principal action resource when "+tc.expr+";")
			result := Evaluate(policies, tc.req, false)
			if result.Diagnostics[0].Status != StatusError {
				t.Fatalf("expected error status, got %s (%q)",
					result.Diagnostics[0].Status, result.Diagnostics[0].Error)
			}
			if result.Decision != DecisionDeny {
				t.Fatalf("expected deny, got %s", result.Decision)
			}
			closed := Evaluate(policies, tc.req, true)
			if closed.Decision != DecisionDeny || len(closed.Diagnostics) != 1 {
				t.Fatalf("failClosed expected short-circuit deny, got %s with %d diags",
					closed.Decision, len(closed.Diagnostics))
			}
		})
	}
}

// permit 自身求值出错时不产生放行效果；forbid 优先于 permit。
func TestPermitErrorAndForbidPrecedence(t *testing.T) {
	policies := mustParse(t, `
		permit principal action resource
			when principal.missing_attr == "x";
		forbid principal action resource
			when resource.classified == true;
		permit principal action resource
			when action.name == "read";
	`)
	req := &Request{
		Principal: map[string]any{"name": "mallory"},
		Action:    map[string]any{"name": "read"},
		Resource:  map[string]any{"classified": true},
	}
	result := Evaluate(policies, req, false)
	if result.Decision != DecisionDeny {
		t.Fatalf("matched forbid must win over permit, got %s", result.Decision)
	}
	// forbid 命中即短路：只保留到该 forbid 为止的诊断。
	want := []PolicyStatus{StatusError, StatusMatched}
	if got := statuses(result.Diagnostics); !equalStatuses(got, want) {
		t.Fatalf("statuses = %v, want %v (forbid should short-circuit)", got, want)
	}

	noForbid := mustParse(t, `
		permit principal action resource
			when principal.missing_attr == "x";
		permit principal action resource
			when action.name == "read";
	`)
	allowed := Evaluate(noForbid, req, false)
	if allowed.Decision != DecisionAllow {
		t.Fatalf("expected healthy permit to allow, got %s", allowed.Decision)
	}
	if got := statuses(allowed.Diagnostics); !equalStatuses(got,
		[]PolicyStatus{StatusError, StatusMatched}) {
		t.Fatalf("statuses = %v", got)
	}
}

// in、跨类型相等、数字与字符串比较、无 when 无条件策略等基础语义。
func TestOperatorsAndBarePolicy(t *testing.T) {
	policies := mustParse(t, `
		permit principal action resource
			when action.name in ["read", "view"] && resource.level > 2;
		permit principal action resource
			when principal.role != "guest" && principal.age >= 18;
		forbid principal action resource;
	`)
	req := &Request{
		Principal: map[string]any{"role": "member", "age": 20.0},
		Action:    map[string]any{"name": "read"},
		Resource:  map[string]any{"level": 4.0},
	}
	result := Evaluate(policies, req, false)
	if result.Decision != DecisionDeny {
		t.Fatalf("bare forbid must always match, got %s", result.Decision)
	}
	if result.Diagnostics[2].Status != StatusMatched {
		t.Fatalf("bare forbid status = %s", result.Diagnostics[2].Status)
	}

	healthy := policies[:2]
	if ok := Evaluate(healthy, req, false); ok.Decision != DecisionAllow {
		t.Fatalf("expected allow, got %s", ok.Decision)
	}

	missReq := &Request{
		Principal: map[string]any{"role": "guest", "age": 15.0},
		Action:    map[string]any{"name": "delete"},
		Resource:  map[string]any{"level": 4.0},
	}
	miss := Evaluate(healthy, missReq, false)
	// 两条 permit 都不满足（in 不命中、角色/年龄不达标），默认拒绝且无求值错误。
	if miss.Decision != DecisionDeny {
		t.Fatalf("in miss: expected deny, got %s", miss.Decision)
	}
	if got := statuses(miss.Diagnostics); !equalStatuses(got,
		[]PolicyStatus{StatusNotMatched, StatusNotMatched}) {
		t.Fatalf("in miss: statuses = %v, want both not_matched", got)
	}

	eqPolicies := mustParse(t, `
		permit principal action resource when principal.age == "30";
		permit principal action resource when principal.role != 42;
	`)
	eqReq := &Request{Principal: map[string]any{"age": 30.0, "role": "member"}}
	eq := Evaluate(eqPolicies, eqReq, false)
	if eq.Decision != DecisionAllow {
		t.Fatalf("second permit should match via != across types, got %s", eq.Decision)
	}
	for _, diag := range eq.Diagnostics {
		if diag.Status == StatusError {
			t.Fatalf("cross-type equality must not error: %q", diag.Error)
		}
	}
}

// 空策略集默认拒绝。
func TestDefaultDenyWithNoPolicies(t *testing.T) {
	result := Evaluate(nil, &Request{}, true)
	if result.Decision != DecisionDeny {
		t.Fatalf("expected default deny, got %s", result.Decision)
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("expected no diagnostics, got %d", len(result.Diagnostics))
	}
}

func TestParseErrors(t *testing.T) {
	bad := []string{
		`allow principal action resource when true;`,
		`permit principal resource when true;`,
		`forbid principal action resource when principal. == "x";`,
		`permit principal action resource when (action.name == "x";`,
	}
	for _, src := range bad {
		if _, err := ParsePolicies(src); err == nil {
			t.Fatalf("expected parse error for %q", src)
		}
	}
}

// 不依赖文本解析，直接用 AST 类型构造策略并求值，验证包的可编程入口。
func TestProgrammaticASTAndEvalCondition(t *testing.T) {
	// forbid when resource.meta.owner == principal.name
	forbid := &Policy{
		ID:     "ast-forbid",
		Effect: Forbid,
		When: &BinaryExpr{
			Op: "==",
			LHS: &AttrExpr{
				Target: &AttrExpr{Target: &VarExpr{Name: "resource"}, Name: "meta"},
				Name:   "owner",
			},
			RHS: &AttrExpr{Target: &VarExpr{Name: "principal"}, Name: "name"},
		},
	}
	req := &Request{
		Principal: map[string]any{"name": "alice"},
		Resource:  map[string]any{"meta": map[string]any{"owner": "bob"}},
	}

	matched, err := EvalCondition(forbid.When, req)
	if err != nil {
		t.Fatalf("EvalCondition error: %v", err)
	}
	if matched {
		t.Fatal("owner bob != alice, condition should be false")
	}

	// 只有 forbid 且不满足 => 默认拒绝；补上匹配的 permit 后放行。
	if got := Evaluate([]*Policy{forbid}, req, false); got.Decision != DecisionDeny {
		t.Fatalf("expected default deny, got %s", got.Decision)
	}
	permit := &Policy{ID: "ast-permit", Effect: Permit, When: &LiteralExpr{Value: true}}
	if got := Evaluate([]*Policy{forbid, permit}, req, false); got.Decision != DecisionAllow {
		t.Fatalf("expected allow, got %s", got.Decision)
	}

	// 直接取不存在的嵌套属性，EvalCondition 必须返回错误。
	bad := &AttrExpr{
		Target: &AttrExpr{Target: &VarExpr{Name: "resource"}, Name: "missing"},
		Name:   "owner",
	}
	if _, err := EvalCondition(bad, req); err == nil {
		t.Fatal("expected error when traversing a missing attribute")
	}
}

// 补充短路 ||、字符串/整数比较、has 目标非记录、in 命中集合等边界。
func TestShortCircuitAndRemainingBranches(t *testing.T) {
	// || 短路：左侧为真时，右侧缺属性不求值、不报错。
	orPolicies := mustParse(t, `
		permit principal action resource
			when action.name == "read" || principal.missing.deep == 1;
	`)
	orReq := &Request{Action: map[string]any{"name": "read"}}
	if got := Evaluate(orPolicies, orReq, true); got.Decision != DecisionAllow {
		t.Fatalf("|| short-circuit should allow, got %s", got.Decision)
	}

	// 字符串与整数比较走对应类型分支。
	cmp := mustParse(t, `
		permit principal action resource when principal.name < "z";
		permit principal action resource when principal.level >= 1;
	`)
	cmpReq := &Request{
		Principal: map[string]any{"name": "alice", "level": 2},
	}
	cmpResult := Evaluate(cmp, cmpReq, false)
	if cmpResult.Decision != DecisionAllow {
		t.Fatalf("string/int comparisons should match, got %s", cmpResult.Decision)
	}

	// has 目标不是记录：属于求值错误，而不是静默 false。
	hasBad := mustParse(t, `
		forbid principal action resource when principal.name has "owner";
	`)
	hasReq := &Request{Principal: map[string]any{"name": "alice"}}
	if got := Evaluate(hasBad, hasReq, false); got.Diagnostics[0].Status != StatusError {
		t.Fatalf("has on non-record should error, got %s", got.Diagnostics[0].Status)
	}

	// in 命中集合（含整数元素）放行。
	inPolicies := mustParse(t, `
		permit principal action resource when principal.level in [1, 2, 3];
	`)
	if got := Evaluate(inPolicies, cmpReq, false); got.Decision != DecisionAllow {
		t.Fatalf("in membership should match, got %s", got.Decision)
	}

	// Effect 的字符串表示稳定，便于日志记录。
	if Permit.String() != "permit" || Forbid.String() != "forbid" {
		t.Fatal("unexpected effect string")
	}
}
