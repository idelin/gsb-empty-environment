package binding

import (
	"reflect"
	"testing"
)

func TestStrongDefinitionOverridesLaterAndEarlierWeakDefinitions(t *testing.T) {
	modules := []Module{
		{ID: "source", Trusted: true, Dependencies: []string{"weak-auth"}},
		{
			ID:           "weak-auth",
			Trusted:      true,
			Dependencies: []string{"strong-auth"},
			Exports: []Export{{
				Name:     "auth",
				Version:  "1",
				Strength: Weak,
			}},
		},
		{
			ID:      "strong-auth",
			Trusted: false,
			Exports: []Export{{
				Name:     "auth",
				Version:  "1",
				Strength: Strong,
			}},
		},
	}
	relocation := Relocation{
		SourceID:        "source",
		Name:            "auth",
		Version:         "1",
		AllowPreemption: true,
		RequireTrusted:  true,
	}

	result, err := Bind(modules, []string{"weak-auth", "strong-auth", "source"}, relocation)
	if err != nil {
		t.Fatalf("Bind returned error: %v", err)
	}
	if !result.Found {
		t.Fatalf("expected a binding, got reason %q", result.Reason)
	}
	if result.WinnerModuleID != "strong-auth" {
		t.Fatalf("expected strong-auth to win, got %q", result.WinnerModuleID)
	}
	if len(result.StrongCandidates) != 1 || len(result.WeakCandidates) != 1 {
		t.Fatalf("expected one strong and one weak candidate, got %d strong and %d weak", len(result.StrongCandidates), len(result.WeakCandidates))
	}
	if !result.TrustViolation || result.DefinerTrusted {
		t.Fatalf("expected trust violation by untrusted strong definition")
	}
}

func TestNoPreemptionChecksLocalStrongDefinitionFirst(t *testing.T) {
	modules := []Module{
		{
			ID:      "source",
			Trusted: true,
			Exports: []Export{{
				Name:     "auth",
				Version:  "1",
				Strength: Strong,
			}},
		},
		{
			ID:      "plugin",
			Trusted: false,
			Exports: []Export{{
				Name:     "auth",
				Version:  "1",
				Strength: Strong,
			}},
		},
	}
	relocation := Relocation{
		SourceID:        "source",
		Name:            "auth",
		Version:         "1",
		AllowPreemption: false,
		RequireTrusted:  true,
	}

	result, err := Bind(modules, []string{"plugin", "source"}, relocation)
	if err != nil {
		t.Fatalf("Bind returned error: %v", err)
	}
	if result.WinnerModuleID != "source" || result.TrustViolation {
		t.Fatalf("expected trusted local source to win, got module %q violation=%v", result.WinnerModuleID, result.TrustViolation)
	}
	if result.SearchOrder[0] != "source" {
		t.Fatalf("expected source first without preemption, got order %v", result.SearchOrder)
	}
}

func TestNoPreemptionSearchesLocalBeforeExternalStrongDefinition(t *testing.T) {
	modules := []Module{
		{
			ID:      "source",
			Trusted: true,
			Exports: []Export{{Name: "auth", Version: "1", Strength: Weak}},
		},
		{
			ID:      "external",
			Trusted: false,
			Exports: []Export{{Name: "auth", Version: "1", Strength: Strong}},
		},
	}
	relocation := Relocation{
		SourceID:        "source",
		Name:            "auth",
		Version:         "1",
		AllowPreemption: false,
		RequireTrusted:  true,
	}

	result, err := Bind(modules, []string{"external", "source"}, relocation)
	if err != nil {
		t.Fatalf("Bind returned error: %v", err)
	}
	if result.WinnerModuleID != "external" || result.Export.Strength != Strong {
		t.Fatalf("expected external strong to win after collecting the complete sequence, got module=%q strength=%q", result.WinnerModuleID, result.Export.Strength)
	}
	if result.SearchOrder[0] != "source" || result.SearchOrder[1] != "external" {
		t.Fatalf("expected source then external without preemption, got %v", result.SearchOrder)
	}
	if len(result.WeakCandidates) != 1 || len(result.StrongCandidates) != 1 {
		t.Fatalf("expected one weak and one strong candidate, got %d weak and %d strong", len(result.WeakCandidates), len(result.StrongCandidates))
	}
}

func TestExactVersionAndDependencyCycleBFS(t *testing.T) {
	modules := []Module{
		{ID: "source", Trusted: true, Dependencies: []string{"a"}},
		{
			ID:           "a",
			Trusted:      true,
			Dependencies: []string{"b"},
			Exports: []Export{{
				Name:     "auth",
				Version:  "1",
				Strength: Weak,
			}},
		},
		{
			ID:           "b",
			Trusted:      true,
			Dependencies: []string{"a"},
			Exports: []Export{{
				Name:     "auth",
				Version:  "2",
				Strength: Strong,
			}},
		},
	}
	relocation := Relocation{
		SourceID:        "source",
		Name:            "auth",
		Version:         "1",
		AllowPreemption: true,
		RequireTrusted:  true,
	}

	result, err := Bind(modules, []string{"source", "a", "b"}, relocation)
	if err != nil {
		t.Fatalf("Bind returned error for dependency cycle: %v", err)
	}
	expectedOrder := []string{"source", "a", "b"}
	if !reflect.DeepEqual(result.SearchOrder, expectedOrder) {
		t.Fatalf("expected deduplicated BFS order %v, got %v", expectedOrder, result.SearchOrder)
	}
	if !result.Found || result.WinnerModuleID != "a" {
		t.Fatalf("expected exact version in a to win, got found=%v winner=%q", result.Found, result.WinnerModuleID)
	}

	relocation.Version = "3"
	result, err = Bind(modules, []string{"source", "a", "b"}, relocation)
	if err != nil {
		t.Fatalf("Bind returned error for missing version: %v", err)
	}
	if result.Found {
		t.Fatalf("expected no exact version match")
	}
	for _, event := range result.Events {
		if event.ModuleID == "b" && event.Status != VersionMismatch {
			t.Fatalf("expected b to report version mismatch, got %q", event.Status)
		}
	}
}

func TestTrustedRelayAliasUsesActualDefinerTrust(t *testing.T) {
	modules := []Module{
		{ID: "source", Trusted: true, Dependencies: []string{"relay"}},
		{
			ID:      "relay",
			Trusted: true,
			Exports: []Export{{
				Name:     "auth-alias",
				Version:  "1",
				Strength: Strong,
				AliasOf: &SymbolRef{
					ModuleID: "malware",
					Name:     "auth-original",
					Version:  "9",
				},
			}},
		},
		{
			ID:      "malware",
			Trusted: false,
			Exports: []Export{{
				Name:     "auth-original",
				Version:  "9",
				Strength: Strong,
			}},
		},
	}
	relocation := Relocation{
		SourceID:        "source",
		Name:            "auth-alias",
		Version:         "1",
		AllowPreemption: true,
		RequireTrusted:  true,
	}

	result, err := Bind(modules, []string{"source", "relay", "malware"}, relocation)
	if err != nil {
		t.Fatalf("Bind returned error: %v", err)
	}
	if !result.Found || result.WinnerModuleID != "relay" || result.DefinerModuleID != "malware" {
		t.Fatalf("expected relay alias resolving to malware, got winner=%q definer=%q", result.WinnerModuleID, result.DefinerModuleID)
	}
	if result.DefinerTrusted || !result.TrustViolation {
		t.Fatalf("expected untrusted actual definer to cause a trust violation")
	}
}

func TestFindMinimumAttackPlanAndTruncation(t *testing.T) {
	base := []Module{
		{ID: "source", Trusted: true},
		{ID: "auth", Trusted: true},
	}
	plugins := []Module{
		{ID: "evil-a", Trusted: false, Exports: []Export{{Name: "auth", Version: "1", Strength: Strong}}},
		{ID: "evil-b", Trusted: false, Exports: []Export{{Name: "auth", Version: "1", Strength: Strong}}},
	}
	relocation := Relocation{
		SourceID:        "source",
		Name:            "auth",
		Version:         "1",
		AllowPreemption: true,
		RequireTrusted:  true,
	}

	report, err := FindAttackPlans(base, []string{"source", "auth"}, plugins, relocation, AttackOptions{MaxPlans: 20})
	if err != nil {
		t.Fatalf("FindAttackPlans returned error: %v", err)
	}
	if report.Status != AttackSolved || report.MinimumPlugins != 1 {
		t.Fatalf("expected solved one-plugin attack, got status=%q minimum=%d", report.Status, report.MinimumPlugins)
	}
	if len(report.Plans) != 6 || report.Truncated {
		t.Fatalf("expected six complete plans, got %d truncated=%v", len(report.Plans), report.Truncated)
	}
	if report.Plans[0].Placements[0].PluginID != "evil-a" {
		t.Fatalf("expected sorting by plugin ID, got %q", report.Plans[0].Placements[0].PluginID)
	}
	for _, plan := range report.Plans {
		if !plan.Binding.TrustViolation || plan.Binding.DefinerTrusted {
			t.Fatalf("expected every returned plan to bind an untrusted definition")
		}
	}

	expectedPositions := []int{0, 1, 2}
	var actualPositions []int
	for _, plan := range report.Plans {
		if plan.Placements[0].PluginID == "evil-a" {
			actualPositions = append(actualPositions, plan.Placements[0].Position)
		}
	}
	if !reflect.DeepEqual(actualPositions, expectedPositions) {
		t.Fatalf("expected evil-a positions %v, got %v", expectedPositions, actualPositions)
	}
}

func TestPlanOutputTruncation(t *testing.T) {
	base := []Module{{ID: "source", Trusted: true}}
	plugins := []Module{
		{ID: "evil-a", Trusted: false, Exports: []Export{{Name: "auth", Version: "1", Strength: Strong}}},
		{ID: "evil-b", Trusted: false, Exports: []Export{{Name: "auth", Version: "1", Strength: Strong}}},
		{ID: "evil-c", Trusted: false, Exports: []Export{{Name: "auth", Version: "1", Strength: Strong}}},
	}
	relocation := Relocation{SourceID: "source", Name: "auth", Version: "1", AllowPreemption: true, RequireTrusted: true}

	report, err := FindAttackPlans(base, []string{"source"}, plugins, relocation, AttackOptions{MaxPlans: 2})
	if err != nil {
		t.Fatalf("FindAttackPlans returned error: %v", err)
	}
	if len(report.Plans) != 2 || !report.Truncated {
		t.Fatalf("expected two truncated plans, got %d truncated=%v", len(report.Plans), report.Truncated)
	}
}

func TestMultiPluginSamePositionOrderAndSorting(t *testing.T) {
	base := []Module{{ID: "source", Trusted: true}}
	plugins := []Module{
		{ID: "evil-b", Trusted: false, Exports: []Export{{Name: "auth", Version: "1", Strength: Strong}}},
		{ID: "evil-a", Trusted: false, Exports: []Export{{Name: "auth", Version: "1", Strength: Strong}}},
	}
	relocation := Relocation{SourceID: "source", Name: "auth", Version: "1", AllowPreemption: true, RequireTrusted: true}

	report, err := FindAttackPlans(base, []string{"source"}, plugins, relocation, AttackOptions{MaxPlugins: 2, MaxPlans: 20})
	if err != nil {
		t.Fatalf("FindAttackPlans returned error: %v", err)
	}
	if report.Status != AttackSolved || report.MinimumPlugins != 1 {
		t.Fatalf("expected one-plugin minimum, got status=%q minimum=%d", report.Status, report.MinimumPlugins)
	}

	onePlugin, err := FindAttackPlans(base, []string{"source"}, plugins, relocation, AttackOptions{MaxPlugins: 1, MaxPlans: 20})
	if err != nil {
		t.Fatalf("one-plugin search returned error: %v", err)
	}
	if got := onePlugin.Plans[0].Placements[0].PluginID; got != "evil-a" {
		t.Fatalf("expected plugin ID sorting to prefer evil-a, got %q", got)
	}

	last := insertAtPositions([]string{"source"}, []PluginPlacement{
		{PluginID: "evil-b", Position: 0},
		{PluginID: "evil-a", Position: 0},
	})
	expectedOrder := []string{"evil-a", "evil-b", "source"}
	if !reflect.DeepEqual(last, expectedOrder) {
		t.Fatalf("expected same-position IDs ordered globally as %v, got %v", expectedOrder, last)
	}
}

func TestAttackSkipsArrangementsWithMissingDependency(t *testing.T) {
	base := []Module{
		{ID: "source", Trusted: true, Dependencies: []string{"auth"}},
		{
			ID:      "auth",
			Trusted: true,
			Exports: []Export{{Name: "auth", Version: "1", Strength: Strong}},
		},
	}
	plugins := []Module{
		{ID: "evil", Trusted: false, Exports: []Export{{Name: "auth", Version: "1", Strength: Strong}}},
	}
	relocation := Relocation{SourceID: "source", Name: "auth", Version: "1", AllowPreemption: true, RequireTrusted: true}

	report, err := FindAttackPlans(base, []string{"source", "auth"}, plugins, relocation, AttackOptions{})
	if err != nil {
		t.Fatalf("FindAttackPlans returned error: %v", err)
	}
	if report.Status != AttackSolved || len(report.Plans) != 2 {
		t.Fatalf("expected two valid loadable attacks, got status=%q plans=%d", report.Status, len(report.Plans))
	}
	for _, plan := range report.Plans {
		if plan.Placements[0].Position >= 2 {
			t.Fatalf("expected only positions before the missing dependency becomes loadable, got %d", plan.Placements[0].Position)
		}
	}
}

func TestAttackSucceedsThroughTrustedRelayAliasToUntrustedDefiner(t *testing.T) {
	base := []Module{
		{ID: "source", Trusted: true},
		{ID: "relay", Trusted: true},
	}
	plugins := []Module{
		{
			ID:      "evil-definer",
			Trusted: false,
			Exports: []Export{{Name: "auth-original", Version: "9", Strength: Strong}},
		},
		{
			ID:      "relay-plugin",
			Trusted: true,
			Exports: []Export{{
				Name:     "auth",
				Version:  "1",
				Strength: Strong,
				AliasOf: &SymbolRef{
					ModuleID: "evil-definer",
					Name:     "auth-original",
					Version:  "9",
				},
			}},
		},
	}
	relocation := Relocation{SourceID: "source", Name: "auth", Version: "1", AllowPreemption: true, RequireTrusted: true}

	report, err := FindAttackPlans(base, []string{"source", "relay"}, plugins, relocation, AttackOptions{})
	if err != nil {
		t.Fatalf("FindAttackPlans returned error: %v", err)
	}
	if report.Status != AttackSolved || report.MinimumPlugins != 2 {
		t.Fatalf("expected two-plugin relay attack, got status=%q minimum=%d", report.Status, report.MinimumPlugins)
	}
	plan := report.Plans[0]
	if plan.Binding.WinnerModuleID != "relay-plugin" || plan.Binding.DefinerModuleID != "evil-definer" {
		t.Fatalf("expected relay-plugin winner resolving to evil-definer, got winner=%q definer=%q", plan.Binding.WinnerModuleID, plan.Binding.DefinerModuleID)
	}
	if plan.Binding.DefinerTrusted || !plan.Binding.TrustViolation {
		t.Fatal("expected actual untrusted definer to decide trust")
	}
}

func TestNoPreemptionPreventsPluginAttackWhenLocalStrongDefinitionWins(t *testing.T) {
	base := []Module{
		{
			ID:      "source",
			Trusted: true,
			Exports: []Export{{Name: "auth", Version: "1", Strength: Strong}},
		},
	}
	plugins := []Module{
		{
			ID:      "evil",
			Trusted: false,
			Exports: []Export{{Name: "auth", Version: "1", Strength: Strong}},
		},
	}
	relocation := Relocation{
		SourceID:        "source",
		Name:            "auth",
		Version:         "1",
		AllowPreemption: false,
		RequireTrusted:  true,
	}

	report, err := FindAttackPlans(base, []string{"source"}, plugins, relocation, AttackOptions{})
	if err != nil {
		t.Fatalf("FindAttackPlans returned error: %v", err)
	}
	if report.Status != AttackNoPlan || report.MinimumPlugins != -1 {
		t.Fatalf("expected no attack plan with unknown minimum, got status=%q minimum=%d", report.Status, report.MinimumPlugins)
	}
	if len(report.Plans) != 0 {
		t.Fatalf("expected no plans, got %d", len(report.Plans))
	}
}

func TestAttackWithoutPluginsReportsNoPlanAndZeroPluginExistingViolation(t *testing.T) {
	trustedBase := []Module{
		{ID: "source", Trusted: true},
		{ID: "auth", Trusted: true, Exports: []Export{{Name: "auth", Version: "1", Strength: Strong}}},
	}
	relocation := Relocation{SourceID: "source", Name: "auth", Version: "1", AllowPreemption: true, RequireTrusted: true}

	report, err := FindAttackPlans(trustedBase, []string{"source", "auth"}, nil, relocation, AttackOptions{})
	if err != nil {
		t.Fatalf("FindAttackPlans returned error for empty plugins: %v", err)
	}
	if report.Status != AttackNoPlan || report.MinimumPlugins != -1 || len(report.Plans) != 0 {
		t.Fatalf("expected no plan for empty plugins, got status=%q minimum=%d plans=%d", report.Status, report.MinimumPlugins, len(report.Plans))
	}

	untrustedBase := []Module{
		{
			ID:           "source",
			Trusted:      true,
			Dependencies: []string{"auth"},
			Exports:      []Export{{Name: "auth", Version: "0", Strength: Strong}},
		},
		{ID: "auth", Trusted: false, Exports: []Export{{Name: "auth", Version: "1", Strength: Strong}}},
	}
	report, err = FindAttackPlans(untrustedBase, []string{"auth", "source"}, nil, relocation, AttackOptions{})
	if err != nil {
		t.Fatalf("FindAttackPlans returned error for existing violation: %v", err)
	}
	if report.Status != AttackSolved || report.MinimumPlugins != 0 || len(report.Plans) != 1 {
		t.Fatalf("expected zero-plugin existing violation, got status=%q minimum=%d plans=%d", report.Status, report.MinimumPlugins, len(report.Plans))
	}
}

func TestBudgetExhaustedReturnsUnknown(t *testing.T) {
	base := []Module{{ID: "source", Trusted: true}}
	plugins := []Module{
		{ID: "evil-a", Trusted: false, Exports: []Export{{Name: "auth", Version: "1", Strength: Strong}}},
	}
	relocation := Relocation{
		SourceID:        "source",
		Name:            "auth",
		Version:         "1",
		AllowPreemption: true,
		RequireTrusted:  true,
	}

	report, err := FindAttackPlans(base, []string{"source"}, plugins, relocation, AttackOptions{SearchBudget: 1})
	if err != nil {
		t.Fatalf("FindAttackPlans returned error: %v", err)
	}
	if report.Status != AttackUnknown {
		t.Fatalf("expected unknown when budget is exhausted, got %q", report.Status)
	}
}

func TestDuplicateSameNameAndVersionExportIsInputError(t *testing.T) {
	modules := []Module{
		{
			ID:      "source",
			Trusted: true,
			Exports: []Export{
				{Name: "auth", Version: "1", Strength: Weak},
				{Name: "auth", Version: "1", Strength: Strong},
			},
		},
	}

	_, err := Bind(modules, []string{"source"}, Relocation{SourceID: "source", Name: "auth", Version: "1", RequireTrusted: true})
	if err == nil {
		t.Fatal("expected duplicate same name/version export to be rejected")
	}
}

func TestAliasTargetMissingExportIsInputError(t *testing.T) {
	modules := []Module{
		{ID: "source", Trusted: true},
		{
			ID:      "relay",
			Trusted: true,
			Exports: []Export{{
				Name:     "auth",
				Version:  "1",
				Strength: Strong,
				AliasOf:  &SymbolRef{ModuleID: "source", Name: "missing", Version: "1"},
			}},
		},
	}

	_, err := Bind(modules, []string{"source", "relay"}, Relocation{SourceID: "source", Name: "other", Version: "1", RequireTrusted: true})
	if err == nil {
		t.Fatal("expected missing alias target export to be an input error")
	}
}

func TestAliasCycleIsInputError(t *testing.T) {
	modules := []Module{
		{
			ID:      "source",
			Trusted: true,
			Exports: []Export{{
				Name:     "a",
				Version:  "1",
				Strength: Strong,
				AliasOf:  &SymbolRef{ModuleID: "source", Name: "b", Version: "1"},
			}, {
				Name:     "b",
				Version:  "1",
				Strength: Strong,
				AliasOf:  &SymbolRef{ModuleID: "source", Name: "a", Version: "1"},
			}},
		},
	}

	_, err := Bind(modules, []string{"source"}, Relocation{SourceID: "source", Name: "a", Version: "1", RequireTrusted: true})
	if err == nil {
		t.Fatal("expected alias cycle to be an input error")
	}
}

func TestAttackRejectsLoadedConfigurationAboveModuleLimit(t *testing.T) {
	base := make([]Module, 15)
	for index := range base {
		base[index] = Module{ID: "base-" + string(rune('a'+index)), Trusted: true}
	}
	plugins := []Module{
		{ID: "evil-a", Trusted: false, Exports: []Export{{Name: "auth", Version: "1", Strength: Strong}}},
		{ID: "evil-b", Trusted: false, Exports: []Export{{Name: "auth", Version: "1", Strength: Strong}}},
	}
	baseOrder := make([]string, len(base))
	for index, module := range base {
		baseOrder[index] = module.ID
	}
	relocation := Relocation{SourceID: base[0].ID, Name: "auth", Version: "1", AllowPreemption: true, RequireTrusted: true}

	report, err := FindAttackPlans(base, baseOrder, plugins, relocation, AttackOptions{})
	if err != nil {
		t.Fatalf("expected oversized arrangements to be skipped, got error: %v", err)
	}
	if report.Status != AttackSolved || report.MinimumPlugins != 1 {
		t.Fatalf("expected one-plugin solution under the 16-module limit, got status=%q minimum=%d", report.Status, report.MinimumPlugins)
	}
	for _, plan := range report.Plans {
		if len(plan.GlobalOrder) != 16 {
			t.Fatalf("expected 16 loaded modules, got %d", len(plan.GlobalOrder))
		}
	}
}
