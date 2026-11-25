package symbolbind

import (
	"strings"
	"testing"
)

func export(name, version string, strength Strength, definition *DefinitionRef) Export {
	return Export{
		Name:       name,
		Version:    version,
		Strength:   strength,
		Definition: definition,
	}
}

func TestStrongWinsOverEarlierWeakAcrossFullSequence(t *testing.T) {
	input := Input{
		Modules: []Module{
			{ID: "host", Trusted: true, Exports: []Export{export("auth", "1", Weak, nil)}},
			{ID: "dep", Trusted: true, Exports: []Export{export("auth", "1", Strong, nil)}},
		},
		GlobalOrder: []string{"host", "dep"},
		Plugins: []Module{
			{ID: "evil-weak", Trusted: false, Exports: []Export{export("auth", "1", Weak, nil)}},
		},
	}

	trace, err := BindWithGlobal(input, []string{"evil-weak", "host", "dep"}, Relocation{
		OriginID:           "host",
		Name:               "auth",
		Version:            "1",
		AllowInterposition: true,
		RequireTrusted:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(trace.Candidates), 3; got != want {
		t.Fatalf("candidate count = %d, want %d", got, want)
	}
	if trace.Winner == nil || trace.Winner.ExporterID != "dep" || trace.Winner.Strength != Strong {
		t.Fatalf("winner = %+v, want strong dep", trace.Winner)
	}
	if trace.Untrusted {
		t.Fatal("trusted strong definition must win over earlier untrusted weak definition")
	}

	result, err := FindMinimalAttacks(input, Relocation{
		OriginID:           "host",
		Name:               "auth",
		Version:            "1",
		AllowInterposition: true,
		RequireTrusted:     true,
	}, AttackOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusNoAttack {
		t.Fatalf("status = %s, want no_attack: %s", result.Status, result.Reason)
	}
}

func TestAliasAndTrustedRelayUseActualDefinerTrust(t *testing.T) {
	input := Input{
		Modules: []Module{
			{
				ID:           "caller",
				Trusted:      true,
				Dependencies: []string{"relay"},
			},
			{
				ID:      "relay",
				Trusted: true,
				Exports: []Export{
					export("auth", "1", Strong, &DefinitionRef{ModuleID: "evil"}),
				},
			},
		},
		GlobalOrder: []string{"caller", "relay"},
		Plugins: []Module{
			{ID: "evil", Trusted: false, Exports: []Export{export("auth", "1", Strong, nil)}},
		},
	}

	trace, err := BindWithGlobal(input, []string{"caller", "relay", "evil"}, Relocation{
		OriginID:           "caller",
		Name:               "auth",
		Version:            "1",
		AllowInterposition: true,
		RequireTrusted:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if trace.Winner == nil || trace.Winner.ExporterID != "relay" || trace.Winner.DefinerID != "evil" {
		t.Fatalf("winner = %+v, want relay exporting evil's definition", trace.Winner)
	}
	if trace.Winner.DefinerTrusted || !trace.Untrusted {
		t.Fatal("trust must follow the actual definer, not the trusted relay exporter")
	}
}

func TestNoInterpositionPrefersLocalStrongDefinition(t *testing.T) {
	input := Input{
		Modules: []Module{
			{ID: "host", Trusted: true, Exports: []Export{export("auth", "1", Strong, nil)}},
			{ID: "dep", Trusted: true, Exports: []Export{export("auth", "1", Weak, nil)}},
		},
		GlobalOrder: []string{"host", "dep"},
		Plugins: []Module{
			{ID: "evil", Trusted: false, Exports: []Export{export("auth", "1", Strong, nil)}},
		},
	}
	relocation := Relocation{
		OriginID:           "host",
		Name:               "auth",
		Version:            "1",
		AllowInterposition: false,
		RequireTrusted:     true,
	}

	trace, err := BindWithGlobal(input, []string{"evil", "host", "dep"}, relocation)
	if err != nil {
		t.Fatal(err)
	}
	if trace.Sequence[0].ModuleID != "host" || trace.Sequence[0].Source != "origin" {
		t.Fatalf("first sequence entry = %+v, want local origin", trace.Sequence[0])
	}
	if trace.Winner == nil || trace.Winner.ExporterID != "host" || trace.Untrusted {
		t.Fatalf("winner = %+v, want trusted local host", trace.Winner)
	}

	result, err := FindMinimalAttacks(input, relocation, AttackOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusNoAttack {
		t.Fatalf("status = %s, want no_attack", result.Status)
	}
}

func TestExactVersionIsRequired(t *testing.T) {
	input := Input{
		Modules: []Module{
			{ID: "host", Trusted: true},
			{ID: "dep", Trusted: true, Exports: []Export{export("auth", "2", Strong, nil)}},
		},
		GlobalOrder: []string{"host", "dep"},
		Plugins: []Module{
			{ID: "evil", Trusted: false, Exports: []Export{export("auth", "1.1", Strong, nil)}},
		},
	}

	trace, err := BindWithGlobal(input, []string{"evil", "host", "dep"}, Relocation{
		OriginID:           "host",
		Name:               "auth",
		Version:            "1",
		AllowInterposition: true,
		RequireTrusted:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if trace.Resolved || trace.Winner != nil {
		t.Fatalf("trace resolved = %t winner = %+v, want no exact-version match", trace.Resolved, trace.Winner)
	}

	result, err := FindMinimalAttacks(input, Relocation{
		OriginID:           "host",
		Name:               "auth",
		Version:            "1",
		AllowInterposition: true,
		RequireTrusted:     true,
	}, AttackOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusNoAttack {
		t.Fatalf("status = %s, want no_attack", result.Status)
	}
}

func TestDependencyCycleDoesNotPreventBFS(t *testing.T) {
	input := Input{
		Modules: []Module{
			{ID: "host", Trusted: true, Dependencies: []string{"a"}},
			{ID: "a", Trusted: true, Dependencies: []string{"b"}, Exports: []Export{export("auth", "1", Strong, nil)}},
			{ID: "b", Trusted: true, Dependencies: []string{"a"}},
		},
		GlobalOrder: []string{"host", "a", "b"},
	}

	trace, err := Bind(input, Relocation{
		OriginID:           "host",
		Name:               "auth",
		Version:            "1",
		AllowInterposition: false,
		RequireTrusted:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	gotSequence := []string{trace.Sequence[0].ModuleID, trace.Sequence[1].ModuleID, trace.Sequence[2].ModuleID}
	wantSequence := []string{"host", "a", "b"}
	for index := range wantSequence {
		if gotSequence[index] != wantSequence[index] {
			t.Fatalf("sequence = %v, want %v", gotSequence, wantSequence)
		}
	}
	if trace.Winner == nil || trace.Winner.DefinerID != "a" {
		t.Fatalf("winner = %+v, want a", trace.Winner)
	}
}

func TestMinimalAttackSortingAndTruncation(t *testing.T) {
	input := Input{
		Modules: []Module{
			{ID: "host", Trusted: true},
			{ID: "dep", Trusted: true},
		},
		GlobalOrder: []string{"host", "dep"},
		Plugins: []Module{
			{ID: "evil-a", Trusted: false, Dependencies: []string{"evil-b"}, Exports: []Export{export("auth", "1", Strong, &DefinitionRef{ModuleID: "evil-b", Name: "auth-impl"})}},
			{ID: "evil-b", Trusted: false, Exports: []Export{export("auth-impl", "1", Strong, nil)}},
		},
	}
	relocation := Relocation{
		OriginID:           "host",
		Name:               "auth",
		Version:            "1",
		AllowInterposition: true,
		RequireTrusted:     true,
	}

	result, err := FindMinimalAttacks(input, relocation, AttackOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusAttack || result.PluginCount != 2 || len(result.Schemes) != 2 || !result.Truncated {
		t.Fatalf("result = %+v, want two truncated two-plugin attacks", result)
	}
	wantFirst := []Insertion{{PluginID: "evil-a", Position: 0}, {PluginID: "evil-b", Position: 0}}
	if got := result.Schemes[0].Insertions; len(got) != 2 || got[0] != wantFirst[0] || got[1] != wantFirst[1] {
		t.Fatalf("first insertions = %+v, want %+v", got, wantFirst)
	}
	if result.Schemes[0].Trace.Winner == nil || result.Schemes[0].Trace.Winner.DefinerID != "evil-b" {
		t.Fatalf("winner = %+v, want actual definer evil-b", result.Schemes[0].Trace.Winner)
	}
}

func TestExhaustedBudgetReturnsUnknown(t *testing.T) {
	input := Input{
		Modules: []Module{
			{ID: "host", Trusted: true},
			{ID: "dep", Trusted: true},
		},
		GlobalOrder: []string{"host", "dep"},
		Plugins: []Module{
			{ID: "evil", Trusted: false, Exports: []Export{export("auth", "1", Strong, nil)}},
		},
	}

	result, err := FindMinimalAttacks(input, Relocation{
		OriginID:           "host",
		Name:               "auth",
		Version:            "1",
		AllowInterposition: true,
		RequireTrusted:     true,
	}, AttackOptions{MaxSchemes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusUnknown {
		t.Fatalf("status = %s, want unknown", result.Status)
	}
}

func TestAttackSearchReturnsMissingDependencyError(t *testing.T) {
	input := Input{
		Modules: []Module{
			{ID: "host", Trusted: true, Dependencies: []string{"evil-helper"}},
		},
		GlobalOrder: []string{"host"},
		Plugins: []Module{
			{ID: "evil", Trusted: false, Exports: []Export{export("auth", "1", Strong, nil)}},
			{ID: "evil-helper", Trusted: false},
		},
	}

	_, err := FindMinimalAttacks(input, Relocation{
		OriginID:           "host",
		Name:               "auth",
		Version:            "1",
		AllowInterposition: false,
		RequireTrusted:     true,
	}, AttackOptions{})
	if err == nil {
		t.Fatal("missing dependency in a searched graph must return an error")
	}
}

func TestInputValidationRejectsDuplicateExportAndMissingDependency(t *testing.T) {
	valid := Input{
		Modules: []Module{
			{ID: "host", Trusted: true, Exports: []Export{
				export("auth", "1", Strong, nil),
				export("auth", "1", Weak, nil),
			}},
		},
		GlobalOrder: []string{"host"},
	}
	if err := Validate(valid); err == nil {
		t.Fatal("duplicate name/version export must be an input error")
	}

	missingDependency := Input{
		Modules: []Module{
			{ID: "host", Trusted: true, Dependencies: []string{"missing"}},
		},
		GlobalOrder: []string{"host"},
	}
	if err := Validate(missingDependency); err == nil {
		t.Fatal("missing dependency must be an input error")
	}
}

func TestTraceReportIncludesCompleteSearchAndReason(t *testing.T) {
	input := Input{
		Modules: []Module{
			{ID: "host", Trusted: true},
			{ID: "dep", Trusted: true, Exports: []Export{export("auth", "1", Strong, nil)}},
		},
		GlobalOrder: []string{"host", "dep"},
	}

	trace, err := Bind(input, Relocation{
		OriginID:           "host",
		Name:               "auth",
		Version:            "1",
		AllowInterposition: true,
		RequireTrusted:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	report := trace.String()
	for _, fragment := range []string{
		"sequence:",
		"1. host (global)",
		"2. dep (global)",
		"inspections:",
		"candidates:",
		"winner: exporter=dep definer=dep trusted=true strength=strong",
		"reason:",
	} {
		if !strings.Contains(report, fragment) {
			t.Fatalf("report %q does not contain %q", report, fragment)
		}
	}
}
