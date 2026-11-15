package netsec

import (
	"fmt"
	"sort"
)

// Analyze 在策略与绑定保存前执行失效路径分析。
// objects 为参数资源当前对象，grants 为各主体对参数对象的删除授权。
func Analyze(policy Policy, binding Binding, objects []ParameterObject, grants []DeleteGrant) Report {
	report := Report{Policy: policy, Binding: binding}

	report.analyzeValidationActions()
	report.analyzeFailurePolicy()
	report.analyzeParamRef(objects, grants)

	return report
}

func (r *Report) analyzeValidationActions() {
	for _, action := range r.Binding.ValidationActions {
		if action == ActionDeny {
			return
		}
	}
	r.Findings = append(r.Findings, Finding{
		Code:        CodeNoDenyAction,
		Severity:    SeverityHigh,
		Message:     "validationActions 不含 Deny，命中策略的请求仍会被 API Server 放行（仅 Warn/Audit）",
		PolicyName:  r.Policy.Name,
		BindingName: r.Binding.Name,
		Bypassable:  true,
	})
}

func (r *Report) analyzeFailurePolicy() {
	if r.Policy.FailurePolicy != FailurePolicyIgnore {
		return
	}
	var references []string
	for _, validation := range r.Policy.Validations {
		if validation.ErrorPossible {
			references = append(references, fmt.Sprintf("validation[%d]", validation.RuleIndex))
		}
	}
	for _, condition := range r.Policy.MatchConditions {
		if condition.ErrorPossible {
			references = append(references, "matchCondition:"+condition.Name)
		}
	}
	if len(references) == 0 {
		return
	}
	r.Findings = append(r.Findings, Finding{
		Code:        CodeIgnoreExpressionError,
		Severity:    SeverityHigh,
		Message:     "failurePolicy=Ignore 时，下列表达式求值出错会放行请求，攻击者可构造特殊对象触发错误以绕过策略",
		PolicyName:  r.Policy.Name,
		BindingName: r.Binding.Name,
		Bypassable:  true,
		References:  references,
	})
}

func (r *Report) analyzeParamRef(objects []ParameterObject, grants []DeleteGrant) {
	ref := r.Binding.ParamRef
	if ref == nil {
		return
	}

	if ref.Name != "" && ref.Selector != nil {
		r.Findings = append(r.Findings, Finding{
			Code:        CodeNameAndSelectorBothSet,
			Severity:    SeverityBlocker,
			Message:     "paramRef 不能同时设置 name 与 selector，绑定语义非法，必须拒绝保存",
			PolicyName:  r.Policy.Name,
			BindingName: r.Binding.Name,
		})
		return
	}

	allowOnMissing := r.paramNotFoundAction() == ParamNotFoundAllow

	switch {
	case ref.Name != "":
		r.analyzeNameRef(ref.Name, allowOnMissing, objects, grants)
	case ref.Selector != nil:
		r.analyzeSelectorRef(ref.Selector, allowOnMissing, objects, grants)
	}
}

func (r *Report) analyzeNameRef(name string, allowOnMissing bool, objects []ParameterObject, grants []DeleteGrant) {
	existing := findObject(objects, name)
	if existing == nil {
		if allowOnMissing {
			r.Findings = append(r.Findings, Finding{
				Code:        CodeParameterMissingAllow,
				Severity:    SeverityHigh,
				Message:     fmt.Sprintf("参数对象 %q 当前不存在且 parameterNotFoundAction=Allow，参数缺失期间策略完全失效", name),
				PolicyName:  r.Policy.Name,
				BindingName: r.Binding.Name,
				Bypassable:  true,
			})
		}
		return
	}

	subjects := restrictedDeleters(name, grants)
	r.reportDeletable([]string{name}, subjects, allowOnMissing)
}

func (r *Report) analyzeSelectorRef(selector *LabelSelector, allowOnMissing bool, objects []ParameterObject, grants []DeleteGrant) {
	var matched []string
	for _, object := range objects {
		if selector.Matches(object.Labels) {
			matched = append(matched, object.Name)
		}
	}
	sort.Strings(matched)

	if len(matched) == 0 {
		if allowOnMissing {
			r.Findings = append(r.Findings, Finding{
				Code:        CodeSelectorZeroMatchAllow,
				Severity:    SeverityHigh,
				Message:     "paramRef.selector 当前命中零个参数对象且 parameterNotFoundAction=Allow，策略处于失效状态",
				PolicyName:  r.Policy.Name,
				BindingName: r.Binding.Name,
				Bypassable:  true,
			})
		}
		return
	}

	// 通过删除参数实现绕过需要清空全部命中对象，因此只报告能删除所有命中对象的受限主体。
	var subjects []string
	for _, subject := range restrictedSubjects(grants) {
		if canDeleteAll(subject, matched, grants) {
			subjects = append(subjects, subject)
		}
	}
	r.reportDeletable(matched, subjects, allowOnMissing)
}

func (r *Report) reportDeletable(objects, subjects []string, allowOnMissing bool) {
	if len(subjects) == 0 {
		return
	}
	sort.Strings(subjects)
	finding := Finding{
		Code:               CodeParameterDeletable,
		PolicyName:         r.Policy.Name,
		BindingName:        r.Binding.Name,
		RestrictedSubjects: subjects,
	}
	if allowOnMissing {
		finding.Severity = SeverityHigh
		finding.Bypassable = true
		finding.Message = fmt.Sprintf(
			"受限主体可删除参数对象 %v，删除后 parameterNotFoundAction=Allow 将放行请求，构成可利用的绕过路径",
			objects,
		)
	} else {
		finding.Severity = SeverityMedium
		finding.Message = fmt.Sprintf(
			"受限主体可删除参数对象 %v；parameterNotFoundAction=Deny 时删除导致策略失败关闭（拒绝），属可用性风险而非绕过",
			objects,
		)
	}
	r.Findings = append(r.Findings, finding)
}

func (r *Report) paramNotFoundAction() ParamNotFoundAction {
	if r.Binding.ParameterNotFoundAction == "" {
		return ParamNotFoundDeny // Kubernetes 默认值
	}
	return r.Binding.ParameterNotFoundAction
}

func findObject(objects []ParameterObject, name string) *ParameterObject {
	for index := range objects {
		if objects[index].Name == name {
			return &objects[index]
		}
	}
	return nil
}

func restrictedSubjects(grants []DeleteGrant) []string {
	seen := map[string]struct{}{}
	var subjects []string
	for _, grant := range grants {
		if grant.Subject.Restricted {
			if _, ok := seen[grant.Subject.Name]; !ok {
				seen[grant.Subject.Name] = struct{}{}
				subjects = append(subjects, grant.Subject.Name)
			}
		}
	}
	return subjects
}

func canDeleteAll(subject string, objects []string, grants []DeleteGrant) bool {
	wildcard := false
	perObject := map[string]struct{}{}
	for _, grant := range grants {
		if grant.Subject.Name != subject || !grant.Subject.Restricted {
			continue
		}
		if grant.ObjectName == "*" {
			wildcard = true
			continue
		}
		perObject[grant.ObjectName] = struct{}{}
	}
	if wildcard {
		return true
	}
	for _, object := range objects {
		if _, ok := perObject[object]; !ok {
			return false
		}
	}
	return true
}

func restrictedDeleters(objectName string, grants []DeleteGrant) []string {
	var subjects []string
	for _, grant := range grants {
		if !grant.Subject.Restricted {
			continue
		}
		if grant.ObjectName == "*" || grant.ObjectName == objectName {
			subjects = append(subjects, grant.Subject.Name)
		}
	}
	return subjects
}
