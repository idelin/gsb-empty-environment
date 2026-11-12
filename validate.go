package netsec

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// Severity 表示发现项的严重级别。
type Severity string

const (
	// SeverityError 会导致保存被拒绝（Reject 返回 true）。
	SeverityError Severity = "error"
	// SeverityWarning 不阻断保存，但策略可能达不到预期防护效果。
	SeverityWarning Severity = "warning"
	// SeverityInfo 为说明性标注，例如 Prefix 依据规范化路径评估。
	SeverityInfo Severity = "info"
)

// FindingCode 是发现项的机器可读编码。
type FindingCode string

const (
	// FindingUncoveredCall 保护目标经某个同族调用仍未被覆盖。
	FindingUncoveredCall FindingCode = "UNCOVERED_CALL"
	// FindingPostfixOnlyDirectory 仅靠 Postfix 保护目录。
	FindingPostfixOnlyDirectory FindingCode = "POSTFIX_ONLY_DIRECTORY"
	// FindingOverrideNoReturn Override 钩子缺少返回码。
	FindingOverrideNoReturn FindingCode = "OVERRIDE_NO_RETURN"
	// FindingOverrideReturnZero Override 返回码为 0，可能被当作成功而放行。
	FindingOverrideReturnZero FindingCode = "OVERRIDE_RETURN_ZERO"
	// FindingInvalidSelector 选择器引用了钩子上不存在或非字符串的参数。
	FindingInvalidSelector FindingCode = "INVALID_SELECTOR_PARAM"
	// FindingPrefixNormalized Prefix 依据规范化后的路径评估的标注。
	FindingPrefixNormalized FindingCode = "PREFIX_NORMALIZED"
	// FindingUnknownFamily 目标的访问方式在同族调用表中不存在。
	FindingUnknownFamily FindingCode = "UNKNOWN_FAMILY"
)

// Finding 是一条校验发现。
type Finding struct {
	Severity Severity
	Code     FindingCode
	Target   string // 关联的保护目标路径，策略级问题为空
	Call     string // 关联的系统调用，不涉及具体调用时为空
	Hook     string // 关联的钩子调用名，不涉及钩子时为空
	Message  string
	// Normalized 为 true 时表示该发现与规范化后的路径评估有关，
	// 报告中须显式标注“Prefix 只对规范化后的路径有效”。
	Normalized bool
}

// TargetCoverage 是单个保护目标的覆盖明细。
type TargetCoverage struct {
	Target      Target
	Family      string
	Covered     []string
	Uncovered   []string
	Normalized  string // 规范化后的目标路径
	NormalizedN int    // 参与评估的 Prefix 选择器数量
}

// Report 是一次覆盖校验的结果。
type Report struct {
	Findings []Finding
	Coverage []TargetCoverage
	Rejected bool
}

// HasError 报告是否含 error 级别发现。
func (r *Report) HasError() bool {
	for _, finding := range r.Findings {
		if finding.Severity == SeverityError {
			return true
		}
	}
	return false
}

// Validate 在策略保存前执行同族调用覆盖校验。
//
// 依次校验：
//   - Override 钩子必须显式给出返回码，返回码为 0 时提示可能放行；
//   - 每个保护目标在其同族调用表中的每个调用是否都被有效钩子覆盖；
//   - 目录是否只由 Postfix 选择器保护；
//   - Prefix 选择器一律对规范化后的路径评估，并在报告中标注。
func Validate(policy Policy, targets []Target, table FamilyTable) *Report {
	report := &Report{}
	badParams := make(map[string]map[string]bool)

	for _, hook := range policy.Hooks {
		badParams[hook.Call] = validateHookParams(hook)
		report.Findings = append(report.Findings, validateHookAction(hook)...)
		for _, param := range sortedKeys(badParams[hook.Call]) {
			report.Findings = append(report.Findings, Finding{
				Severity: SeverityError,
				Code:     FindingInvalidSelector,
				Hook:     hook.Call,
				Message: fmt.Sprintf(
					"钩子 %s 的选择器引用了不存在或非字符串类型的参数 %q",
					hook.Call, param,
				),
			})
		}
	}

	for _, target := range targets {
		family, ok := table.lookupFamily(target.Access)
		if !ok {
			report.Findings = append(report.Findings, Finding{
				Severity: SeverityError,
				Code:     FindingUnknownFamily,
				Target:   target.Path,
				Message:  fmt.Sprintf("保护目标 %q 的访问方式 %q 在同族调用表中不存在", target.Path, target.Access),
			})
			continue
		}

		normalizedPath := normalizePath(target.Path)
		coverage := TargetCoverage{
			Target:     target,
			Family:     family.Name,
			Normalized: normalizedPath,
		}

		for _, call := range family.Calls {
			hook, found := findHook(policy.Hooks, call)
			if !found {
				coverage.Uncovered = append(coverage.Uncovered, call)
				report.Findings = append(report.Findings, Finding{
					Severity: SeverityError,
					Code:     FindingUncoveredCall,
					Target:   target.Path,
					Call:     call,
					Message: fmt.Sprintf(
						"保护目标 %q 经同族调用 %s 仍未被覆盖（族 %s 缺少该钩子）",
						target.Path, call, family.Name,
					),
				})
				continue
			}

			matched, blocks, prefixCount, postfixOnly := evaluateHook(hook, normalizedPath, badParams[call])
			coverage.NormalizedN += prefixCount
			if matched && blocks {
				coverage.Covered = append(coverage.Covered, call)
			} else {
				coverage.Uncovered = append(coverage.Uncovered, call)
				reason := "无选择器命中目标路径"
				if matched && !blocks {
					reason = "命中的 Override 钩子返回码为 0，可能放行"
				}
				report.Findings = append(report.Findings, Finding{
					Severity: SeverityError,
					Code:     FindingUncoveredCall,
					Target:   target.Path,
					Call:     call,
					Hook:     call,
					Message: fmt.Sprintf(
						"保护目标 %q 经同族调用 %s 仍未被覆盖：%s",
						target.Path, call, reason,
					),
				})
			}

			if target.IsDir && matched && postfixOnly {
				report.Findings = append(report.Findings, Finding{
					Severity: SeverityError,
					Code:     FindingPostfixOnlyDirectory,
					Target:   target.Path,
					Call:     call,
					Hook:     call,
					Message: fmt.Sprintf(
						"目录 %q 在调用 %s 上仅由 Postfix 选择器保护，Postfix 不能单独用于目录保护",
						target.Path, call,
					),
				})
			}
		}

		if coverage.NormalizedN > 0 {
			report.Findings = append(report.Findings, Finding{
				Severity:   SeverityInfo,
				Code:       FindingPrefixNormalized,
				Target:     target.Path,
				Normalized: true,
				Message: fmt.Sprintf(
					"Prefix 选择器只对规范化后的路径有效：%q 规范化为 %q，共 %d 个 Prefix 按此评估",
					target.Path, normalizedPath, coverage.NormalizedN,
				),
			})
		}

		sort.Strings(coverage.Covered)
		sort.Strings(coverage.Uncovered)
		report.Coverage = append(report.Coverage, coverage)
	}

	report.Rejected = report.HasError()
	return report
}

// validateHookAction 校验钩子动作级规则：
// Override 缺返回码直接拒绝；返回码为 0 提示可能放行。
func validateHookAction(hook Hook) []Finding {
	if hook.Action != ActionOverride {
		return nil
	}
	if hook.ReturnCode == nil {
		return []Finding{{
			Severity: SeverityError,
			Code:     FindingOverrideNoReturn,
			Hook:     hook.Call,
			Message:  fmt.Sprintf("钩子 %s 的动作为 Override 但缺少返回码，拒绝保存", hook.Call),
		}}
	}
	if *hook.ReturnCode == 0 {
		return []Finding{{
			Severity: SeverityWarning,
			Code:     FindingOverrideReturnZero,
			Hook:     hook.Call,
			Message:  fmt.Sprintf("钩子 %s 的 Override 返回码为 0，调用方可能视为成功而放行", hook.Call),
		}}
	}
	return nil
}

// validateHookParams 返回该钩子里引用了不存在或非字符串参数的选择器参数集合。
func validateHookParams(hook Hook) map[string]bool {
	stringParams := make(map[string]bool)
	for _, param := range hook.Params {
		if param.Type == ParamString {
			stringParams[param.Name] = true
		}
	}
	invalid := make(map[string]bool)
	for _, selector := range hook.Selectors {
		if !stringParams[selector.Param] {
			invalid[selector.Param] = true
		}
	}
	return invalid
}

// evaluateHook 判断钩子对目标路径的命中与阻断情况。
//
// 返回值：
//   - matched：是否至少一个选择器命中；
//   - blocks：命中后是否能形成有效覆盖（Sigkill 或非 0 返回码的 Override）；
//   - prefixCount：参与评估的 Prefix 条件数量；
//   - postfixOnly：命中的选择器是否只含 Postfix 条件。
func evaluateHook(hook Hook, normalizedPath string, invalidParams map[string]bool) (matched, blocks bool, prefixCount int, postfixOnly bool) {
	anyMatched := false
	allPostfixOnly := true

	for _, selector := range hook.Selectors {
		if invalidParams[selector.Param] {
			continue
		}
		ok, count, onlyPostfix := selectorMatches(selector, normalizedPath)
		prefixCount += count
		if ok {
			anyMatched = true
			if !onlyPostfix {
				allPostfixOnly = false
			}
		}
	}

	if !anyMatched {
		return false, false, prefixCount, false
	}

	blocks = hook.Action == ActionSigkill ||
		(hook.Action == ActionOverride && hook.ReturnCode != nil && *hook.ReturnCode != 0)
	return true, blocks, prefixCount, allPostfixOnly
}

// selectorMatches 评估单个选择器。空选择器不命中任何路径。
func selectorMatches(selector Selector, normalizedPath string) (matched bool, prefixCount int, postfixOnly bool) {
	hasCondition := false

	if selector.Prefix != "" {
		hasCondition = true
		prefixCount++
		// Prefix 只对规范化后的路径有效。
		prefix := path.Clean(selector.Prefix)
		if !hasPathPrefix(normalizedPath, prefix) {
			return false, prefixCount, false
		}
	}

	if selector.Equal != "" {
		hasCondition = true
		if normalizedPath != path.Clean(selector.Equal) {
			return false, prefixCount, false
		}
	}

	if selector.Postfix != "" {
		hasCondition = true
		if !strings.HasSuffix(normalizedPath, selector.Postfix) {
			return false, prefixCount, false
		}
	}

	if !hasCondition {
		return false, 0, false
	}

	postfixOnly = selector.Prefix == "" && selector.Equal == "" && selector.Postfix != ""
	return true, prefixCount, postfixOnly
}

// findHook 按调用名取钩子，重名时取第一个。
func findHook(hooks []Hook, call string) (Hook, bool) {
	for _, hook := range hooks {
		if hook.Call == call {
			return hook, true
		}
	}
	return Hook{}, false
}

// normalizePath 按 Linux 词法文法规范化路径：
// 解析 . 与 ..、折叠重复斜杠；相对路径与开头的 .. 会保留，
// 因此 ../../etc/passwd 与相对路径不会被误判为受保护前缀。
func normalizePath(raw string) string {
	return path.Clean(raw)
}

// hasPathPrefix 按路径段判断 prefix：/srv/secret 不会误匹配 /srv/secret2。
func hasPathPrefix(target, prefix string) bool {
	if !strings.HasPrefix(target, prefix) {
		return false
	}
	if len(target) == len(prefix) {
		return true
	}
	return prefix == "/" || target[len(prefix)] == '/'
}

// sortedKeys 返回 map 的有序键，保证报告输出稳定。
func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
