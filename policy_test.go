package netsec

import "testing"

func TestEvaluateLastMatchingRuleWins(t *testing.T) {
	denyThenAllow := []Rule{
		NewRule(Deny, "file-read*", Subpath("/Users/alice/private")),
		NewRule(Allow, "file-read*", Subpath("/Users/alice")),
	}
	allowThenDeny := []Rule{
		NewRule(Allow, "file-read*", Subpath("/Users/alice")),
		NewRule(Deny, "file-read*", Subpath("/Users/alice/private")),
	}

	const op = "file-read-data"
	const path = "/Users/alice/private/notes.txt"
	if got := Evaluate(denyThenAllow, op, path); got != Allow {
		t.Fatalf("deny then allow: got %q, want %q", got, Allow)
	}
	if got := Evaluate(allowThenDeny, op, path); got != Deny {
		t.Fatalf("allow then deny: got %q, want %q", got, Deny)
	}
}

func TestEvaluateDefaultDeny(t *testing.T) {
	if got := Evaluate(nil, "file-read-data", "/tmp/x"); got != Deny {
		t.Fatalf("empty policy: got %q, want %q", got, Deny)
	}
	policy := New()
	if got := policy.Evaluate("process-exec", "/bin/sh"); got != Deny {
		t.Fatalf("unmatched policy: got %q, want %q", got, Deny)
	}
}

func TestSubpathComponentBoundary(t *testing.T) {
	rules := []Rule{NewRule(Allow, "file-read*", Subpath("/a/b"))}
	tests := []struct {
		path string
		want Effect
	}{
		{"/a/b", Allow},
		{"/a/b/file", Allow},
		{"/a/b/deep/file", Allow},
		{"/a/bc", Deny},
		{"/a/bc/file", Deny},
		{"/a", Deny},
	}
	for _, tc := range tests {
		if got := Evaluate(rules, "file-read-data", tc.path); got != tc.want {
			t.Errorf("subpath /a/b, path %q: got %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestLiteralIsExact(t *testing.T) {
	rules := []Rule{NewRule(Allow, "file-read-data", Literal("/a/b"))}
	if got := Evaluate(rules, "file-read-data", "/a/b"); got != Allow {
		t.Fatalf("exact literal: got %q, want %q", got, Allow)
	}
	for _, path := range []string{"/a/b/c", "/a/bc", "/a"} {
		if got := Evaluate(rules, "file-read-data", path); got != Deny {
			t.Errorf("literal must not match %q, got %q", path, got)
		}
	}
}

func TestRegexMatchesWholePath(t *testing.T) {
	rules := []Rule{NewRule(Allow, "file-read-data", Regex(`/a/b`))}
	if got := Evaluate(rules, "file-read-data", "/a/b"); got != Allow {
		t.Fatalf("regex exact path: got %q, want %q", got, Allow)
	}
	for _, path := range []string{"/a/b/c", "x/a/b", "/a/bx"} {
		if got := Evaluate(rules, "file-read-data", path); got != Deny {
			t.Errorf("regex /a/b must not match %q, got %q", path, got)
		}
	}

	suffixRules := []Rule{NewRule(Allow, "file-read-data", Regex(`^/safe/.*$`))}
	if got := Evaluate(suffixRules, "file-read-data", "/safe/deep/file"); got != Allow {
		t.Fatalf("anchored suffix regex: got %q, want %q", got, Allow)
	}
	if got := Evaluate(suffixRules, "file-read-data", "/safe"); got != Deny {
		t.Fatalf("anchored suffix regex matched too much: got %q, want %q", got, Deny)
	}
}

func TestOperationPrefixWildcard(t *testing.T) {
	rules := []Rule{
		NewRule(Allow, "file-read*", Literal("/read")),
		NewRule(Allow, "process-exec", Literal("/exec")),
	}
	tests := []struct {
		operation string
		path      string
		want      Effect
	}{
		{"file-read", "/read", Allow},
		{"file-read-data", "/read", Allow},
		{"file-read-metadata", "/read", Allow},
		{"file-write-data", "/read", Deny},
		{"process-exec", "/exec", Allow},
		{"process-exec-sandboxed", "/exec", Deny},
	}
	for _, tc := range tests {
		if got := Evaluate(rules, tc.operation, tc.path); got != tc.want {
			t.Errorf("operation %q, path %q: got %q, want %q",
				tc.operation, tc.path, got, tc.want)
		}
	}
}

func TestCheckReportsWhetherAnyRuleMatched(t *testing.T) {
	rules := []Rule{NewRule(Deny, "file-read-data", Literal("/secret"))}
	if got, matched := Check(rules, "file-read-data", "/secret"); got != Deny || !matched {
		t.Fatalf("matching rule: got (%q, %v), want (%q, true)", got, matched, Deny)
	}
	if got, matched := Check(rules, "file-write-data", "/other"); got != Deny || matched {
		t.Fatalf("unmatched rule: got (%q, %v), want (%q, false)", got, matched, Deny)
	}
}

func TestInvalidRulesAreRejectedByValidationAndIgnoredByEvaluation(t *testing.T) {
	invalid := NewRule(Deny, "file-read-data", Regex(`[`))
	if got := Evaluate([]Rule{NewRule(Allow, "file-read-data", Literal("/x")), invalid}, "file-read-data", "/x"); got != Allow {
		t.Fatalf("invalid regex rule changed result: got %q, want %q", got, Allow)
	}
	policy := NewPolicy([]Rule{invalid})
	if err := policy.Validate(); err == nil {
		t.Fatal("expected invalid regex to fail validation")
	}
}
