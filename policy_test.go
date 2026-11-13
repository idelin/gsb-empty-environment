package netsec

import (
	"strings"
	"testing"
)

func mustParse(t *testing.T, source string) *Policy {
	t.Helper()
	policy, err := Parse(source)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	return policy
}

func TestWildcardMatchesSlashAndSpace(t *testing.T) {
	tests := []struct {
		pattern string
		value   string
		want    bool
	}{
		{pattern: "*", value: "anything/at all", want: true},
		{pattern: "/var/*", value: "/var/log/app.log", want: true},
		{pattern: "a*b", value: "a/b with space b", want: true},
		{pattern: "a?c", value: "a/c", want: true},
		{pattern: "[a-c]x", value: "bx", want: true},
		{pattern: `\*`, value: "*", want: true},
		{pattern: `\*`, value: "x", want: false},
		{pattern: "abc", value: "abc", want: true},
		{pattern: "abc", value: "ABC", want: false},
	}

	for _, test := range tests {
		if got := fnmatch(test.pattern, test.value); got != test.want {
			t.Errorf("fnmatch(%q, %q) = %v, want %v", test.pattern, test.value, got, test.want)
		}
	}
}

func TestParentDirectoryIsRejectedOnlyForWildcardPatterns(t *testing.T) {
	policy := mustParse(t, `
alice host1=(root) /bin/cat *
alice host1=(root) /bin/cat ../safe
`)

	traversal := Request{
		User:    "alice",
		Host:    "host1",
		RunAs:   "root",
		Program: "/bin/cat",
		Args:    []string{"../../etc/shadow"},
	}
	if decision := policy.Authorize(traversal); decision.Allowed {
		t.Fatalf("wildcard traversal was allowed: %+v", decision)
	}

	literal := Request{
		User:    "alice",
		Host:    "host1",
		RunAs:   "root",
		Program: "/bin/cat",
		Args:    []string{"../safe"},
	}
	if decision := policy.Authorize(literal); !decision.Allowed {
		t.Fatalf("literal traversal path was denied: %+v", decision)
	}

	otherWildcard := mustParse(t, "alice host1=(root) /bin/cat ../?\n")
	if decision := otherWildcard.Authorize(Request{
		User: "alice", Host: "host1", RunAs: "root",
		Program: "/bin/cat", Args: []string{"../.."},
	}); decision.Allowed {
		t.Fatalf("parent directory was allowed through a non-star wildcard: %+v", decision)
	}

	anyArgs := mustParse(t, "alice host1=(root) /bin/cat\n")
	if decision := anyArgs.Authorize(Request{
		User: "alice", Host: "host1", RunAs: "root",
		Program: "/bin/cat", Args: []string{"../x"},
	}); decision.Allowed {
		t.Fatalf("parent directory was allowed through implicit AnyArgs: %+v", decision)
	}
}

func TestLiteralOptionUsesOptionBoundary(t *testing.T) {
	tests := []struct {
		pattern string
		option  string
		want    bool
	}{
		{pattern: "--foobar", option: "-foobar", want: false},
		{pattern: "x-foo", option: "-foo", want: false},
		{pattern: `\--foo`, option: "--foo", want: true},
		{pattern: "*--foo*", option: "--foo", want: true},
		{pattern: "commit --foo *", option: "--foo", want: true},
	}
	for _, test := range tests {
		if got := patternContainsLiteralOption(test.pattern, test.option); got != test.want {
			t.Errorf("patternContainsLiteralOption(%q, %q) = %v, want %v",
				test.pattern, test.option, got, test.want)
		}
	}
}

func TestExtraOptionsAreRejectedUnlessSpelledLiterally(t *testing.T) {
	policy := mustParse(t, `
alice host1=(root) /usr/bin/git commit *
alice host1=(root) /usr/bin/git commit --amend *
`)

	extraOption := Request{
		User:    "alice",
		Host:    "host1",
		RunAs:   "root",
		Program: "/usr/bin/git",
		Args:    []string{"commit", "--amend", "message"},
	}
	if decision := policy.Authorize(extraOption); !decision.Allowed {
		t.Fatalf("explicitly allowed option was denied: %+v", decision)
	}

	unknownOption := Request{
		User:    "alice",
		Host:    "host1",
		RunAs:   "root",
		Program: "/usr/bin/git",
		Args:    []string{"commit", "--exec", "payload"},
	}
	if decision := policy.Authorize(unknownOption); decision.Allowed {
		t.Fatalf("injected option was allowed: %+v", decision)
	}

	noPattern := mustParse(t, "alice host1=(root) /usr/bin/env\n")
	request := Request{
		User:    "alice",
		Host:    "host1",
		RunAs:   "root",
		Program: "/usr/bin/env",
		Args:    []string{"-i"},
	}
	if decision := noPattern.Authorize(request); decision.Allowed {
		t.Fatalf("option was allowed without a literal option in the pattern: %+v", decision)
	}
}

func TestEmptyAndQuotedArgumentPatterns(t *testing.T) {
	policy := mustParse(t, `
alice host1=(root) /bin/echo ""
alice host1=(root) /bin/printf "a b"
alice host1=(root) /bin/uname
`)

	noArguments := Request{
		User: "alice", Host: "host1", RunAs: "root",
		Program: "/bin/echo",
	}
	if decision := policy.Authorize(noArguments); !decision.Allowed {
		t.Fatalf("empty argument list was denied: %+v", decision)
	}
	if decision := policy.Authorize(Request{
		User: "alice", Host: "host1", RunAs: "root",
		Program: "/bin/echo", Args: []string{""},
	}); decision.Allowed {
		t.Fatalf("a single empty argument bypassed the empty argument pattern: %+v", decision)
	}
	if decision := policy.Authorize(Request{
		User: "alice", Host: "host1", RunAs: "root",
		Program: "/bin/echo", Args: []string{"x"},
	}); decision.Allowed {
		t.Fatalf("unexpected argument was allowed: %+v", decision)
	}

	quoted := Request{
		User: "alice", Host: "host1", RunAs: "root",
		Program: "/bin/printf", Args: []string{"a", "b"},
	}
	if decision := policy.Authorize(quoted); !decision.Allowed {
		t.Fatalf("joined arguments did not match quoted pattern: %+v", decision)
	}
	if decision := policy.Authorize(Request{
		User: "alice", Host: "host1", RunAs: "root",
		Program: "/bin/printf", Args: []string{"a b", "c"},
	}); decision.Allowed {
		t.Fatalf("arguments outside quoted pattern were allowed: %+v", decision)
	}

	anyArguments := Request{
		User: "alice", Host: "host1", RunAs: "root",
		Program: "/bin/uname", Args: []string{"-a"},
	}
	if decision := policy.Authorize(anyArguments); decision.Allowed {
		t.Fatalf("AnyArgs pattern allowed an unlisted option: %+v", decision)
	}
}

func TestNegationAndLastMatchWins(t *testing.T) {
	policy := mustParse(t, `
alice host1=(root) /bin/cat *
alice host1=(root) !/bin/cat /etc/shadow
alice host1=(root) /bin/cat /etc/shadow
`)

	request := Request{
		User: "alice", Host: "host1", RunAs: "root",
		Program: "/bin/cat", Args: []string{"/etc/shadow"},
	}
	decision := policy.Authorize(request)
	if !decision.Allowed || decision.RuleIndex != 2 {
		t.Fatalf("later allow did not override negation: %+v", decision)
	}

	latestDeny := mustParse(t, `
alice host1=(root) /bin/cat *
alice host1=(root) /bin/cat /etc/shadow
alice host1=(root) !/bin/cat /etc/shadow
`)
	decision = latestDeny.Authorize(request)
	if decision.Allowed || !decision.Matched || decision.RuleIndex != 2 {
		t.Fatalf("last matching negation did not win: %+v", decision)
	}

	if decision := latestDeny.Authorize(Request{
		User: "alice", Host: "host1", RunAs: "root",
		Program: "/bin/cat", Args: []string{"/etc/hosts"},
	}); !decision.Allowed || decision.RuleIndex != 0 {
		t.Fatalf("unrelated non-matching denial changed the earlier allow: %+v", decision)
	}

	broadDeny := mustParse(t, `
alice host1=(root) /bin/cat *
alice host1=(root) !/bin/cat *
`)
	for _, arguments := range [][]string{{"-n"}, {"../etc/passwd"}} {
		if decision := broadDeny.Authorize(Request{
			User: "alice", Host: "host1", RunAs: "root",
			Program: "/bin/cat", Args: arguments,
		}); decision.Allowed || decision.RuleIndex != 1 {
			t.Fatalf("broad negative rule was skipped for %v: %+v", arguments, decision)
		}
	}
}

func TestCommandAliasPreservesDuplicateProgramPatterns(t *testing.T) {
	policy := mustParse(t, `
Cmnd_Alias CAT_COMMANDS = /bin/cat *, /bin/cat /etc/hosts, !/bin/cat /etc/shadow
alice host1=(root) CAT_COMMANDS
`)
	if len(policy.Rules) != 3 {
		t.Fatalf("expanded rule count = %d, want 3", len(policy.Rules))
	}
	if policy.Rules[0].Program != "/bin/cat" ||
		policy.Rules[1].Program != "/bin/cat" ||
		policy.Rules[2].Program != "/bin/cat" {
		t.Fatalf("duplicate alias program was lost: %+v", policy.Rules)
	}
	if policy.Rules[0].ArgPattern != "*" ||
		policy.Rules[1].ArgPattern != "/etc/hosts" ||
		policy.Rules[2].ArgPattern != "/etc/shadow" {
		t.Fatalf("alias argument patterns were not preserved: %+v", policy.Rules)
	}
	if policy.Authorize(Request{
		User: "alice", Host: "host1", RunAs: "root",
		Program: "/bin/cat", Args: []string{"/etc/shadow"},
	}).Allowed {
		t.Fatal("negative alias command did not deny the request")
	}
}

func TestProgramMustMatchLiteralAbsolutePath(t *testing.T) {
	policy := mustParse(t, "alice host1=(root) /usr/bin/cat *\n")

	decision := policy.Authorize(Request{
		User: "alice", Host: "host1", RunAs: "root",
		Program: "/usr/bin/cat", Args: []string{"/etc/hosts"},
	})
	if !decision.Allowed {
		t.Fatalf("literal matching program was denied: %+v", decision)
	}

	for _, program := range []string{"/usr/bin/../usr/bin/cat", "/usr/bin/cat ", "usr/bin/cat"} {
		decision := policy.Authorize(Request{
			User: "alice", Host: "host1", RunAs: "root",
			Program: program, Args: []string{"/etc/hosts"},
		})
		if decision.Allowed {
			t.Errorf("non-literal program %q was allowed", program)
		}
	}
}

func TestEscapedSpaceInProgramPath(t *testing.T) {
	policy := mustParse(t, "alice host1=(root) /opt/my\\ program *\n")
	if policy.Rules[0].Program != "/opt/my program" {
		t.Fatalf("program path was not unescaped: %q", policy.Rules[0].Program)
	}
	if !policy.Allowed(Request{
		User: "alice", Host: "host1", RunAs: "root",
		Program: "/opt/my program", Args: []string{"safe"},
	}) {
		t.Fatal("escaped program path did not match the literal request path")
	}
}

func TestParseRejectsUnsafeOrInvalidRules(t *testing.T) {
	tests := []string{
		"alice host1=(root) bin/cat *",
		"alice host1=(root) /usr/bin/cat* *",
		"alice host1=(root) /bin/cat \"unterminated",
		"alice host1=(root) MISSING_ALIAS",
		"Cmnd_Alias LOOP = LOOP\n",
	}
	for _, source := range tests {
		if _, err := Parse(source); err == nil {
			t.Errorf("Parse(%q) succeeded, want an error", source)
		}
	}
}

func TestSubjectMatching(t *testing.T) {
	policy := mustParse(t, `
alice host1=(root) /bin/true ""
ALL host1=(ALL) /bin/echo *
`)

	for _, request := range []Request{
		{User: "alice", Host: "host1", RunAs: "root", Program: "/bin/true"},
		{User: "bob", Host: "host1", RunAs: "root", Program: "/bin/echo", Args: []string{"ok"}},
	} {
		if decision := policy.Authorize(request); !decision.Allowed {
			t.Errorf("subject match failed for %+v: %+v", request, decision)
		}
	}

	wrongHost := Request{
		User: "alice", Host: "host2", RunAs: "root", Program: "/bin/true",
	}
	if decision := policy.Authorize(wrongHost); decision.Allowed {
		t.Fatalf("host mismatch was allowed: %+v", decision)
	}
}

func TestCommentsAndQuotedHash(t *testing.T) {
	policy := mustParse(t, `
# leading comment
alice host1=(root) /bin/echo "a#b" # trailing comment
`)
	if policy.Rules[0].ArgPattern != "a#b" {
		t.Fatalf("quoted hash was stripped: %q", policy.Rules[0].ArgPattern)
	}
	if !policy.Allowed(Request{
		User: "alice", Host: "host1", RunAs: "root",
		Program: "/bin/echo", Args: []string{"a#b"},
	}) {
		t.Fatal("quoted hash pattern did not match")
	}
}

func TestSingleQuoteIsLiteralInsideDoubleQuotes(t *testing.T) {
	policy := mustParse(t, "alice host1=(root) /bin/echo \"don't\"\n")
	if policy.Rules[0].ArgPattern != "don't" {
		t.Fatalf("apostrophe was treated as a quote delimiter: %q", policy.Rules[0].ArgPattern)
	}
	if !policy.Allowed(Request{
		User: "alice", Host: "host1", RunAs: "root",
		Program: "/bin/echo", Args: []string{"don't"},
	}) {
		t.Fatal("apostrophe pattern did not match")
	}
}

func TestNoMatchHasNoMatchedRule(t *testing.T) {
	policy := mustParse(t, "alice host1=(root) /bin/true \"\"\n")
	decision := policy.Authorize(Request{
		User: "alice", Host: "host1", RunAs: "root", Program: "/bin/false",
	})
	if decision.Allowed || decision.Matched || decision.RuleIndex != -1 {
		t.Fatalf("unexpected no-match decision: %+v", decision)
	}
	if !strings.Contains(decision.Reason, "no matching") {
		t.Fatalf("unexpected no-match reason: %q", decision.Reason)
	}
}

func TestAuthorizeConvenience(t *testing.T) {
	decision, err := Authorize("alice host1=(root) /bin/true \"\"\n", Request{
		User: "alice", Host: "host1", RunAs: "root", Program: "/bin/true",
	})
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("Authorize() denied valid request: %+v", decision)
	}
}
