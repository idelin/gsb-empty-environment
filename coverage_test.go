package netsec

import (
	"strings"
	"testing"
)

func intPtr(v int) *int {
	return &v
}

func stringArg(index int) Arg {
	return Arg{Index: index, Type: "string"}
}

func sigkillHook(call string, argIndex int, operator Operator, value string) Hook {
	return Hook{
		Call: call,
		Args: []Arg{stringArg(argIndex)},
		Selectors: []Selector{{
			ArgIndex: argIndex,
			Operator: operator,
			Value:    value,
		}},
		Action: ActionSigkill,
	}
}

func gapFor(report *Report, call string) *Gap {
	for i := range report.Gaps {
		if report.Gaps[i].Call == call {
			return &report.Gaps[i]
		}
	}
	return nil
}

func hasIssue(report *Report, code string) bool {
	for _, issue := range report.Issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func TestOpenatOnlyLeavesSameFamilyCallsUncovered(t *testing.T) {
	targets := []Target{FileTarget("/etc/shadow", AccessRead)}
	hooks := []Hook{sigkillHook("sys_openat", 1, OperatorPrefix, "/etc")}

	report, err := ValidateHooks(hooks, targets, DefaultFamilyTable())
	if err != nil {
		t.Fatalf("ValidateHooks returned error: %v", err)
	}
	if !report.Accepted {
		t.Fatal("policy should be structurally accepted")
	}
	if report.Complete {
		t.Fatal("hooking only openat must not be reported as complete")
	}
	if gapFor(report, "openat") != nil {
		t.Fatal("openat should be covered by the Prefix hook")
	}
	for _, call := range []string{"open", "openat2"} {
		if gapFor(report, call) == nil {
			t.Errorf("expected an uncovered gap for %s", call)
		}
	}
	if !hasIssue(report, "prefix-normalized-only") {
		t.Error("report must annotate that Prefix only applies to normalized paths")
	}
}

func TestRelativePathCannotCoverAbsoluteTarget(t *testing.T) {
	targets := []Target{FileTarget("/etc/shadow", AccessRead)}
	hooks := []Hook{sigkillHook("openat", 1, OperatorEqual, "etc/shadow")}

	report, err := ValidateHooks(hooks, targets, DefaultFamilyTable())
	if err != nil {
		t.Fatalf("ValidateHooks returned error: %v", err)
	}
	gap := gapFor(report, "openat")
	if gap == nil {
		t.Fatal("relative openat path must not cover /etc/shadow")
	}
	if !strings.Contains(gap.Reason, "relative") {
		t.Fatalf("gap reason should explain relative path failure, got %q", gap.Reason)
	}
	if !hasIssue(report, "equal-relative-path") {
		t.Error("expected a relative-path warning")
	}
}

func TestTraversalPathSelectorIsNotNormalized(t *testing.T) {
	targets := []Target{FileTarget("/etc/shadow", AccessRead)}
	hooks := []Hook{sigkillHook("openat", 1, OperatorEqual, "/etc/../etc/shadow")}

	report, err := ValidateHooks(hooks, targets, DefaultFamilyTable())
	if err != nil {
		t.Fatalf("ValidateHooks returned error: %v", err)
	}
	if gapFor(report, "openat") == nil {
		t.Fatal("unnormalized traversal selector must not prove coverage")
	}
	if !hasIssue(report, "equal-not-normalized") {
		t.Error("expected a not-normalized warning")
	}
}

func TestPostfixAloneCannotProtectDirectory(t *testing.T) {
	targets := []Target{DirectoryTarget("/etc/ssl", AccessRead)}
	hooks := []Hook{sigkillHook("openat", 1, OperatorPostfix, "ssl")}

	report, err := ValidateHooks(hooks, targets, DefaultFamilyTable())
	if err != nil {
		t.Fatalf("ValidateHooks returned error: %v", err)
	}
	gap := gapFor(report, "openat")
	if gap == nil {
		t.Fatal("Postfix alone must not cover a directory target")
	}
	if !gap.Directory {
		t.Fatal("gap should preserve directory semantics")
	}
	if !strings.Contains(gap.Reason, "Postfix") {
		t.Fatalf("gap reason should mention Postfix, got %q", gap.Reason)
	}
	if !hasIssue(report, "postfix-directory-protection") {
		t.Error("expected a Postfix directory warning")
	}
}

func TestPostfixCoversFileOnly(t *testing.T) {
	targets := []Target{FileTarget("/etc/ssh/sshd_config", AccessRead)}
	hooks := []Hook{sigkillHook("openat", 1, OperatorPostfix, "sshd_config")}

	report, err := ValidateHooks(hooks, targets, DefaultFamilyTable())
	if err != nil {
		t.Fatalf("ValidateHooks returned error: %v", err)
	}
	if gapFor(report, "openat") != nil {
		t.Fatal("Postfix should cover a file target")
	}
}

func TestPrefixMustBeNormalized(t *testing.T) {
	targets := []Target{FileTarget("/etc/shadow", AccessRead)}
	for _, value := range []string{"/etc/", "/etc/./", "/etc/../etc"} {
		t.Run(value, func(t *testing.T) {
			hooks := []Hook{sigkillHook("openat", 1, OperatorPrefix, value)}
			report, err := ValidateHooks(hooks, targets, DefaultFamilyTable())
			if err != nil {
				t.Fatalf("ValidateHooks returned error: %v", err)
			}
			if gapFor(report, "openat") == nil {
				t.Fatalf("unnormalized Prefix %q must not cover the target", value)
			}
			if !hasIssue(report, "prefix-not-normalized") {
				t.Fatalf("expected prefix-not-normalized warning for %q", value)
			}
		})
	}
}

func TestCompleteCoverage(t *testing.T) {
	target := "/srv/data/report.txt"
	targets := []Target{FileTarget(target, AccessWrite)}
	hooks := []Hook{
		sigkillHook("open", 0, OperatorEqual, target),
		sigkillHook("openat", 1, OperatorEqual, target),
		sigkillHook("openat2", 1, OperatorEqual, target),
		sigkillHook("creat", 0, OperatorEqual, target),
		sigkillHook("truncate", 0, OperatorEqual, target),
		sigkillHook("rename", 1, OperatorEqual, target),
		sigkillHook("renameat", 3, OperatorEqual, target),
		sigkillHook("renameat2", 3, OperatorEqual, target),
	}

	report, err := ValidateHooks(hooks, targets, DefaultFamilyTable())
	if err != nil {
		t.Fatalf("ValidateHooks returned error: %v", err)
	}
	if !report.Accepted || !report.Complete || !report.OK() {
		t.Fatalf("expected complete coverage, got %+v", report)
	}
	if len(report.Gaps) != 0 {
		t.Fatalf("expected no gaps, got %+v", report.Gaps)
	}
}

func TestCompleteDirectoryCoverageWithPrefix(t *testing.T) {
	targets := []Target{DirectoryTarget("/etc", AccessRead)}
	hooks := []Hook{
		sigkillHook("open", 0, OperatorPrefix, "/etc"),
		sigkillHook("openat", 1, OperatorPrefix, "/etc"),
		sigkillHook("openat2", 1, OperatorPrefix, "/etc"),
	}

	report, err := ValidateHooks(hooks, targets, DefaultFamilyTable())
	if err != nil {
		t.Fatalf("ValidateHooks returned error: %v", err)
	}
	if !report.Complete {
		t.Fatalf("expected directory coverage, got gaps %+v", report.Gaps)
	}
}

func TestOverrideRequiresReturnCode(t *testing.T) {
	targets := []Target{FileTarget("/etc/shadow", AccessRead)}
	hooks := []Hook{{
		Call:      "openat",
		Args:      []Arg{stringArg(1)},
		Selectors: []Selector{{ArgIndex: 1, Operator: OperatorEqual, Value: "/etc/shadow"}},
		Action:    ActionOverride,
	}}

	report, err := ValidateHooks(hooks, targets, DefaultFamilyTable())
	if err == nil {
		t.Fatal("Override without a return code must be rejected")
	}
	if report == nil || report.Accepted {
		t.Fatal("report should mark the policy as not accepted")
	}
	if !hasIssue(report, "override-missing-return-code") {
		t.Error("expected override-missing-return-code")
	}
}

func TestOverrideZeroWarnsAndDoesNotCover(t *testing.T) {
	targets := []Target{FileTarget("/etc/shadow", AccessRead)}
	hook := sigkillHook("openat", 1, OperatorEqual, "/etc/shadow")
	hook.Action = ActionOverride
	hook.ReturnCode = intPtr(0)
	hooks := []Hook{hook}

	report, err := ValidateHooks(hooks, targets, DefaultFamilyTable())
	if err != nil {
		t.Fatalf("ValidateHooks returned error: %v", err)
	}
	if gapFor(report, "openat") == nil {
		t.Fatal("Override with return code 0 must not count as protection")
	}
	if !hasIssue(report, "override-return-zero") {
		t.Error("expected override-return-zero warning")
	}
}

func TestOverrideNonZeroCovers(t *testing.T) {
	targets := []Target{FileTarget("/etc/shadow", AccessRead)}
	hook := sigkillHook("openat", 1, OperatorEqual, "/etc/shadow")
	hook.Action = ActionOverride
	hook.ArgError = intPtr(-13)
	hooks := []Hook{hook}

	report, err := ValidateHooks(hooks, targets, DefaultFamilyTable())
	if err != nil {
		t.Fatalf("ValidateHooks returned error: %v", err)
	}
	if gapFor(report, "openat") != nil {
		t.Fatal("nonzero Override should cover the hook's path")
	}
}

func TestSelectorOnNonPathArgumentDoesNotCover(t *testing.T) {
	targets := []Target{FileTarget("/etc/shadow", AccessRead)}
	hooks := []Hook{{
		Call:      "openat",
		Args:      []Arg{{Index: 0, Type: "int"}, {Index: 1, Type: "string"}},
		Selectors: []Selector{{ArgIndex: 0, Operator: OperatorEqual, Value: "/etc/shadow"}},
		Action:    ActionSigkill,
	}}

	report, err := ValidateHooks(hooks, targets, DefaultFamilyTable())
	if err != nil {
		t.Fatalf("ValidateHooks returned error: %v", err)
	}
	if gapFor(report, "openat") == nil {
		t.Fatal("a selector on openat's dirfd argument must not cover its path argument")
	}
	if !hasIssue(report, "non-path-argument-selector") {
		t.Error("expected non-path-argument-selector warning")
	}
}

func TestCanonicalCall(t *testing.T) {
	cases := map[string]string{
		"sys_openat":            "openat",
		"__x64_sys_openat2":     "openat2",
		"__arm64_sys_renameat2": "renameat2",
		"do_sys_openat2":        "openat2",
		"do_renameat2":          "renameat2",
		"ksys_unlinkat":         "unlinkat",
	}
	for input, want := range cases {
		if got := CanonicalCall(input); got != want {
			t.Errorf("CanonicalCall(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCanonicalHookCallCoversFamilyEntry(t *testing.T) {
	targets := []Target{FileTarget("/etc/shadow", AccessRead)}
	hook := sigkillHook("__x64_sys_openat2", 1, OperatorEqual, "/etc/shadow")

	report, err := ValidateHooks([]Hook{hook}, targets, DefaultFamilyTable())
	if err != nil {
		t.Fatalf("ValidateHooks returned error: %v", err)
	}
	if gapFor(report, "openat2") != nil {
		t.Fatal("kernel-prefixed hook call should match its canonical family entry")
	}
}

func TestPrefixUsesComponentBoundary(t *testing.T) {
	targets := []Target{FileTarget("/etcetera/shadow", AccessRead)}
	hooks := []Hook{sigkillHook("openat", 1, OperatorPrefix, "/etc")}

	report, err := ValidateHooks(hooks, targets, DefaultFamilyTable())
	if err != nil {
		t.Fatalf("ValidateHooks returned error: %v", err)
	}
	if gapFor(report, "openat") == nil {
		t.Fatal("Prefix /etc must not match /etcetera")
	}
}

func TestEqualDoesNotCoverDirectoryContents(t *testing.T) {
	targets := []Target{DirectoryTarget("/etc", AccessRead)}
	hooks := []Hook{sigkillHook("openat", 1, OperatorEqual, "/etc")}

	report, err := ValidateHooks(hooks, targets, DefaultFamilyTable())
	if err != nil {
		t.Fatalf("ValidateHooks returned error: %v", err)
	}
	gap := gapFor(report, "openat")
	if gap == nil {
		t.Fatal("Equal on a directory must not be treated as recursive coverage")
	}
	if !strings.Contains(gap.Reason, "not its contents") {
		t.Fatalf("gap reason should mention directory contents, got %q", gap.Reason)
	}
}

func TestAccessParsing(t *testing.T) {
	access, err := ParseAccess("read, write | truncate")
	if err != nil {
		t.Fatalf("ParseAccess returned error: %v", err)
	}
	if !access.Has(AccessRead | AccessWrite | AccessTruncate) {
		t.Fatalf("ParseAccess returned %v", access)
	}
	if _, err := ParseAccess("execute-ish"); err == nil {
		t.Fatal("ParseAccess should reject an unknown mode")
	}
}
