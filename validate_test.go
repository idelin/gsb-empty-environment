package netsec

import "testing"

func stringParams(names ...string) []Param {
	params := make([]Param, 0, len(names))
	for _, name := range names {
		params = append(params, Param{Name: name, Type: ParamString})
	}
	return params
}

func findFinding(report *Report, code FindingCode, call string) (Finding, bool) {
	for _, finding := range report.Findings {
		if finding.Code == code && (call == "" || finding.Call == call || finding.Hook == call) {
			return finding, true
		}
	}
	return Finding{}, false
}

func countFindings(report *Report, code FindingCode) int {
	count := 0
	for _, finding := range report.Findings {
		if finding.Code == code {
			count++
		}
	}
	return count
}

// 场景一：只钩 openat，open 族其余调用全部暴露。
func TestValidate_OnlyOpenatHooked(t *testing.T) {
	policy := Policy{
		Name: "only-openat",
		Hooks: []Hook{{
			Call:      "openat",
			Params:    stringParams("path"),
			Selectors: []Selector{{Param: "path", Prefix: "/srv/secret"}},
			Action:    ActionSigkill,
		}},
	}
	targets := []Target{{Path: "/srv/secret/data", Access: AccessOpen}}

	report := Validate(policy, targets, DefaultFamilyTable)

	if !report.Rejected {
		t.Fatal("只钩 openat 时报告必须拒绝保存")
	}

	coverage := report.Coverage[0]
	if len(coverage.Covered) != 1 || coverage.Covered[0] != "openat" {
		t.Fatalf("openat 应被覆盖，实际 covered=%v", coverage.Covered)
	}
	wantUncovered := map[string]bool{"open": true, "openat2": true, "creat": true}
	if len(coverage.Uncovered) != len(wantUncovered) {
		t.Fatalf("应有 %d 个未覆盖调用，实际 %v", len(wantUncovered), coverage.Uncovered)
	}
	for _, call := range coverage.Uncovered {
		if !wantUncovered[call] {
			t.Fatalf("未预期的未覆盖调用 %s", call)
		}
		if _, ok := findFinding(report, FindingUncoveredCall, call); !ok {
			t.Fatalf("缺少 %s 的 UNCOVERED_CALL 发现", call)
		}
	}

	prefixFinding, ok := findFinding(report, FindingPrefixNormalized, "")
	if !ok || !prefixFinding.Normalized {
		t.Fatal("必须输出带规范化标注的 PREFIX_NORMALIZED 发现")
	}
}

// 场景二：相对路径与带 .. 的路径先规范化，再交给 Prefix 评估。
func TestValidate_RelativePathNormalization(t *testing.T) {
	policy := Policy{
		Name: "relative",
		Hooks: []Hook{{
			Call:      "openat",
			Params:    stringParams("path"),
			Selectors: []Selector{{Param: "path", Prefix: "srv/secret"}},
			Action:    ActionSigkill,
		}},
	}

	// work/../srv/secret/key 规范化后是 srv/secret/key，仍被同一相对 Prefix 命中。
	report := Validate(policy, []Target{{
		Path:   "work/../srv/secret/key",
		Access: AccessOpen,
	}}, DefaultFamilyTable)

	coverage := report.Coverage[0]
	if coverage.Normalized != "srv/secret/key" {
		t.Fatalf("规范化结果错误：%q", coverage.Normalized)
	}
	if hookCovered(coverage, "openat") != true {
		t.Fatalf("规范化后的路径应被 openat 覆盖，covered=%v", coverage.Covered)
	}

	prefixFinding, ok := findFinding(report, FindingPrefixNormalized, "")
	if !ok || !prefixFinding.Normalized {
		t.Fatal("相对路径评估必须标注 Prefix 只对规范化路径有效")
	}

	// 开头的 .. 无法消掉：相对目标不会被绝对路径 Prefix 误判为已覆盖。
	absolutePolicy := Policy{
		Name: "absolute",
		Hooks: []Hook{{
			Call:      "openat",
			Params:    stringParams("path"),
			Selectors: []Selector{{Param: "path", Prefix: "/srv/secret"}},
			Action:    ActionSigkill,
		}},
	}
	escapeReport := Validate(absolutePolicy, []Target{{
		Path:   "../etc/passwd",
		Access: AccessOpen,
	}}, DefaultFamilyTable)
	escapeCoverage := escapeReport.Coverage[0]
	if escapeCoverage.Normalized != "../etc/passwd" {
		t.Fatalf("相对路径应保留前导 ..，实际 %q", escapeCoverage.Normalized)
	}
	if hookCovered(escapeCoverage, "openat") {
		t.Fatalf("相对路径 %q 不应被绝对 Prefix 覆盖", escapeCoverage.Target.Path)
	}
}

// 场景三：目录仅由 Postfix 选择器保护，必须报错；文件不受此限制。
func TestValidate_PostfixOnlyOnDirectory(t *testing.T) {
	policy := Policy{
		Name: "postfix-only",
		Hooks: []Hook{{
			Call:      "openat",
			Params:    stringParams("path"),
			Selectors: []Selector{{Param: "path", Postfix: "secret"}},
			Action:    ActionSigkill,
		}},
	}

	dirReport := Validate(policy, []Target{{
		Path:   "/srv/secret",
		IsDir:  true,
		Access: AccessOpen,
	}}, DefaultFamilyTable)

	finding, ok := findFinding(dirReport, FindingPostfixOnlyDirectory, "openat")
	if !ok || finding.Severity != SeverityError {
		t.Fatal("目录仅 Postfix 保护必须输出 error 级 POSTFIX_ONLY_DIRECTORY")
	}
	if !dirReport.Rejected {
		t.Fatal("目录仅 Postfix 保护必须拒绝保存")
	}

	// 普通文件用 Postfix 合法，不应产生该发现。
	fileReport := Validate(policy, []Target{{
		Path:   "/srv/secret",
		IsDir:  false,
		Access: AccessOpen,
	}}, DefaultFamilyTable)
	if countFindings(fileReport, FindingPostfixOnlyDirectory) != 0 {
		t.Fatal("非目录目标不应报 POSTFIX_ONLY_DIRECTORY")
	}

	// 同时带 Prefix 时不再属于“仅 Postfix”。
	combined := Policy{
		Name: "combined",
		Hooks: []Hook{{
			Call:   "openat",
			Params: stringParams("path"),
			Selectors: []Selector{{
				Param:   "path",
				Prefix:  "/srv",
				Postfix: "secret",
			}},
			Action: ActionSigkill,
		}},
	}
	combinedReport := Validate(combined, []Target{{
		Path:   "/srv/secret",
		IsDir:  true,
		Access: AccessOpen,
	}}, DefaultFamilyTable)
	if countFindings(combinedReport, FindingPostfixOnlyDirectory) != 0 {
		t.Fatal("Prefix+Postfix 组合不应报 POSTFIX_ONLY_DIRECTORY")
	}
}

// 场景四：open 族与 rename 族全部调用都有有效钩子时完整覆盖。
func TestValidate_FullCoverage(t *testing.T) {
	policy := Policy{
		Name: "full",
		Hooks: []Hook{
			{Call: "open", Params: stringParams("path"), Selectors: []Selector{{Param: "path", Prefix: "/srv/secret"}}, Action: ActionSigkill},
			{Call: "openat", Params: stringParams("path"), Selectors: []Selector{{Param: "path", Prefix: "/srv/secret"}}, Action: ActionSigkill},
			{Call: "openat2", Params: stringParams("path"), Selectors: []Selector{{Param: "path", Prefix: "/srv/secret"}}, Action: ActionSigkill},
			{Call: "creat", Params: stringParams("path"), Selectors: []Selector{{Param: "path", Prefix: "/srv/secret"}}, Action: ActionSigkill},
			{Call: "rename", Params: stringParams("oldpath", "newpath"), Selectors: []Selector{{Param: "oldpath", Prefix: "/srv/secret"}}, Action: ActionSigkill},
			{Call: "renameat", Params: stringParams("oldpath", "newpath"), Selectors: []Selector{{Param: "oldpath", Prefix: "/srv/secret"}}, Action: ActionSigkill},
			{Call: "renameat2", Params: stringParams("oldpath", "newpath"), Selectors: []Selector{{Param: "newpath", Prefix: "/srv/secret"}}, Action: ActionSigkill},
		},
	}
	targets := []Target{
		{Path: "/srv/secret/data", Access: AccessOpen},
		{Path: "/srv/secret/data", Access: AccessRename},
	}

	report := Validate(policy, targets, DefaultFamilyTable)

	if report.Rejected {
		t.Fatalf("完整覆盖不应拒绝，发现：%v", report.Findings)
	}
	if countFindings(report, FindingUncoveredCall) != 0 {
		t.Fatalf("不应有未覆盖调用，发现：%v", report.Findings)
	}

	wantCovered := map[AccessMode][]string{
		AccessOpen:   {"open", "openat", "openat2", "creat"},
		AccessRename: {"rename", "renameat", "renameat2"},
	}
	for _, coverage := range report.Coverage {
		want := wantCovered[coverage.Target.Access]
		if len(coverage.Covered) != len(want) || len(coverage.Uncovered) != 0 {
			t.Fatalf("访问 %s 覆盖不完整：covered=%v uncovered=%v",
				coverage.Target.Access, coverage.Covered, coverage.Uncovered)
		}
	}
}

// Override 规则：缺返回码拒绝；返回码 0 警告且不计覆盖；非 0 返回码可覆盖。
func TestValidate_OverrideRules(t *testing.T) {
	policy := Policy{
		Name: "override",
		Hooks: []Hook{
			{Call: "open", Params: stringParams("path"), Selectors: []Selector{{Param: "path", Equal: "/srv/secret/x"}}, Action: ActionOverride},
			{Call: "openat", Params: stringParams("path"), Selectors: []Selector{{Param: "path", Equal: "/srv/secret/x"}}, Action: ActionOverride, ReturnCode: int64Ptr(0)},
			{Call: "openat2", Params: stringParams("path"), Selectors: []Selector{{Param: "path", Equal: "/srv/secret/x"}}, Action: ActionOverride, ReturnCode: int64Ptr(-1)},
			{Call: "creat", Params: stringParams("path"), Selectors: []Selector{{Param: "path", Equal: "/srv/secret/x"}}, Action: ActionSigkill},
		},
	}

	report := Validate(policy, []Target{{Path: "/srv/secret/x", Access: AccessOpen}}, DefaultFamilyTable)

	if noReturn, ok := findFinding(report, FindingOverrideNoReturn, "open"); !ok || noReturn.Severity != SeverityError {
		t.Fatal("Override 缺返回码必须输出 error 级 OVERRIDE_NO_RETURN")
	}
	if zero, ok := findFinding(report, FindingOverrideReturnZero, "openat"); !ok || zero.Severity != SeverityWarning {
		t.Fatal("Override 返回码 0 必须输出 warning 级 OVERRIDE_RETURN_ZERO")
	}
	if !report.Rejected {
		t.Fatal("存在 error 级发现时必须拒绝保存")
	}

	coverage := report.Coverage[0]
	if hookCovered(coverage, "open") {
		t.Fatal("缺返回码的 Override 不应形成覆盖")
	}
	if hookCovered(coverage, "openat") {
		t.Fatal("返回码 0 的 Override 可能放行，不应形成覆盖")
	}
	if !hookCovered(coverage, "openat2") {
		t.Fatal("返回码非 0 的 Override 应形成覆盖")
	}
	if !hookCovered(coverage, "creat") {
		t.Fatal("Sigkill 钩子应形成覆盖")
	}
}

// 选择器引用未声明或非字符串参数时拒绝，并视为未覆盖。
func TestValidate_InvalidSelectorParam(t *testing.T) {
	policy := Policy{
		Name: "bad-param",
		Hooks: []Hook{{
			Call:   "openat",
			Params: []Param{{Name: "flags", Type: ParamFlags}},
			Selectors: []Selector{{
				Param:  "path",
				Prefix: "/srv/secret",
			}},
			Action: ActionSigkill,
		}},
	}

	report := Validate(policy, []Target{{Path: "/srv/secret/x", Access: AccessOpen}}, DefaultFamilyTable)

	if _, ok := findFinding(report, FindingInvalidSelector, "openat"); !ok {
		t.Fatal("必须输出 INVALID_SELECTOR_PARAM 发现")
	}
	coverage := report.Coverage[0]
	if hookCovered(coverage, "openat") {
		t.Fatal("参数非法的选择器不应形成覆盖")
	}
}

// Prefix 按路径段语义评估，/srv/secret2 不会被 /srv/secret 前缀误覆盖。
func TestSelectorMatches_PrefixBoundary(t *testing.T) {
	selector := Selector{Param: "path", Prefix: "/srv/secret"}

	if matched, _, _ := selectorMatches(selector, "/srv/secret/data"); !matched {
		t.Fatal("/srv/secret/data 应命中 Prefix")
	}
	if matched, _, _ := selectorMatches(selector, "/srv/secret2/data"); matched {
		t.Fatal("/srv/secret2/data 不应命中 /srv/secret Prefix")
	}

	// 带 .. 的 Prefix 先规范化再比较。
	dirty := Selector{Param: "path", Prefix: "/srv/./secret/../secret"}
	if matched, _, _ := selectorMatches(dirty, "/srv/secret/data"); !matched {
		t.Fatal("带 . 与 .. 的 Prefix 规范化后应命中")
	}
}

func hookCovered(coverage TargetCoverage, call string) bool {
	for _, covered := range coverage.Covered {
		if covered == call {
			return true
		}
	}
	return false
}

func int64Ptr(value int64) *int64 {
	return &value
}
