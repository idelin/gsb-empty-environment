package verifier

import (
	"testing"

	"prescreen.local/ddd-10554-v3/spec"
)

func verifierChip() *spec.Chip {
	return &spec.Chip{
		Owners: []spec.OwnerID{"BL"},
		Positions: []spec.Position{
			{ID: "legacy", Owner: "BL", Scopes: []spec.Scope{"S"}, Public: true},
			{ID: "shadow", Owner: "BL", Scopes: []spec.Scope{"S"}, Shadow: true, Public: true},
		},
		Images: map[string]spec.Image{
			"revoked":  {ID: "revoked", Versions: map[spec.Scope]spec.Generation{"S": 0}},
			"recovery": {ID: "recovery", Versions: map[spec.Scope]spec.Generation{"S": 2}},
		},
		Revoked:  map[spec.OwnerID]string{"BL": "revoked"},
		Recovery: []spec.RecoveryCombo{{Images: map[spec.OwnerID]string{"BL": "recovery"}}},
		Initial: &spec.State{
			Counters: map[spec.PositionID]map[spec.Scope]spec.Generation{
				"legacy": {"S": 1},
				"shadow": {"S": 0},
			},
			Pointers: map[spec.OwnerID]spec.Pointer{"BL": {Phase: spec.PhaseStable}},
		},
		MaxBurns: 1,
	}
}

func TestIndependentSolveMatchesStructure(t *testing.T) {
	eng, err := newEngine(verifierChip())
	if err != nil {
		t.Fatal(err)
	}
	en, err := eng.enumerate()
	if err != nil {
		t.Fatal(err)
	}
	sol, err := en.solve()
	if err != nil {
		t.Fatal(err)
	}
	if sol.RootWorstCase() != 1 {
		t.Fatalf("独立求解最坏烧录 = %d, 期望 1", sol.RootWorstCase())
	}
	for sk, s := range en.stateOf {
		if !eng.safe(s) {
			t.Fatalf("枚举到不安全状态 %s", sk)
		}
	}
}

func TestRejectsTamperedCertificate(t *testing.T) {
	// 直接构造一个内部自相矛盾的最小证书，核验器必须拒绝而非信任输入。
	cert := &spec.Certificate{
		SchemaVersion: 1,
		Chip:          verifierChip(),
		RootBeliefKey: "does-not-exist",
		Beliefs:       map[string]*spec.BeliefNode{},
		States:        map[string]*spec.State{},
		Actions:       map[string]*spec.Action{},
		Observations:  map[string]*spec.Observation{},
	}
	report := Verify(cert)
	if report.OK() {
		t.Fatal("被篡改的根信念证书必须被核验拒绝")
	}
}
