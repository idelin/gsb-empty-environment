package adgame

import "fmt"

// ReplayError 描述一次策略回放中发现的不一致。
type ReplayError struct {
	// Ply 是发现问题时已经走过的半步数。
	Ply int
	// State 是当时的事实集合。
	State State
	// Message 描述具体的不一致。
	Message string
}

// Error 实现 error 接口。
func (e *ReplayError) Error() string {
	return fmt.Sprintf("adgame: 策略回放在第 %d 半步 (状态 %016b 截断为 %#x) 失败: %s",
		e.Ply, uint16(e.State), uint16(e.State), e.Message)
}

// ValidateStrategy 用独立于求解器记忆化的递归回放核对策略 DAG:
//
//   - 每条边的动作在源局面上确实合法 (前置与预算) 且后继状态正确;
//   - 节点胜负与独立极小极大递归 (bruteMinimax) 的结论完全一致;
//   - 守方获胜根节点上, 守方沿 Winning 边可在攻方任意分支下守住目标;
//   - 攻方获胜根节点上, 攻方沿 Winning 边存在一条强制获胜反策略。
//
// 返回 nil 表示策略 DAG 通过全部检查。
func ValidateStrategy(g *Game, sol *Solution) error {
	if err := g.Validate(); err != nil {
		return err
	}
	visited := make(map[memoKey]bool)
	return validateNode(g, sol, sol.Root(), visited)
}

func validateNode(g *Game, sol *Solution, n *Node, visited map[memoKey]bool) error {
	key := memoKey{state: n.State, ply: n.Ply, budget: n.Budget}
	if visited[key] {
		return nil
	}
	visited[key] = true

	want := bruteMinimax(g, n.State, n.Ply, n.Budget)
	if want != n.Outcome {
		return &ReplayError{Ply: n.Ply, State: n.State,
			Message: fmt.Sprintf("节点胜负 %s 与独立极小极大结论 %s 不一致", n.Outcome, want)}
	}

	if n.Terminal {
		if len(n.Edges) != 0 {
			return &ReplayError{Ply: n.Ply, State: n.State, Message: "终止节点不应包含边"}
		}
		goalHit := g.Goal.Satisfied(n.State)
		if goalHit != n.GoalHit {
			return &ReplayError{Ply: n.Ply, State: n.State, Message: "终止原因 GoalHit 标记错误"}
		}
		if goalHit && n.Outcome != AttackerWins {
			return &ReplayError{Ply: n.Ply, State: n.State, Message: "目标成立却未判攻方获胜"}
		}
		if !goalHit && n.Ply == g.Plies() && n.Outcome != DefenderWins {
			return &ReplayError{Ply: n.Ply, State: n.State, Message: "半步耗尽却未判守方获胜"}
		}
		return nil
	}

	moves := g.LegalMoves(n.Side, n.State, n.Budget)
	if len(moves) == 0 {
		if len(n.Edges) != 1 || !n.Edges[0].Pass {
			return &ReplayError{Ply: n.Ply, State: n.State, Message: "无合法动作时应有且仅有一条停着边"}
		}
		child := sol.Node(n.Edges[0].To)
		if child == nil || child.State != n.State || child.Ply != n.Ply+1 {
			return &ReplayError{Ply: n.Ply, State: n.State, Message: "停着边后继局面错误"}
		}
		return validateNode(g, sol, child, visited)
	}

	if len(n.Edges) != len(moves) {
		return &ReplayError{Ply: n.Ply, State: n.State,
			Message: fmt.Sprintf("合法动作 %d 个但策略边 %d 条, 分支不完整", len(moves), len(n.Edges))}
	}

	hasWinning := false
	for i := range moves {
		a := &moves[i]
		e := findEdge(n, a.ID)
		if e == nil {
			return &ReplayError{Ply: n.Ply, State: n.State,
				Message: fmt.Sprintf("缺少合法动作 %q 的分支", a.ID)}
		}
		child := sol.Node(e.To)
		if child == nil {
			return &ReplayError{Ply: n.Ply, State: n.State, Message: "边指向不存在的节点"}
		}
		wantState := a.Apply(n.State)
		wantBudget := n.Budget
		if n.Side == Defender {
			wantBudget -= a.Cost
		}
		if child.State != wantState {
			return &ReplayError{Ply: n.Ply, State: n.State,
				Message: fmt.Sprintf("动作 %q 后继状态错误", a.ID)}
		}
		if child.Ply != n.Ply+1 {
			return &ReplayError{Ply: n.Ply, State: n.State,
				Message: fmt.Sprintf("动作 %q 后继半步/预算错误", a.ID)}
		}
		// 节点存储规范化预算 (折叠了花不出去的富余部分), 因此不能直接与
		// 真实剩余预算比较大小。改用真实剩余预算调用纯语义参考实现,
		// 并额外验证“规范化预算”与“真实预算”在该局面上结论等价,
		// 从而交叉证明预算折叠没有改变胜负。
		if bruteMinimax(g, child.State, child.Ply, child.Budget) != child.Outcome {
			return &ReplayError{Ply: child.Ply, State: child.State,
				Message: "后继节点在规范化预算下与纯语义极小极大不一致"}
		}
		if bruteMinimax(g, child.State, child.Ply, wantBudget) != child.Outcome {
			return &ReplayError{Ply: child.Ply, State: child.State,
				Message: "后继节点胜负与纯语义极小极大不一致"}
		}
		if e.Winning {
			hasWinning = true
			if child.Outcome != n.Outcome {
				return &ReplayError{Ply: n.Ply, State: n.State,
					Message: fmt.Sprintf("动作 %q 被标为必胜但后继胜负不一致", a.ID)}
			}
		}
		if err := validateNode(g, sol, child, visited); err != nil {
			return err
		}
	}

	if n.Outcome == AttackerWins && n.Side == Attacker ||
		n.Outcome == DefenderWins && n.Side == Defender {
		if !hasWinning {
			return &ReplayError{Ply: n.Ply, State: n.State, Message: "必胜局面缺少 Winning 边"}
		}
		if n.PolicyPass || n.PolicyActionID == "" {
			return &ReplayError{Ply: n.Ply, State: n.State, Message: "必胜局面缺少确定性策略动作"}
		}
		policyEdge := findEdge(n, n.PolicyActionID)
		if policyEdge == nil || !policyEdge.Winning {
			return &ReplayError{Ply: n.Ply, State: n.State, Message: "策略动作不在 Winning 边中"}
		}
	}
	return nil
}

func findEdge(n *Node, actionID string) *Edge {
	for i := range n.Edges {
		if n.Edges[i].ActionID == actionID {
			return &n.Edges[i]
		}
	}
	return nil
}

// bruteMinimax 是完全独立的极小极大参考实现: 不共享求解器的任何缓存,
// 也不做预算裁剪, 直接按博弈定义递归。预算裁剪只是求解器的记忆化规范化
// 手段, 不改变胜负, 因此纯粹语义实现与求解器在根局面上必须给出相同结论。
func bruteMinimax(g *Game, s State, ply, budget int) Outcome {
	if g.Goal.Satisfied(s) {
		return AttackerWins
	}
	if ply >= g.Plies() {
		return DefenderWins
	}
	side := g.sideAt(ply)
	moves := g.LegalMoves(side, s, budget)
	if len(moves) == 0 {
		return bruteMinimax(g, s, ply+1, budget)
	}
	if side == Attacker {
		for i := range moves {
			if bruteMinimax(g, moves[i].Apply(s), ply+1, budget) == AttackerWins {
				return AttackerWins
			}
		}
		return DefenderWins
	}
	// 守方是选择方: 只要存在一个防护动作能导向守方胜, 守方就选它并保住
	// 目标; 只有全部动作都被攻方利用时才判攻方胜。
	for i := range moves {
		if bruteMinimax(g, moves[i].Apply(s), ply+1, budget-moves[i].Cost) == DefenderWins {
			return DefenderWins
		}
	}
	return AttackerWins
}
