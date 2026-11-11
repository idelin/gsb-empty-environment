package adgame_test

import (
	"fmt"

	"example.org/netsec/uid1250/adgame"
)

// ExampleSolve 演示演练宿主如何定义博弈、求完整策略树并读取验证报告。
func ExampleSolve() {
	const (
		access adgame.Fact = iota
		cred
		exfil
		patch
	)
	g := &adgame.Game{
		Name:    "demo-kill-chain",
		Initial: adgame.State(1 << access),
		Goal:    adgame.Goal{Required: adgame.State(1 << exfil)},
		Rounds:  2,
		Budget:  5,
		Actions: []adgame.Action{
			{ID: "steal", Owner: adgame.Attacker, Require: adgame.State(1 << access), Add: adgame.State(1 << cred)},
			{ID: "exfil", Owner: adgame.Attacker, Require: adgame.State(1 << cred), Forbid: adgame.State(1 << patch), Add: adgame.State(1 << exfil)},
			{ID: "patch", Owner: adgame.Defender, Add: adgame.State(1 << patch), Cost: 2},
		},
	}

	sol, err := adgame.Solve(g, adgame.Options{})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("outcome:", sol.Outcome())
	fmt.Println("nodes:", sol.Stats().MemoStates)

	// 独立回放校验: 纯语义极小极大与完整策略 DAG 必须一致。
	if err := adgame.ValidateStrategy(g, sol); err != nil {
		fmt.Println("invalid strategy:", err)
		return
	}

	// 极小模型穷举验证动作失效、无合法动作与预算耗尽。
	report, err := adgame.Verify(g)
	if err != nil {
		fmt.Println("verify error:", err)
		return
	}
	fmt.Println("reachable states:", len(report.ReachableStates))
	fmt.Println("failure classes:", len(report.Failures))

	// Output:
	// outcome: defender-wins
	// nodes: 5
	// reachable states: 6
	// failure classes: 4
}
