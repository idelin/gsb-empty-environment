package adgame

import (
	"errors"
	"math/rand"
	"testing"
)

// 事实编号约定:
const (
	fAccess  Fact = 0 // 已获得入口
	fCred    Fact = 1 // 已窃取凭据
	fEscal   Fact = 2 // 已提权
	fExfil   Fact = 3 // 已完成数据外泄 (目标)
	fPatched Fact = 5 // 漏洞已修补
)

func stateOf(facts ...Fact) State {
	var s State
	for _, f := range facts {
		s = s.With(f)
	}
	return s
}

// chainGame 构造一条攻击链: access -> cred -> escal -> exfil,
// 守方可以修补漏洞 (阻断 cred->escal) 或隔离主机 (移除 access/cred)。
func chainGame(rounds, budget, patchCost, isolateCost int) *Game {
	return &Game{
		Name:    "kill-chain",
		Initial: stateOf(fAccess),
		Goal:    Goal{Required: stateOf(fExfil)},
		Rounds:  rounds,
		Budget:  budget,
		Actions: []Action{
			{ID: "steal-cred", Owner: Attacker, Require: stateOf(fAccess), Add: stateOf(fCred)},
			{ID: "escalate", Owner: Attacker, Require: stateOf(fCred), Forbid: stateOf(fPatched), Add: stateOf(fEscal)},
			{ID: "exfil", Owner: Attacker, Require: stateOf(fEscal), Add: stateOf(fExfil)},
			{ID: "patch", Owner: Defender, Add: stateOf(fPatched), Cost: patchCost},
			{ID: "isolate", Owner: Defender, Require: stateOf(fAccess), Delete: stateOf(fAccess, fCred), Cost: isolateCost},
		},
	}
}

func TestDefenderGuaranteesWithFullTree(t *testing.T) {
	g := chainGame(2, 5, 2, 3)
	sol, err := Solve(g, Options{})
	if err != nil {
		t.Fatalf("Solve 返回错误: %v", err)
	}
	if sol.Outcome() != DefenderWins {
		t.Fatalf("期望守方必胜, 得到 %s", sol.Outcome())
	}
	if err := ValidateStrategy(g, sol); err != nil {
		t.Fatalf("策略回放失败: %v", err)
	}
	root := sol.Root()
	if root.Side != Attacker || len(root.Edges) != 1 {
		t.Fatalf("根节点分支异常: %+v", root)
	}
	child := sol.Node(root.Edges[0].To)
	if child == nil || child.Side != Defender {
		t.Fatalf("缺少守方应对节点")
	}
	winning := sol.WinningActions(child)
	if len(winning) < 2 {
		t.Fatalf("守方应至少有 patch/isolate 两个制胜动作, 得到 %v", winning)
	}
	if sol.Stats().MemoStates <= 1 {
		t.Fatalf("记忆化节点数异常: %+v", sol.Stats())
	}
}

func TestAttackerCounterStrategyWhenBudgetExhausted(t *testing.T) {
	// 攻击链需要 3 个攻方半步, 给 3 个回合; 预算 0 时守方买不起任何
	// 防护动作, 攻方沿 steal-cred -> escalate -> exfil 强制得手。
	g := chainGame(3, 0, 2, 3)
	sol, err := Solve(g, Options{})
	if err != nil {
		t.Fatalf("Solve 返回错误: %v", err)
	}
	if sol.Outcome() != AttackerWins {
		t.Fatalf("期望攻方反策略获胜, 得到 %s", sol.Outcome())
	}
	if err := ValidateStrategy(g, sol); err != nil {
		t.Fatalf("策略回放失败: %v", err)
	}
	if sol.Root().PolicyActionID == "" {
		t.Fatalf("攻方根节点缺少必胜策略动作")
	}
}

func TestBudgetBoundaryFlipsOutcome(t *testing.T) {
	g := chainGame(3, 2, 100, 3)
	solPoor, err := Solve(g, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if solPoor.Outcome() != AttackerWins {
		t.Fatalf("预算 2 时期望攻方获胜")
	}
	g.Budget = 3
	solOK, err := Solve(g, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if solOK.Outcome() != DefenderWins {
		t.Fatalf("预算 3 时期守望方获胜")
	}
	if err := ValidateStrategy(g, solOK); err != nil {
		t.Fatal(err)
	}
}

func TestHorizonDefenderWins(t *testing.T) {
	// 攻击链需要 3 个攻方半步 (steal-cred -> escalate -> exfil),
	// 1 个回合只给 1 个攻方半步, 时间耗尽守方胜。
	g := chainGame(1, 100, 2, 3)
	sol, err := Solve(g, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if sol.Outcome() != DefenderWins {
		t.Fatalf("回合不足时期守望方靠时间耗尽获胜, 得到 %s", sol.Outcome())
	}
	if err := ValidateStrategy(g, sol); err != nil {
		t.Fatal(err)
	}
	if sol.Stats().HorizonTerminals == 0 {
		t.Fatalf("应出现半步耗尽终止节点")
	}
}

func TestInitialGoalIsImmediateAttackerWin(t *testing.T) {
	g := chainGame(2, 5, 2, 3)
	g.Initial = stateOf(fExfil)
	sol, err := Solve(g, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if sol.Outcome() != AttackerWins || !sol.Root().Terminal || !sol.Root().GoalHit {
		t.Fatalf("初始即达目标应立即判攻方获胜: %+v", sol.Root())
	}
}

func TestNoLegalMovePassesAndVerifies(t *testing.T) {
	g := &Game{
		Initial: stateOf(fAccess),
		Goal:    Goal{Required: stateOf(fExfil)},
		Rounds:  1,
		Budget:  0,
		Actions: []Action{
			{ID: "steal-cred", Owner: Attacker, Require: stateOf(fAccess), Add: stateOf(fCred)},
			{ID: "escalate", Owner: Attacker, Require: stateOf(fCred), Add: stateOf(fEscal)},
			{ID: "exfil", Owner: Attacker, Require: stateOf(fEscal), Add: stateOf(fExfil)},
		},
	}
	sol, err := Solve(g, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if sol.Outcome() != DefenderWins {
		t.Fatalf("期望守方靠停着与时间耗尽获胜")
	}
	if sol.Stats().PassNodes == 0 {
		t.Fatalf("应记录停着节点")
	}
	if err := ValidateStrategy(g, sol); err != nil {
		t.Fatal(err)
	}

	report, err := Verify(g)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range report.NoLegalMoves {
		if w.Side == Defender && len(w.States) > 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("验证器未报告守方无合法动作的极小模型: %+v", report.NoLegalMoves)
	}
}

func TestVerifyActionFailuresAndExhaustion(t *testing.T) {
	g := chainGame(2, 1, 2, 3)
	report, err := Verify(g)
	if err != nil {
		t.Fatal(err)
	}

	saw := map[string]map[FailureReason]bool{}
	for _, fw := range report.Failures {
		if saw[fw.ActionID] == nil {
			saw[fw.ActionID] = map[FailureReason]bool{}
		}
		saw[fw.ActionID][fw.Reason] = true
	}
	if !saw["exfil"][Inapplicable] {
		t.Fatalf("缺少 exfil 前置不满足的极小模型见证")
	}
	if !saw["patch"][Ineffective] {
		t.Fatalf("缺少 patch 已修补后无变化的极小模型见证")
	}
	if !saw["patch"][BlockedByBudget] || !saw["isolate"][BlockedByBudget] {
		t.Fatalf("缺少守方动作预算阻断见证: %+v", saw)
	}

	if len(report.Exhaustion) == 0 {
		t.Fatalf("应穷举出预算耗尽局面")
	}
	if report.MinExtraBudget != 1 || report.EarliestExhaustion == nil {
		t.Fatalf("最小追加预算期望 1, 得到 %d", report.MinExtraBudget)
	}
	for _, fw := range report.Failures {
		for _, s := range fw.States {
			assertMinimal(t, report.ReachableStates, s, failurePred(g, fw))
		}
	}
}

func failurePred(g *Game, fw FailureWitness) func(State) bool {
	a, _ := g.actionByID(fw.ActionID)
	switch fw.Reason {
	case Inapplicable:
		return func(s State) bool { return !a.Applicable(s) }
	case Ineffective:
		return func(s State) bool { return a.Applicable(s) && a.NoOp(s) }
	case BlockedByBudget:
		return func(s State) bool { return a.Applicable(s) && a.Cost > g.Budget }
	default:
		return func(State) bool { return false }
	}
}

func assertMinimal(t *testing.T, candidates []State, s State, pred func(State) bool) {
	t.Helper()
	inSet := make([]bool, 1<<FactCount)
	for _, c := range candidates {
		inSet[c] = true
	}
	if s == 0 {
		return
	}
	for sub := (s - 1) & s; ; sub = (sub - 1) & s {
		if inSet[sub] && pred(sub) {
			t.Fatalf("状态 %#x 不是极小模型, 真子集 %#x 也满足谓词", uint16(s), uint16(sub))
		}
		if sub == 0 {
			return
		}
	}
}

func TestClosureCertificateSoundness(t *testing.T) {
	g := chainGame(2, 5, 2, 3)
	report, err := Verify(g)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Closure.GoalReachable {
		t.Fatalf("攻击链可达时闭包证书误报不可达")
	}

	g2 := &Game{
		Initial: stateOf(fAccess, fCred),
		Goal:    Goal{Required: stateOf(fExfil)},
		Rounds:  3,
		Budget:  0,
		Actions: []Action{
			{ID: "exfil", Owner: Attacker, Require: stateOf(fEscal), Add: stateOf(fExfil)},
		},
	}
	sol, err := Solve(g2, Options{})
	if err != nil {
		t.Fatal(err)
	}
	report2, err := Verify(g2)
	if err != nil {
		t.Fatal(err)
	}
	if report2.Closure.GoalReachable {
		t.Fatalf("闭包缺少 escal 时不应声称目标可达")
	}
	if !report2.Closure.MissingFacts.Has(fExfil) {
		t.Fatalf("MissingFacts 应包含 exfil")
	}
	if sol.Outcome() != DefenderWins {
		t.Fatalf("独立闭包证明不可达时极小极大也应判守方获胜")
	}
}

func TestReachableStatesCoverDAG(t *testing.T) {
	g := chainGame(2, 5, 2, 3)
	report, err := Verify(g)
	if err != nil {
		t.Fatal(err)
	}
	sol, err := Solve(g, Options{})
	if err != nil {
		t.Fatal(err)
	}
	inReach := make(map[State]bool)
	for _, s := range report.ReachableStates {
		inReach[s] = true
	}
	for _, id := range sol.SortedNodeIDs() {
		n := sol.Node(id)
		if !inReach[n.State] {
			t.Fatalf("DAG 节点状态 %#x 不在可达状态论域中", uint16(n.State))
		}
	}
}

func TestDeterminismAcrossRuns(t *testing.T) {
	g := chainGame(2, 5, 2, 3)
	first, err := Solve(g, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for trial := 0; trial < 5; trial++ {
		other, err := Solve(g, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if other.Outcome() != first.Outcome() {
			t.Fatal("多次求解胜负不稳定")
		}
		id, pass, ok := other.NextMove(g.Initial, 0, g.Budget)
		if !ok || pass {
			t.Fatalf("策略查询不确定: %q pass=%v ok=%v", id, pass, ok)
		}
		firstID, _, _ := first.NextMove(g.Initial, 0, g.Budget)
		if id != firstID {
			t.Fatalf("策略查询不稳定: %q vs %q", id, firstID)
		}
	}
}

func TestValidationErrors(t *testing.T) {
	cases := []*Game{
		{Rounds: 0, Budget: 0},
		{Rounds: 1, Budget: -1},
		{Rounds: 1, Budget: 0, Actions: []Action{{ID: "", Owner: Attacker}}},
		{Rounds: 1, Budget: 0, Actions: []Action{{ID: "x", Owner: Attacker, Cost: 1}}},
		{Rounds: 1, Budget: 0, Actions: []Action{{ID: "x", Owner: Defender, Cost: -1}}},
		{Rounds: 1, Budget: 0, Actions: []Action{
			{ID: "a", Owner: Attacker, Require: stateOf(fAccess), Forbid: stateOf(fAccess)},
		}},
		{Rounds: 1, Budget: 0, Actions: []Action{
			{ID: "dup", Owner: Attacker},
			{ID: "dup", Owner: Attacker},
		}},
	}
	for i, g := range cases {
		if err := g.Validate(); !errors.Is(err, ErrInvalidGame) {
			t.Fatalf("用例 %d 期望 ErrInvalidGame, 得到 %v", i, err)
		}
		if _, err := Solve(g, Options{}); !errors.Is(err, ErrInvalidGame) {
			t.Fatalf("用例 %d Solve 期望 ErrInvalidGame, 得到 %v", i, err)
		}
	}
}

func TestNodeLimitRejected(t *testing.T) {
	g := chainGame(3, 5, 2, 3)
	if _, err := Solve(g, Options{MaxNodes: 2}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("期望 ErrLimitExceeded, 得到 %v", err)
	}
}

func TestRandomGamesMatchBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(20261111))
	facts := 5
	for trial := 0; trial < 200; trial++ {
		g := randomGame(rng, facts)
		sol, err := Solve(g, Options{})
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		want := bruteMinimax(g, g.Initial, 0, g.Budget)
		if want != sol.Outcome() {
			t.Fatalf("trial %d: 求解器 %s 与独立极小极大 %s 不一致", trial, sol.Outcome(), want)
		}
		if err := ValidateStrategy(g, sol); err != nil {
			t.Fatalf("trial %d: 回放失败: %v", trial, err)
		}
		// 极小模型穷举在小规模随机博弈上也必须可复现且自洽。
		report, err := Verify(g)
		if err != nil {
			t.Fatalf("trial %d: Verify: %v", trial, err)
		}
		for _, fw := range report.Failures {
			for _, s := range fw.States {
				assertMinimal(t, report.ReachableStates, s, failurePred(g, fw))
			}
		}
	}
}

func randomGame(rng *rand.Rand, facts int) *Game {
	randomState := func() State {
		var s State
		for f := 0; f < facts; f++ {
			if rng.Intn(2) == 1 {
				s = s.With(Fact(f))
			}
		}
		return s
	}
	actionCount := 2 + rng.Intn(4)
	actions := make([]Action, 0, actionCount*2)
	for i := 0; i < actionCount; i++ {
		req := randomState()
		actions = append(actions, Action{
			ID:      "atk-" + string(rune('a'+i)),
			Owner:   Attacker,
			Require: req,
			Add:     randomState(),
			Delete:  randomState() &^ req,
		})
		if rng.Intn(2) == 1 {
			actions = append(actions, Action{
				ID:      "def-" + string(rune('a'+i)),
				Owner:   Defender,
				Require: req,
				Add:     randomState(),
				Delete:  randomState() &^ req,
				Cost:    rng.Intn(4),
			})
		}
	}
	return &Game{
		Initial: randomState(),
		Goal:    Goal{Required: randomState()},
		Rounds:  1 + rng.Intn(2),
		Budget:  rng.Intn(6),
		Actions: actions,
	}
}

func TestMinimalEnumerationHandlesNonMonotone(t *testing.T) {
	// 非单调谓词: 恰有两个事实成立。验证极小模型不被单调性假设污染。
	candidates := make([]State, 0, 1<<4)
	for s := State(0); s < 1<<4; s++ {
		candidates = append(candidates, s)
	}
	pred := func(s State) bool { return s.Size() == 2 }
	minimals := EnumerateInclusionMinimal(candidates, pred)
	if len(minimals) != 6 {
		t.Fatalf("4 个事实中恰含 2 个的极小模型应有 6 个, 得到 %d: %v", len(minimals), minimals)
	}
	for _, s := range minimals {
		if s.Size() != 2 {
			t.Fatalf("极小模型事实数错误: %#x", uint16(s))
		}
	}

	empty := EnumerateInclusionMinimal(candidates, func(State) bool { return false })
	if empty != nil {
		t.Fatalf("谓词恒假时应返回 nil")
	}
	zero := EnumerateInclusionMinimal(candidates, func(s State) bool { return s == 0 })
	if len(zero) != 1 || zero[0] != 0 {
		t.Fatalf("空状态应是唯一极小模型, 得到 %v", zero)
	}
}

func TestNextMoveAcrossFullPlay(t *testing.T) {
	g := chainGame(2, 5, 2, 3)
	sol, err := Solve(g, Options{})
	if err != nil {
		t.Fatal(err)
	}
	state, budget, ply := g.Initial, g.Budget, 0
	for ply < g.Plies() && !g.Goal.Satisfied(state) {
		id, pass, ok := sol.NextMove(state, ply, budget)
		if !ok {
			t.Fatalf("第 %d 半步策略查询失败", ply)
		}
		side := g.sideAt(ply)
		if pass {
			ply++
			continue
		}
		a, exists := g.actionByID(id)
		if !exists || a.Owner != side {
			t.Fatalf("策略返回了不属于当前行棋方的动作: %q", id)
		}
		if side == Defender {
			if a.Cost > budget {
				t.Fatalf("策略动作 %q 超出剩余预算", id)
			}
			budget -= a.Cost
		}
		state = a.Apply(state)
		ply++
	}
	if g.Goal.Satisfied(state) {
		t.Fatalf("守方必胜策略下目标不应被达成")
	}
}
