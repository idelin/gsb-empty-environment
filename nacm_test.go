package netsec

import "testing"

func TestFirstMatchingRuleWins(t *testing.T) {
	permitFirst := Policy{
		RuleLists: []RuleList{{
			Groups: []string{"operators"},
			Rules: []Rule{
				{Path: "/system", Action: Permit},
				{Path: "/system", Action: Deny},
			},
		}},
	}
	denyFirst := Policy{
		RuleLists: []RuleList{{
			Groups: []string{"operators"},
			Rules: []Rule{
				{Path: "/system", Action: Deny},
				{Path: "/system", Action: Permit},
			},
		}},
	}

	if got := permitFirst.Check([]string{"operators"}, "/system/clock", Update, false); got != Permit {
		t.Fatalf("permit-first decision = %v, want permit", got)
	}
	if got := denyFirst.Check([]string{"operators"}, "/system/clock", Update, false); got != Deny {
		t.Fatalf("deny-first decision = %v, want deny", got)
	}
}

func TestRuleListOrderWinsAcrossGroups(t *testing.T) {
	policy := Policy{
		RuleLists: []RuleList{
			{
				Name:   "operators-deny",
				Groups: []string{"operators"},
				Rules:  []Rule{{Path: "/system", Action: Deny}},
			},
			{
				Name:   "admins-permit",
				Groups: []string{"admins"},
				Rules:  []Rule{{Path: "/system", Action: Permit}},
			},
		},
		WriteDefault: Permit,
	}

	if got := policy.Check([]string{"operators", "admins"}, "/system/clock", Update, false); got != Deny {
		t.Fatalf("multi-group decision = %v, want deny from the first applicable list", got)
	}
	if got := policy.Check([]string{"admins"}, "/system/clock", Update, false); got != Permit {
		t.Fatalf("admin decision = %v, want permit", got)
	}
}

func TestRuleListSelectionUsesGroupMembership(t *testing.T) {
	policy := Policy{
		RuleLists: []RuleList{
			{Groups: []string{"admins"}, Rules: []Rule{{Path: "/", Action: Permit}}},
			{Groups: nil, Rules: []Rule{{Path: "/", Action: Permit}}},
			{Groups: []string{""}, Rules: []Rule{{Path: "/", Action: Permit}}},
		},
		ReadDefault: Deny,
	}

	if got := policy.Check([]string{"operators"}, "/system", Read, false); got != Deny {
		t.Fatalf("unrelated group decision = %v, want deny from default", got)
	}
	if got := policy.Check(nil, "/system", Read, false); got != Deny {
		t.Fatalf("no group decision = %v, want deny from default", got)
	}

	applicable := policy.RuleListsFor([]string{"admins"})
	if len(applicable) != 1 || applicable[0].Groups[0] != "admins" {
		t.Fatalf("RuleListsFor returned %#v, want only the admin list", applicable)
	}
}

func TestPathRulesCoverSubtrees(t *testing.T) {
	policy := Policy{
		RuleLists: []RuleList{{
			Groups: []string{"monitoring"},
			Rules:  []Rule{{Path: "/system/settings", Operations: OpRead, Action: Permit}},
		}},
		ReadDefault: Deny,
	}

	tests := []struct {
		path string
		want Action
	}{
		{"/system/settings", Permit},
		{"/system/settings/ntp", Permit},
		{"/system/settings/ntp/server", Permit},
		{"/system/settings2", Deny},
		{"/system", Deny},
		{"/systematic/settings", Deny},
	}
	for _, test := range tests {
		if got := policy.Check([]string{"monitoring"}, test.path, Read, false); got != test.want {
			t.Errorf("path %q decision = %v, want %v", test.path, got, test.want)
		}
	}
}

func TestRootRuleCoversEveryPath(t *testing.T) {
	policy := Policy{
		RuleLists: []RuleList{{
			Groups: []string{"admins"},
			Rules:  []Rule{{Path: "/", Action: Permit}},
		}},
		ReadDefault: Deny,
	}

	for _, path := range []string{"/", "/system", "/system/users/alice"} {
		if got := policy.Check([]string{"admins"}, path, Read, false); got != Permit {
			t.Errorf("root rule decision for %q = %v, want permit", path, got)
		}
	}
}

func TestOperationsMatchIndependently(t *testing.T) {
	policy := Policy{
		RuleLists: []RuleList{{
			Groups: []string{"operators"},
			Rules: []Rule{
				{Path: "/system", Operations: OpRead, Action: Permit},
				{Path: "/system", Operations: OpUpdate, Action: Deny},
			},
		}},
		ReadDefault:  Deny,
		WriteDefault: Permit,
		ExecDefault:  Deny,
	}

	tests := []struct {
		op   Operation
		want Action
	}{
		{Read, Permit},
		{Create, Permit},
		{Update, Deny},
		{Delete, Permit},
		{Exec, Deny},
	}
	for _, test := range tests {
		if got := policy.Check([]string{"operators"}, "/system/clock", test.op, false); got != test.want {
			t.Errorf("operation %v decision = %v, want %v", test.op, got, test.want)
		}
	}
}

func TestOmittedOperationsMeansAll(t *testing.T) {
	policy := Policy{
		RuleLists: []RuleList{{
			Groups: []string{"operators"},
			Rules:  []Rule{{Path: "/system", Action: Permit}},
		}},
		WriteDefault: Deny,
	}

	if got := policy.Check([]string{"operators"}, "/system/clock", Update, false); got != Permit {
		t.Fatalf("zero-value operations decision = %v, want permit", got)
	}
}

func TestDefaultsUseThreeClasses(t *testing.T) {
	policy := Policy{ReadDefault: Permit, WriteDefault: Deny, ExecDefault: Permit}

	tests := []struct {
		op   Operation
		want Action
	}{
		{Read, Permit},
		{Create, Deny},
		{Update, Deny},
		{Delete, Deny},
		{Exec, Permit},
	}
	for _, test := range tests {
		if got := policy.Check([]string{"unlisted"}, "/system", test.op, false); got != test.want {
			t.Errorf("default for %v = %v, want %v", test.op, got, test.want)
		}
	}
}

func TestDefaultDenyAllDisablesDefaultPermits(t *testing.T) {
	policy := Policy{
		ReadDefault:  Permit,
		WriteDefault: Permit,
		ExecDefault:  Permit,
	}

	for _, op := range []Operation{Read, Create, Update, Delete, Exec} {
		if got := policy.Check([]string{"operators"}, "/system", op, true); got != Deny {
			t.Errorf("default-deny-all operation %v = %v, want deny", op, got)
		}
	}
}

func TestDefaultDenyAllStillAllowsExplicitPermit(t *testing.T) {
	policy := Policy{
		RuleLists: []RuleList{{
			Groups: []string{"operators"},
			Rules:  []Rule{{Path: "/system", Operations: OpRead, Action: Permit}},
		}},
		ReadDefault: Deny,
	}

	if got := policy.Check([]string{"operators"}, "/system/clock", Read, true); got != Permit {
		t.Fatalf("explicit permit on default-deny-all node = %v, want permit", got)
	}
	if got := policy.Check([]string{"operators"}, "/system/clock", Update, true); got != Deny {
		t.Fatalf("non-matching operation on default-deny-all node = %v, want deny", got)
	}
}

func TestDefaultDenyWriteOnlyDisablesWriteDefault(t *testing.T) {
	policy := Policy{ReadDefault: Permit, WriteDefault: Permit, ExecDefault: Permit}
	node := Node{Path: "/system", DefaultDenyWrite: true}

	for _, op := range []Operation{Create, Update, Delete} {
		if got := policy.CheckNode([]string{"operators"}, node, op); got != Deny {
			t.Errorf("default-deny-write operation %v = %v, want deny", op, got)
		}
	}
	if got := policy.CheckNode([]string{"operators"}, node, Read); got != Permit {
		t.Fatalf("read on default-deny-write node = %v, want permit", got)
	}
}

func TestPathWildcardsAndPredicates(t *testing.T) {
	policy := Policy{
		RuleLists: []RuleList{{
			Groups: []string{"operators"},
			Rules: []Rule{
				{Path: "/interfaces/*", Operations: OpRead, Action: Permit},
				{Path: "/interfaces/interface[name='eth0']", Operations: OpUpdate, Action: Permit},
			},
		}},
		ReadDefault:  Deny,
		WriteDefault: Deny,
	}

	tests := []struct {
		path string
		op   Operation
		want Action
	}{
		{"/interfaces/eth0/status", Read, Permit},
		{"/interfaces/eth0", Read, Permit},
		{"/interfaces", Read, Deny},
		{"/interfaces/interface[name='eth0']/enabled", Update, Permit},
		{"/interfaces/interface[name='eth0'][type='ethernet']", Update, Permit},
		{"/interfaces/interface[name='eth1']", Update, Deny},
	}
	for _, test := range tests {
		if got := policy.Check([]string{"operators"}, test.path, test.op, false); got != test.want {
			t.Errorf("%v %q decision = %v, want %v", test.op, test.path, got, test.want)
		}
	}
}

func TestMalformedOrUnknownRequestsFailClosed(t *testing.T) {
	policy := Policy{ReadDefault: Permit, WriteDefault: Permit, ExecDefault: Permit}

	for _, op := range []Operation{Operation(99), Operation(200)} {
		if got := policy.Check([]string{"operators"}, "/system", op, false); got != Deny {
			t.Errorf("unknown operation %d = %v, want deny", op, got)
		}
	}

	for _, path := range []string{"/system/a]", "/system/[name='unterminated"} {
		if got := policy.Check([]string{"operators"}, path, Read, false); got != Deny {
			t.Errorf("malformed path %q = %v, want deny", path, got)
		}
	}
}

func TestBooleanAndPackageLevelHelpers(t *testing.T) {
	policy := Policy{
		RuleLists: []RuleList{{
			Groups: []string{"operators"},
			Rules:  []Rule{{Path: "/system", Action: Permit}},
		}},
	}

	if !policy.Allows([]string{"operators"}, "/system", Read, false) {
		t.Fatal("policy.Allows returned false, want true")
	}
	if decision := Check(policy, []string{"operators"}, "/system", Read, false); decision != Permit {
		t.Fatalf("Check = %v, want permit", decision)
	}
	if !Allows(policy, []string{"operators"}, "/system", Read, false) {
		t.Fatal("package-level Allows returned false, want true")
	}
}
