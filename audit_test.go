package netsec

import (
	"fmt"
	"strings"
	"testing"
)

func TestAnalyzeFindsMinimumAndFullLookup(t *testing.T) {
	modules := []Module{
		{ID: "origin", Trusted: true},
		{ID: "filler", Trusted: true},
		{ID: "plugin", Trusted: false, Exports: []Export{export("auth", "1.0", true)}},
	}
	audit, err := Analyze(modules, []string{"origin", "filler"}, []string{"plugin"}, Relocation{
		Origin:          "origin",
		Name:            "auth",
		Version:         "1.0",
		AllowPreemption: true,
		RequireTrusted:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if audit.Status != AuditFound || audit.MinPlugins != 1 {
		t.Fatalf("audit = %+v, want one-plugin solution", audit)
	}
	if audit.TotalPlans != 3 || len(audit.Plans) != 3 || audit.Truncated {
		t.Fatalf("plans = %d/%d truncated=%t, want 3/3 false",
			len(audit.Plans), audit.TotalPlans, audit.Truncated)
	}
	for i, plan := range audit.Plans {
		if len(plan.Insertions) != 1 || plan.Insertions[0].ModuleID != "plugin" ||
			plan.Insertions[0].Position != i {
			t.Fatalf("plan %d insertions = %+v, want plugin@%d", i, plan.Insertions, i)
		}
		if plan.Binding.ModuleID != "plugin" || plan.Binding.TargetTrusted {
			t.Fatalf("plan %d binding = %+v", i, plan.Binding)
		}
		if want := len(plan.FinalOrder); len(plan.Binding.Lookup) != want {
			t.Fatalf("plan %d lookup length = %d, want %d", i, len(plan.Binding.Lookup), want)
		}
	}
	if got := audit.Plans[0].Binding.Sequence; strings.Join(got, ",") != "plugin,origin,filler" {
		t.Fatalf("sequence at position 0 = %v", got)
	}
	if !strings.Contains(audit.Plans[0].Binding.Explanation, "强定义") {
		t.Fatalf("missing winning reason: %+v", audit.Plans[0].Binding)
	}
}

func TestAnalyzeReportsExistingZeroPluginVulnerability(t *testing.T) {
	modules := []Module{
		{ID: "origin", Trusted: false, Exports: []Export{export("auth", "1.0", true)}},
		{ID: "plugin", Trusted: false, Exports: []Export{export("auth", "1.0", true)}},
	}
	audit, err := Analyze(modules, []string{"origin"}, []string{"plugin"}, Relocation{
		Origin:          "origin",
		Name:            "auth",
		Version:         "1.0",
		AllowPreemption: false,
		RequireTrusted:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if audit.Status != AuditFound || audit.MinPlugins != 0 || audit.TotalPlans != 1 {
		t.Fatalf("audit = %+v, want existing zero-plugin finding", audit)
	}
	if len(audit.Plans[0].Insertions) != 0 {
		t.Fatalf("zero-plugin plan has insertions: %+v", audit.Plans[0])
	}
	if audit.Plans[0].Binding.ModuleID != "origin" {
		t.Fatalf("binding = %+v", audit.Plans[0].Binding)
	}
}

func TestAnalyzeImpossibleWhenLocalTrustedStrongWins(t *testing.T) {
	modules := []Module{
		{ID: "origin", Trusted: true, Exports: []Export{export("auth", "1.0", true)}},
		{ID: "plugin", Trusted: false, Exports: []Export{export("auth", "1.0", true)}},
	}
	audit, err := Analyze(modules, []string{"origin"}, []string{"plugin"}, Relocation{
		Origin:          "origin",
		Name:            "auth",
		Version:         "1.0",
		AllowPreemption: false,
		RequireTrusted:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if audit.Status != AuditImpossible || audit.MinPlugins != -1 || len(audit.Plans) != 0 {
		t.Fatalf("audit = %+v, want impossible", audit)
	}
}

func TestAnalyzeBudgetBoundaries(t *testing.T) {
	modules := []Module{
		{ID: "origin", Trusted: true, Exports: []Export{export("auth", "1.0", true)}},
		{ID: "plugin", Trusted: false, Exports: []Export{export("auth", "1.0", true)}},
	}
	relocation := Relocation{
		Origin:          "origin",
		Name:            "auth",
		Version:         "1.0",
		AllowPreemption: false,
		RequireTrusted:  true,
	}

	audit, err := AnalyzeBudget(modules, []string{"origin"}, []string{"plugin"}, relocation, 1)
	if err != nil {
		t.Fatal(err)
	}
	if audit.Status != AuditUnknown || audit.Examined != 1 || audit.Budget != 1 {
		t.Fatalf("budget 1 audit = %+v, want unknown after one layout", audit)
	}

	// The complete search has one zero-plugin and two one-plugin layouts.
	audit, err = AnalyzeBudget(modules, []string{"origin"}, []string{"plugin"}, relocation, 3)
	if err != nil {
		t.Fatal(err)
	}
	if audit.Status != AuditImpossible || audit.Examined != 3 {
		t.Fatalf("budget 3 audit = %+v, want completed impossible search", audit)
	}

	audit, err = AnalyzeBudget(modules, []string{"origin"}, []string{"plugin"}, relocation, 0)
	if err != nil {
		t.Fatal(err)
	}
	if audit.Status != AuditUnknown || audit.Examined != 0 {
		t.Fatalf("budget 0 audit = %+v, want unknown", audit)
	}
}

func TestAnalyzeExploresAllLayoutsWithoutDuplicates(t *testing.T) {
	modules := []Module{{ID: "origin", Trusted: true, Exports: []Export{export("auth", "1.0", true)}}}
	for i := 0; i < 9; i++ {
		modules = append(modules, Module{ID: fmt.Sprintf("base%02d", i)})
	}
	for i := 1; i <= 3; i++ {
		modules = append(modules, Module{
			ID:      fmt.Sprintf("p%d", i),
			Trusted: false,
			Exports: []Export{export("auth", "1.0", true)},
		})
	}
	base := []string{"origin"}
	for i := 0; i < 9; i++ {
		base = append(base, fmt.Sprintf("base%02d", i))
	}

	audit, err := Analyze(modules, base, []string{"p1", "p2", "p3"}, Relocation{
		Origin:          "origin",
		Name:            "auth",
		Version:         "1.0",
		AllowPreemption: false,
		RequireTrusted:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if audit.Status != AuditImpossible {
		t.Fatalf("audit = %+v, want impossible", audit)
	}

	// 1 + 3*C(11,1) + (3*C(12,2))*2! + 1*C(13,3)*3!
	want := int64(1 + 3*11 + 3*66*2 + 286*6)
	if audit.Examined != want {
		t.Fatalf("examined = %d, want %d", audit.Examined, want)
	}
}

func TestAnalyzeSortsAndTruncatesEqualPlans(t *testing.T) {
	modules := []Module{{ID: "origin", Trusted: true}}
	base := []string{"origin"}
	for i := 0; i < 9; i++ {
		id := fmt.Sprintf("base%02d", i)
		modules = append(modules, Module{ID: id})
		base = append(base, id)
	}
	for i := 1; i <= 3; i++ {
		modules = append(modules, Module{
			ID:      fmt.Sprintf("p%d", i),
			Trusted: false,
			Exports: []Export{export("auth", "1.0", true)},
		})
	}

	audit, err := Analyze(modules, base, []string{"p3", "p1", "p2"}, Relocation{
		Origin:          "origin",
		Name:            "auth",
		Version:         "1.0",
		AllowPreemption: true,
		RequireTrusted:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if audit.Status != AuditFound || audit.MinPlugins != 1 {
		t.Fatalf("audit = %+v", audit)
	}
	if audit.TotalPlans != 33 || len(audit.Plans) != MaxReportedPlans || !audit.Truncated {
		t.Fatalf("plans=%d total=%d truncated=%t, want 20/33 true",
			len(audit.Plans), audit.TotalPlans, audit.Truncated)
	}
	for i, plan := range audit.Plans {
		wantID := "p1"
		wantPosition := i
		if i >= 11 {
			wantID = "p2"
			wantPosition = i - 11
		}
		got := plan.Insertions[0]
		if got.ModuleID != wantID || got.Position != wantPosition {
			t.Fatalf("plan %d insertion = %+v, want %s@%d", i, got, wantID, wantPosition)
		}
	}
	report := audit.Report()
	if !strings.Contains(report, "是否截断：是") ||
		!strings.Contains(report, "前 20 个") ||
		!strings.Contains(report, "胜出理由") {
		t.Fatalf("report does not state truncation and reason:\n%s", report)
	}
}

func TestAnalyzeWithoutTrustRequirementIsImpossible(t *testing.T) {
	modules := []Module{
		{ID: "origin", Trusted: true},
		{ID: "plugin", Trusted: false, Exports: []Export{export("auth", "1.0", true)}},
	}
	audit, err := Analyze(modules, []string{"origin"}, []string{"plugin"}, Relocation{
		Origin:          "origin",
		Name:            "auth",
		Version:         "1.0",
		AllowPreemption: true,
		RequireTrusted:  false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if audit.Status != AuditImpossible || audit.Examined != 0 {
		t.Fatalf("audit = %+v, want immediate impossible", audit)
	}
}
