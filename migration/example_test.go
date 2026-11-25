package migration_test

import (
	"encoding/json"
	"fmt"

	"prescreen.local/ddd-10554-v3/migration"
	"prescreen.local/ddd-10554-v3/spec"
	"prescreen.local/ddd-10554-v3/verifier"
)

func Example() {
	chip := &spec.Chip{
		Owners: []spec.OwnerID{"BL"},
		Positions: []spec.Position{
			{ID: "legacy", Owner: "BL", Scopes: []spec.Scope{"S"}, Public: true},
			{ID: "shadow", Owner: "BL", Scopes: []spec.Scope{"S"}, Shadow: true, Public: false},
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
		MaxBurns: 2,
	}

	// 1. 离线求解最小最坏烧录策略并生成核验证书。
	planner, err := migration.Solve(chip)
	if err != nil {
		panic(err)
	}
	cert, err := planner.BuildCertificate()
	if err != nil {
		panic(err)
	}

	// 2. 独立核验器（不 import 规划器）重建状态空间并重新求解。
	report := verifier.Verify(cert)
	if !report.OK() {
		panic(report.Errors)
	}

	// 3. 宿主在真实上电循环中只凭公开观测驱动会话。
	model, err := migration.New(chip)
	if err != nil {
		panic(err)
	}
	session := planner.NewSession()
	real := chip.Initial
	burns := 0
	for !session.Done() {
		action := session.NextAction()
		outcomes, err := model.Outcomes(real, action)
		if err != nil {
			panic(err)
		}
		// 取掉电分支（最坏环境），仅把公开读回喂给会话。
		var obs *spec.Observation
		for _, oc := range outcomes {
			if oc.Crashed {
				obs = oc.Observation
				real = oc.State
				burns += oc.Burns
			}
		}
		if err := session.Observe(action, obs); err != nil {
			panic(err)
		}
	}

	fmt.Println("worst-case burns:", cert.WorstCaseBurns)
	fmt.Println("verified:", report.OK())
	fmt.Println("session burns:", burns)

	// 证书是纯数据，可落盘后由独立组件离线核验。
	if _, err := json.Marshal(cert); err != nil {
		panic(err)
	}
	// Output:
	// worst-case burns: 1
	// verified: true
	// session burns: 1
}
