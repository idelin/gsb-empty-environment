package netsec

import (
	"errors"
	"reflect"
	"testing"
)

func TestAuditedNoFlawsMatchesReferenceModel(t *testing.T) {
	req := HopRequest{
		CurrentLabelSet: map[string]Label{
			"project": {
				Key:        "project",
				Value:      "old",
				Transitive: true,
				Origins: []Origin{
					{Kind: OriginInitial, Value: "old"},
					{RoleID: "r0", Hop: 1, Kind: OriginInherited, Value: "old"},
				},
			},
		},
		CurrentTransitive:   []string{"project"},
		RequestedLabels:     map[string]string{"project": "new"},
		RequestedTransitive: []string{"project"},
		FixedLabels:         map[string]string{"department": "eng"},
		OverrideKeys:        []string{"project"},
		Depth:               2,
		RoleID:              "r1",
		CurrentRoleIDs:      []string{"r0"},
	}
	want, err := ApplyHop(req)
	if err != nil {
		t.Fatalf("model: %v", err)
	}
	got, err := AuditedHopWithConfig(req, AuditConfig{Flaws: FlawNone})
	if err != nil {
		t.Fatalf("audited: %v", err)
	}
	if !reflect.DeepEqual(got.Values, want.Values) {
		t.Fatalf("values differ: got %#v want %#v", got.Values, want.Values)
	}
	if !reflect.DeepEqual(got.Transitive, want.Transitive) {
		t.Fatalf("transitive differ: got %v want %v", got.Transitive, want.Transitive)
	}
	if !reflect.DeepEqual(got.Labels["project"].Origins, want.Labels["project"].Origins) {
		t.Fatalf("origins differ: got %#v want %#v", got.Labels["project"].Origins, want.Labels["project"].Origins)
	}
}

func TestAuditedCaseFoldBypassesProtection(t *testing.T) {
	req := HopRequest{
		RequestedLabels: map[string]string{"Project": "open"},
		OverrideKeys:    []string{"project"},
		Depth:           1,
		RoleID:          "r1",
	}
	if _, err := ApplyHop(req); !errors.Is(err, ErrNotOverridable) {
		t.Fatalf("model err = %v, want not-overridable", err)
	}
	res, err := AuditedHopWithConfig(req, AuditConfig{Flaws: FlawCaseFoldKeys})
	if err != nil {
		t.Fatalf("audited: %v", err)
	}
	if got := res.Values["project"]; got != "open" {
		t.Fatalf("audited project = %q, want open (case-fold collision)", got)
	}
	if _, ok := res.Values["Project"]; ok {
		t.Fatal("audited preserved the distinct case spelling")
	}
}

func TestAuditedFixedDepartmentOverride(t *testing.T) {
	req := HopRequest{
		RequestedLabels: map[string]string{"department": "admin"},
		FixedLabels:     map[string]string{"department": "eng"},
		OverrideKeys:    []string{"department"},
		Depth:           1,
		RoleID:          "r1",
	}
	if _, err := ApplyHop(req); !errors.Is(err, ErrFixedConflict) {
		t.Fatalf("model err = %v, want fixed conflict", err)
	}
	res, err := AuditedHop(req)
	if err != nil {
		t.Fatalf("audited: %v", err)
	}
	if got := res.Values["department"]; got != "admin" {
		t.Fatalf("audited department = %q, want admin", got)
	}
}

func TestAuditedTransitiveUpgrade(t *testing.T) {
	req := HopRequest{
		CurrentLabels:       map[string]string{"project": "old"},
		RequestedLabels:     map[string]string{"project": "new"},
		RequestedTransitive: []string{"project"},
		OverrideKeys:        []string{"project"},
		Depth:               1,
		RoleID:              "r1",
	}
	if _, err := ApplyHop(req); !errors.Is(err, ErrTransitiveUp) {
		t.Fatalf("model err = %v, want transitive upgrade", err)
	}
	res, err := AuditedHop(req)
	if err != nil {
		t.Fatalf("audited: %v", err)
	}
	if !res.TransitiveSet["project"] {
		t.Fatal("audited did not upgrade project")
	}
}

func TestAuditedRoleCycle(t *testing.T) {
	s := NewSession(nil)
	s.RoleIDs = []string{"role-a"}
	if _, err := s.Advance(HopRequest{RoleID: "role-a"}); !errors.Is(err, ErrRoleCycle) {
		t.Fatalf("model err = %v, want role cycle", err)
	}
	next, err := auditedTransition(s, HopRequest{RoleID: "role-a"}, DefaultAuditConfig())
	if err != nil {
		t.Fatalf("audited: %v", err)
	}
	if len(next.RoleIDs) != 2 || next.RoleIDs[0] != "role-a" || next.RoleIDs[1] != "role-a" {
		t.Fatalf("audited role chain = %v", next.RoleIDs)
	}
}

func TestAuditedResetSourcesOnOverride(t *testing.T) {
	req := HopRequest{
		CurrentLabels:     map[string]string{"project": "old"},
		CurrentTransitive: []string{"project"},
		RequestedLabels:   map[string]string{"project": "new"},
		OverrideKeys:      []string{"project"},
		Depth:             1,
		RoleID:            "r1",
	}
	model, err := ApplyHop(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(model.Labels["project"].Origins) != 3 {
		t.Fatalf("model origins = %#v", model.Labels["project"].Origins)
	}
	audited, err := AuditedHopWithConfig(req, AuditConfig{Flaws: FlawResetSourcesOnOverride})
	if err != nil {
		t.Fatal(err)
	}
	if len(audited.Labels["project"].Origins) != 1 {
		t.Fatalf("audited origins = %#v, want reset", audited.Labels["project"].Origins)
	}
}
