package netsec

import (
	"reflect"
	"testing"
)

func restricted(name string) Subject {
	return Subject{Name: name, Restricted: true}
}

func admin() Subject {
	return Subject{Name: "cluster-admin", Restricted: false}
}

func compliantPolicy() Policy {
	return Policy{
		Name:          "p-compliant",
		FailurePolicy: FailurePolicyFail,
		Validations: []Expression{
			{RuleIndex: 0, ErrorPossible: false},
		},
	}
}

// 场景 1：参数对象可被受限主体删除 + Allow → 可绕过；参数已被删除（name 缺失）+ Allow → 放行。
func TestAnalyze_DeletableParameterAndMissingName(t *testing.T) {
	policy := compliantPolicy()
	objects := []ParameterObject{{Name: "params-1", Labels: map[string]string{"app": "demo"}}}
	grants := []DeleteGrant{
		{Subject: restricted("sa:tenant-a"), ObjectName: "params-1"},
		{Subject: admin(), ObjectName: "*"},
	}

	binding := Binding{
		Name:                    "b-delete",
		ParamRef:                &ParamRef{Name: "params-1"},
		ParameterNotFoundAction: ParamNotFoundAllow,
		ValidationActions:       []ValidationAction{ActionDeny},
	}

	report := Analyze(policy, binding, objects, grants)
	if report.Rejected() {
		t.Fatalf("配置合法，不应被拒绝: %+v", report.Findings)
	}
	if !report.Has(CodeParameterDeletable) {
		t.Fatalf("应报告参数可被受限主体删除: %+v", report.Findings)
	}
	if !report.Bypassable() {
		t.Fatalf("Allow + 可删除参数必须标记为可绕过")
	}

	finding := report.Findings[0]
	if !reflect.DeepEqual(finding.RestrictedSubjects, []string{"sa:tenant-a"}) {
		t.Fatalf("受限主体识别错误: %v", finding.RestrictedSubjects)
	}
	if finding.Severity != SeverityHigh {
		t.Fatalf("可绕过删除路径应为 High，实际 %s", finding.Severity)
	}

	// 参数已被删除：对象列表为空，name 找不到且 Allow → 缺失放行。
	missing := Analyze(policy, binding, nil, grants)
	if !missing.Has(CodeParameterMissingAllow) || !missing.Bypassable() {
		t.Fatalf("参数缺失 + Allow 必须报告放行: %+v", missing.Findings)
	}

	// 同样缺失但动作为 Deny（默认）→ 不放行、无绕过。
	binding.ParameterNotFoundAction = ParamNotFoundDeny
	denyReport := Analyze(policy, binding, nil, grants)
	if denyReport.Has(CodeParameterMissingAllow) || denyReport.Bypassable() {
		t.Fatalf("Deny 下参数缺失应失败关闭，不应有绕过: %+v", denyReport.Findings)
	}

	// 对象存在但可删除、动作 Deny → 仅可用性风险，不可绕过。
	dosReport := Analyze(policy, binding, objects, grants)
	if !dosReport.Has(CodeParameterDeletable) || dosReport.Bypassable() {
		t.Fatalf("Deny 下可删除参数应为非绕过风险: %+v", dosReport.Findings)
	}
}

// 场景 2：selector 命中零个对象 + Allow → 放行；有命中且可清空时才标记可绕过。
func TestAnalyze_SelectorZeroMatch(t *testing.T) {
	policy := compliantPolicy()
	selector := &LabelSelector{MatchLabels: map[string]string{"env": "prod"}}
	binding := Binding{
		Name:                    "b-selector",
		ParamRef:                &ParamRef{Selector: selector},
		ParameterNotFoundAction: ParamNotFoundAllow,
		ValidationActions:       []ValidationAction{ActionDeny},
	}

	zeroReport := Analyze(policy, binding, []ParameterObject{
		{Name: "other", Labels: map[string]string{"env": "dev"}},
	}, nil)
	if !zeroReport.Has(CodeSelectorZeroMatchAllow) || !zeroReport.Bypassable() {
		t.Fatalf("零命中 + Allow 必须报告放行: %+v", zeroReport.Findings)
	}

	// 同样零命中但 Deny（默认）→ 不放行。
	binding.ParameterNotFoundAction = ParamNotFoundDeny
	zeroDeny := Analyze(policy, binding, []ParameterObject{
		{Name: "other", Labels: map[string]string{"env": "dev"}},
	}, nil)
	if zeroDeny.Has(CodeSelectorZeroMatchAllow) || zeroDeny.Bypassable() {
		t.Fatalf("Deny 下零命中不应放行: %+v", zeroDeny.Findings)
	}

	// 有两个命中对象：只能删除其中一个的主体无法清空 → 不报告；
	// 持通配授权的受限主体可清空全部 → 报告可绕过。
	binding.ParameterNotFoundAction = ParamNotFoundAllow
	objects := []ParameterObject{
		{Name: "p1", Labels: map[string]string{"env": "prod"}},
		{Name: "p2", Labels: map[string]string{"env": "prod"}},
	}
	grants := []DeleteGrant{
		{Subject: restricted("sa:partial"), ObjectName: "p1"},
		{Subject: restricted("sa:wildcard"), ObjectName: "*"},
	}
	matched := Analyze(policy, binding, objects, grants)
	if !matched.Has(CodeParameterDeletable) {
		t.Fatalf("通配删除主体应触发可删除发现: %+v", matched.Findings)
	}
	for _, finding := range matched.Findings {
		if finding.Code == CodeParameterDeletable {
			if !reflect.DeepEqual(finding.RestrictedSubjects, []string{"sa:wildcard"}) {
				t.Fatalf("只有能清空全部命中对象的主体应被报告: %v", finding.RestrictedSubjects)
			}
		}
	}
}

// 场景 3：failurePolicy=Ignore 且表达式/匹配条件可能出错 → 出错即放行。
func TestAnalyze_IgnoreExpressionError(t *testing.T) {
	policy := Policy{
		Name:          "p-ignore",
		FailurePolicy: FailurePolicyIgnore,
		Validations: []Expression{
			{RuleIndex: 0, ErrorPossible: true},
			{RuleIndex: 1, ErrorPossible: false},
		},
		MatchConditions: []MatchCondition{
			{Name: "ns-check", ErrorPossible: true},
			{Name: "stable", ErrorPossible: false},
		},
	}
	binding := Binding{
		Name:                    "b-ignore",
		ParameterNotFoundAction: ParamNotFoundDeny,
		ValidationActions:       []ValidationAction{ActionDeny},
	}

	report := Analyze(policy, binding, nil, nil)
	if !report.Has(CodeIgnoreExpressionError) || !report.Bypassable() {
		t.Fatalf("Ignore + 可出错表达式必须报告放行: %+v", report.Findings)
	}

	var references []string
	for _, finding := range report.Findings {
		if finding.Code == CodeIgnoreExpressionError {
			references = finding.References
		}
	}
	want := []string{"validation[0]", "matchCondition:ns-check"}
	if !reflect.DeepEqual(references, want) {
		t.Fatalf("出错表达式定位错误，got %v want %v", references, want)
	}

	// 同样的表达式但 failurePolicy=Fail → 出错拒绝，无绕过。
	policy.FailurePolicy = FailurePolicyFail
	failReport := Analyze(policy, binding, nil, nil)
	if failReport.Has(CodeIgnoreExpressionError) || failReport.Bypassable() {
		t.Fatalf("Fail 下表达式出错不应放行: %+v", failReport.Findings)
	}

	// Ignore 但静态分析认为所有表达式都不会出错 → 无该发现。
	safePolicy := Policy{
		Name:          "p-safe",
		FailurePolicy: FailurePolicyIgnore,
		Validations:   []Expression{{RuleIndex: 0, ErrorPossible: false}},
	}
	safeReport := Analyze(safePolicy, binding, nil, nil)
	if safeReport.Has(CodeIgnoreExpressionError) {
		t.Fatalf("无出错可能时不应报告: %+v", safeReport.Findings)
	}
}

// 场景 4：validationActions 仅 Warn（或 Warn+Audit、为空）→ 不含 Deny，请求被放行。
func TestAnalyze_OnlyWarn(t *testing.T) {
	policy := compliantPolicy()

	for _, actions := range [][]ValidationAction{
		{ActionWarn},
		{ActionWarn, ActionAudit},
		{},
	} {
		binding := Binding{
			Name:                    "b-warn",
			ParamRef:                &ParamRef{Name: "params-1"},
			ParameterNotFoundAction: ParamNotFoundDeny,
			ValidationActions:       actions,
		}
		report := Analyze(policy, binding, []ParameterObject{{Name: "params-1"}}, nil)
		if !report.Has(CodeNoDenyAction) || !report.Bypassable() {
			t.Fatalf("actions=%v 不含 Deny 必须报告放行: %+v", actions, report.Findings)
		}
	}

	binding := Binding{
		Name:                    "b-deny",
		ParamRef:                &ParamRef{Name: "params-1"},
		ParameterNotFoundAction: ParamNotFoundDeny,
		ValidationActions:       []ValidationAction{ActionWarn, ActionAudit, ActionDeny},
	}
	report := Analyze(policy, binding, []ParameterObject{{Name: "params-1"}}, nil)
	if report.Has(CodeNoDenyAction) {
		t.Fatalf("含 Deny 时不应报告动作缺失: %+v", report.Findings)
	}
}

// 场景 5：合规绑定 —— Fail + Deny + 参数存在且受限主体无删除权 → 无任何发现。
func TestAnalyze_CompliantBinding(t *testing.T) {
	policy := Policy{
		Name:          "p-good",
		FailurePolicy: FailurePolicyFail,
		Validations:   []Expression{{RuleIndex: 0, ErrorPossible: false}},
		MatchConditions: []MatchCondition{
			{Name: "mc", ErrorPossible: false},
		},
	}
	binding := Binding{
		Name:                    "b-good",
		ParamRef:                &ParamRef{Name: "params-1"},
		ParameterNotFoundAction: ParamNotFoundDeny,
		ValidationActions:       []ValidationAction{ActionDeny, ActionAudit},
	}
	objects := []ParameterObject{{Name: "params-1", Labels: map[string]string{"k": "v"}}}
	grants := []DeleteGrant{
		{Subject: admin(), ObjectName: "*"},
		{Subject: restricted("sa:tenant-a"), ObjectName: "params-other"},
	}

	report := Analyze(policy, binding, objects, grants)
	if len(report.Findings) != 0 || report.Rejected() || report.Bypassable() {
		t.Fatalf("合规绑定不应有发现: %+v", report.Findings)
	}
}

// 非法配置：name 与 selector 同时设置 → Blocker，保存必须拒绝。
func TestAnalyze_NameAndSelectorBothSet(t *testing.T) {
	binding := Binding{
		Name: "b-bad",
		ParamRef: &ParamRef{
			Name:     "params-1",
			Selector: &LabelSelector{MatchLabels: map[string]string{"k": "v"}},
		},
		ParameterNotFoundAction: ParamNotFoundAllow,
		ValidationActions:       []ValidationAction{ActionDeny},
	}
	report := Analyze(compliantPolicy(), binding, nil, nil)
	if !report.Rejected() || !report.Has(CodeNameAndSelectorBothSet) {
		t.Fatalf("name+selector 必须作为 Blocker 拒绝: %+v", report.Findings)
	}

	// 非法 paramRef 时短路，不再重复输出参数缺失类发现。
	if report.Has(CodeParameterMissingAllow) {
		t.Fatalf("非法 paramRef 应短路，不应再分析参数缺失: %+v", report.Findings)
	}
}

// 无 paramRef 的策略（使用对象内变量或无参数）不应触发参数类检查。
func TestAnalyze_NoParamRef(t *testing.T) {
	binding := Binding{
		Name:              "b-no-params",
		ValidationActions: []ValidationAction{ActionDeny},
	}
	report := Analyze(compliantPolicy(), binding, nil, nil)
	if len(report.Findings) != 0 || report.Bypassable() {
		t.Fatalf("无 paramRef 时不应有参数类发现: %+v", report.Findings)
	}
}
