package netsec

import (
	"errors"
	"testing"
)

func allowDepartment(value string) PolicyFunc {
	return func(labels map[string]string) bool {
		return labels["department"] == value
	}
}

func TestFindShortestFixedDepartmentEscalation(t *testing.T) {
	root := NewSession(map[string]string{"department": "guest"})
	candidate := HopRequest{
		RequestedLabels: map[string]string{"department": "admin"},
		FixedLabels:     map[string]string{"department": "eng"},
		OverrideKeys:    []string{"department"},
		RoleID:          "role-b",
	}
	w, err := FindEscalation(root, []HopRequest{candidate}, allowDepartment("admin"))
	if err != nil {
		t.Fatalf("FindEscalation: %v", err)
	}
	if w.Depth() != 1 {
		t.Fatalf("depth = %d, want 1", w.Depth())
	}
	if w.RejectedAt != 1 || !errors.Is(w.ModelError, ErrFixedConflict) {
		t.Fatalf("rejected at %d with %v", w.RejectedAt, w.ModelError)
	}
	if w.FinalLabels["department"] != "admin" {
		t.Fatalf("final labels = %#v", w.FinalLabels)
	}
}

func TestFindEscalationIgnoresBenignValueChange(t *testing.T) {
	root := NewSession(map[string]string{"department": "guest", "project": "a"})
	candidate := HopRequest{
		RequestedLabels: map[string]string{"project": "b"},
		OverrideKeys:    []string{"project"},
		RoleID:          "role-b",
	}
	_, err := FindEscalation(root, []HopRequest{candidate}, allowDepartment("admin"))
	if !errors.Is(err, ErrNoEscalation) {
		t.Fatalf("err = %v, want no escalation", err)
	}
}

func TestFindTwoHopTransitiveUpgradeWitness(t *testing.T) {
	root := NewSession(map[string]string{"project": "base"})
	candidates := []HopRequest{
		{
			RequestedLabels:     map[string]string{"project": "admin"},
			RequestedTransitive: []string{"project"},
			OverrideKeys:        []string{"project"},
			RoleID:              "role-1",
		},
		{
			FixedLabels: map[string]string{"department": "admin"},
			RoleID:      "role-2",
		},
	}
	policy := func(labels map[string]string) bool {
		return labels["project"] == "admin" && labels["department"] == "admin"
	}
	w, err := FindEscalation(root, candidates, policy, MaxDepth)
	if err != nil {
		t.Fatalf("FindEscalation: %v", err)
	}
	if w.Depth() != 2 {
		t.Fatalf("depth = %d, want shortest 2-hop witness", w.Depth())
	}
	if !errors.Is(w.ModelError, ErrTransitiveUp) && !errors.Is(w.ModelError, ErrRoleCycle) {
		t.Fatalf("model error = %v, want transitive-upgrade or cycle", w.ModelError)
	}
	if w.FinalLabels["project"] != "admin" || w.FinalLabels["department"] != "admin" {
		t.Fatalf("final labels = %#v", w.FinalLabels)
	}
}

func TestFindEscalationRoleCycle(t *testing.T) {
	root := NewSession(map[string]string{"project": "base"}, "project")
	root.RoleIDs = []string{"role-a"}
	root.Depth = 1
	candidate := HopRequest{
		RequestedLabels: map[string]string{"project": "admin"},
		OverrideKeys:    []string{"project"},
		RoleID:          "role-a",
	}
	policy := func(labels map[string]string) bool { return labels["project"] == "admin" }
	w, err := FindEscalation(root, []HopRequest{candidate}, policy)
	if err != nil {
		t.Fatalf("FindEscalation: %v", err)
	}
	if w.Depth() != 1 || !errors.Is(w.ModelError, ErrRoleCycle) {
		t.Fatalf("witness = %v, err = %v", w, w.ModelError)
	}
}

func TestScenarioSearchFindsOneHopWitness(t *testing.T) {
	scenario := Scenario{
		InitialLabels: map[string]string{"department": "guest"},
		Roles: []RoleSpec{{
			RoleID:       "role-b",
			FixedLabels:  map[string]string{"department": "eng"},
			OverrideKeys: []string{"department"},
		}},
		Values: []string{"admin", "guest"},
		Policy: allowDepartment("admin"),
	}
	w, err := FindEscalation(scenario, scenario)
	if err != nil {
		t.Fatalf("FindEscalation: %v", err)
	}
	if w.Depth() != 1 {
		t.Fatalf("depth = %d, want 1", w.Depth())
	}
	if w.FinalLabels["department"] != "admin" {
		t.Fatalf("final labels = %#v", w.FinalLabels)
	}
}

func TestScenarioSearchExploresCaseApproximateKeys(t *testing.T) {
	scenario := Scenario{
		InitialLabels: map[string]string{"project": "guest"},
		Roles: []RoleSpec{{
			RoleID:       "role-b",
			FixedLabels:  map[string]string{"project": "locked"},
			OverrideKeys: []string{"other"},
		}},
		Keys:   []string{"project", "Project"},
		Values: []string{"admin"},
		Policy: func(labels map[string]string) bool { return labels["project"] == "admin" },
	}
	w, err := FindEscalation(scenario, scenario)
	if err != nil {
		t.Fatalf("FindEscalation: %v", err)
	}
	if w.Depth() != 1 {
		t.Fatalf("depth = %d, want 1", w.Depth())
	}
	if w.FinalLabels["project"] != "admin" {
		t.Fatalf("final labels = %#v", w.FinalLabels)
	}
}
