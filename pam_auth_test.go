package netsec

import (
	"strings"
	"testing"
)

func TestSufficientPermitBeforePasswordCanBypass(t *testing.T) {
	config := strings.NewReader(`
auth	sufficient	pam_permit.so
auth	required	pam_unix.so
`)

	rules, err := ParseAuthStack(config)
	if err != nil {
		t.Fatalf("ParseAuthStack returned error: %v", err)
	}

	bypasses := PasswordBypassPaths(rules)
	if len(bypasses) != 1 {
		t.Fatalf("expected one bypass path, got %d", len(bypasses))
	}

	path := bypasses[0]
	if path.Decision != AuthPass {
		t.Fatalf("expected bypass decision %q, got %q", AuthPass, path.Decision)
	}
	if path.Outcomes[0] != ModuleSuccess || path.Outcomes[1] != ModuleFailure {
		t.Fatalf("expected permit success and unix failure, got %v", path.Outcomes)
	}
	if path.Reached[1] {
		t.Fatalf("expected sufficient success to skip the password module")
	}
	if got := path.ActualOutcomes()[1]; got != ModuleSkipped {
		t.Fatalf("expected actual password outcome %q, got %q", ModuleSkipped, got)
	}
}

func TestRequisiteFailureStopsAndDenies(t *testing.T) {
	rules := mustParseRules(t, `
auth	requisite	pam_permit.so
auth	required	pam_unix.so
`)

	path, err := EvaluateAuthStack(rules, []Outcome{ModuleFailure, ModuleSuccess})
	if err != nil {
		t.Fatalf("EvaluateAuthStack returned error: %v", err)
	}
	if path.Decision != AuthDeny {
		t.Fatalf("expected deny, got %q", path.Decision)
	}
	if path.Reached[1] {
		t.Fatalf("expected requisite failure to stop the stack")
	}

	if bypasses := PasswordBypassPaths(rules); len(bypasses) != 0 {
		t.Fatalf("expected no password bypasses, got %d", len(bypasses))
	}
}

func TestRequisiteSuccessContinuesStack(t *testing.T) {
	rules := mustParseRules(t, `
auth	requisite	pam_permit.so
auth	required	pam_unix.so
`)

	path, err := EvaluateAuthStack(rules, []Outcome{ModuleSuccess, ModuleSuccess})
	if err != nil {
		t.Fatalf("EvaluateAuthStack returned error: %v", err)
	}
	if path.Decision != AuthPass {
		t.Fatalf("expected successful requisite and required to pass, got %q", path.Decision)
	}
	if !path.Reached[1] {
		t.Fatalf("expected requisite success to continue to the password module")
	}
}

func TestExplicitSuccessSkipAllowsPasswordFailurePath(t *testing.T) {
	rules := mustParseRules(t, `
auth	[success=1 default=bad]	pam_permit.so
auth	required	pam_unix.so
auth	required	pam_permit.so
`)

	path, err := EvaluateAuthStack(rules, []Outcome{ModuleSuccess, ModuleFailure, ModuleSuccess})
	if err != nil {
		t.Fatalf("EvaluateAuthStack returned error: %v", err)
	}
	if path.Decision != AuthPass {
		t.Fatalf("expected later required success to pass after the jump, got %q", path.Decision)
	}
	if path.Reached[1] {
		t.Fatalf("expected success=1 to skip one module")
	}
	if !path.Reached[2] {
		t.Fatalf("expected success=1 to resume on the following module")
	}

	bypasses := PasswordBypassPaths(rules)
	if len(bypasses) != 1 {
		t.Fatalf("expected one bypass path, got %d", len(bypasses))
	}
}

func TestExplicitJumpDoesNotItselfPassStack(t *testing.T) {
	rules := mustParseRules(t, `
auth	[success=1 default=bad]	pam_permit.so
auth	required	pam_unix.so
`)

	path, err := EvaluateAuthStack(rules, []Outcome{ModuleSuccess, ModuleFailure})
	if err != nil {
		t.Fatalf("EvaluateAuthStack returned error: %v", err)
	}
	if path.Decision != AuthDeny {
		t.Fatalf("expected jump alone not to pass the stack, got %q", path.Decision)
	}
}

func TestRequiredFailureContinuesAndFinallyDenies(t *testing.T) {
	rules := mustParseRules(t, `
auth	required	pam_unix.so
auth	sufficient	pam_permit.so
`)

	path, err := EvaluateAuthStack(rules, []Outcome{ModuleFailure, ModuleSuccess})
	if err != nil {
		t.Fatalf("EvaluateAuthStack returned error: %v", err)
	}
	if path.Decision != AuthDeny {
		t.Fatalf("required failure must produce final deny, got %q", path.Decision)
	}
	if !path.Reached[1] {
		t.Fatalf("expected required failure to continue to later modules")
	}

	if bypasses := PasswordBypassPaths(rules); len(bypasses) != 0 {
		t.Fatalf("expected no password bypasses, got %d", len(bypasses))
	}
}

func TestEnumeratesEverySuccessFailureCombination(t *testing.T) {
	rules := mustParseRules(t, `
auth	optional	pam_permit.so
auth	required	pam_unix.so
auth	sufficient	pam_deny.so
`)

	paths := EnumerateAuthPaths(rules)
	if len(paths) != 1<<len(rules) {
		t.Fatalf("expected %d enumerated paths, got %d", 1<<len(rules), len(paths))
	}

	seen := make(map[[3]Outcome]bool)
	for _, path := range paths {
		var key [3]Outcome
		copy(key[:], path.Outcomes)
		if seen[key] {
			t.Fatalf("duplicate enumerated path: %v", path.Outcomes)
		}
		seen[key] = true
	}
}

func TestEmptyAuthStackDenies(t *testing.T) {
	paths := EnumerateAuthPaths(nil)
	if len(paths) != 1 {
		t.Fatalf("expected one empty-stack path, got %d", len(paths))
	}
	if paths[0].Decision != AuthDeny {
		t.Fatalf("expected empty auth stack to deny, got %q", paths[0].Decision)
	}
}

func TestParseExplicitControl(t *testing.T) {
	control, err := ParseControl("[success=1 default=bad]")
	if err != nil {
		t.Fatalf("ParseControl returned error: %v", err)
	}
	if control.Kind != Explicit {
		t.Fatalf("expected explicit control, got %d", control.Kind)
	}
	if got := control.Actions["success"]; got.Kind != JumpAction || got.Skip != 1 {
		t.Fatalf("unexpected success action: %+v", got)
	}
	if got := control.Actions["default"]; got.Kind != BadAction {
		t.Fatalf("unexpected default action: %+v", got)
	}
}

func TestExplicitControlWithoutDefaultTreatsFailureAsBad(t *testing.T) {
	rules := mustParseRules(t, `
auth	[success=1]	pam_permit.so
auth	required	pam_unix.so
`)

	path, err := EvaluateAuthStack(rules, []Outcome{ModuleFailure, ModuleSuccess})
	if err != nil {
		t.Fatalf("EvaluateAuthStack returned error: %v", err)
	}
	if path.Decision != AuthDeny {
		t.Fatalf("expected implicit default=bad to deny, got %q", path.Decision)
	}
}

func TestOptionalOnlyMattersAsSoleModule(t *testing.T) {
	single := mustParseRules(t, `auth optional pam_unix.so`)

	failed, err := EvaluateAuthStack(single, []Outcome{ModuleFailure})
	if err != nil {
		t.Fatalf("EvaluateAuthStack returned error: %v", err)
	}
	if failed.Decision != AuthDeny {
		t.Fatalf("expected sole optional failure to deny, got %q", failed.Decision)
	}

	passed, err := EvaluateAuthStack(single, []Outcome{ModuleSuccess})
	if err != nil {
		t.Fatalf("EvaluateAuthStack returned error: %v", err)
	}
	if passed.Decision != AuthPass {
		t.Fatalf("expected sole optional success to pass, got %q", passed.Decision)
	}

	multiple := mustParseRules(t, `
auth	optional	pam_permit.so
auth	optional	pam_unix.so
`)
	mixed, err := EvaluateAuthStack(multiple, []Outcome{ModuleSuccess, ModuleFailure})
	if err != nil {
		t.Fatalf("EvaluateAuthStack returned error: %v", err)
	}
	if mixed.Decision != AuthDeny {
		t.Fatalf("expected optional failure to deny when another optional exists, got %q", mixed.Decision)
	}
}

func mustParseRules(t *testing.T, config string) []Rule {
	t.Helper()
	rules, err := ParseAuthConfig(config)
	if err != nil {
		t.Fatalf("ParseAuthConfig returned error: %v", err)
	}
	return rules
}
