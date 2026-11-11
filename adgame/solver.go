package adgame

import (
	"fmt"
	"sort"
)

// DefaultMaxNodes 是求解器默认允许记忆化的不同 (状态, 半步, 预算) 条目上限。
const DefaultMaxNodes = 1_000_000

// Outcome 是一个博弈节点的确定胜负。
type Outcome uint8

const (
	// AttackerWins 表示攻方可以在剩余半步内强制达成目标。
	AttackerWins Outcome = iota
	// DefenderWins 表示守方可以保证目标在剩余半步内不被达成。
	DefenderWins
)

// String 返回胜负的可读名称。
func (o Outcome) String() string {
	switch o {
	case AttackerWins:
		return "attacker-wins"
	case DefenderWins:
		return "defender-wins"
	default:
		return fmt.Sprintf("outcome(%d)", int(o))
	}
}

// Options 控制求解器的资源上限。
type Options struct {
	// MaxNodes 限制记忆化节点数, 0 表示使用 DefaultMaxNodes; 负数表示不限制。
	MaxNodes int
}

// Edge 是策略 DAG 中从一个节点经一个合法动作 (或停着) 到后继节点的边。
type Edge struct {
	// ActionID 是动作标识; Pass 为 true 时为空。
	ActionID string
	// Pass 表示本行棋方没有任何合法动作, 该边是自动停着。
	Pass bool
	// To 是后继节点 ID。
	To int
	// Cost 是该动作实际扣减的守方预算。
	Cost int
	// Winning 表示沿该边是否保持本节点的确定胜负。
	// 守方获胜节点上的 Winning 边即全部可保住目标的防护动作;
	// 攻方获胜节点上的 Winning 边即攻方反策略动作。
	Winning bool
}

// Node 是策略 DAG 中的一个博弈局面。相同 (状态, 半步, 预算) 局面
// 只构建一次并被多条路径共享, 因此整体结构是 DAG 而不是重复展开的树。
type Node struct {
	// ID 是节点在 Solution 内的稳定标识。
	ID int
	// State 是当前事实集合。
	State State
	// Ply 是已经走过的半步数, 取值 [0, Game.Plies()]。
	Ply int
	// Budget 是守方当前剩余预算。
	Budget int
	// Side 是下一个行棋方; 终止节点上为对应半步的行棋方。
	Side Side
	// Terminal 表示该节点不再产生行棋。
	Terminal bool
	// GoalHit 表示终止原因是攻方目标成立 (true) 或半步耗尽 (false)。
	GoalHit bool
	// Outcome 是该局面的确定胜负。
	Outcome Outcome
	// Edges 包含本局面的全部合法动作分支 (停着局面仅有一条 Pass 边)。
	Edges []Edge
	// PolicyActionID 是确定性策略选出的动作 ID;
	// 守方获胜局面上它是第一个可保住胜利的防护动作,
	// 攻方获胜局面上它是攻方的第一个必胜动作。无法选出时为空。
	PolicyActionID string
	// PolicyPass 为 true 时策略动作是自动停着。
	PolicyPass bool
}

// Stats 汇总一次求解的展开规模, 便于宿主评估防护动作的实际影响。
type Stats struct {
	// MemoStates 是记忆化的不同局面数 (DAG 节点数)。
	MemoStates int
	// EdgeCount 是全部节点的边数之和 (含停着边)。
	EdgeCount int
	// MaxFanout 是单个节点的最大合法动作分支数。
	MaxFanout int
	// GoalTerminals 是因目标成立而终止的节点数。
	GoalTerminals int
	// HorizonTerminals 是因半步耗尽而终止的节点数。
	HorizonTerminals int
	// PassNodes 是出现“无合法动作 -> 自动停着”的节点数。
	PassNodes int
}

// Solution 是一次极小极大求解的完整结果。
type Solution struct {
	game    *Game
	opts    Options
	nodes   map[memoKey]*Node
	byID    map[int]*Node
	rootID  int
	outcome Outcome
	stats   Stats
}

type memoKey struct {
	state  State
	ply    int
	budget int
}

type solver struct {
	game       *Game
	opts       Options
	nodes      map[memoKey]*Node
	byID       map[int]*Node
	stats      Stats
	maxDefCost int
	nextID     int
}

// Solve 对博弈做记忆化极小极大求解, 返回完整策略 DAG。
// 初始状态已经满足目标时直接得到攻方获胜的终止节点。
func Solve(g *Game, opts Options) (*Solution, error) {
	if err := g.Validate(); err != nil {
		return nil, err
	}
	if opts.MaxNodes == 0 {
		opts.MaxNodes = DefaultMaxNodes
	}
	sv := &solver{
		game:  g,
		opts:  opts,
		nodes: make(map[memoKey]*Node),
		byID:  make(map[int]*Node),
	}
	for i := range g.Actions {
		if a := &g.Actions[i]; a.Owner == Defender && a.Cost > sv.maxDefCost {
			sv.maxDefCost = a.Cost
		}
	}
	root, err := sv.build(g.Initial, 0, g.Budget)
	if err != nil {
		return nil, err
	}
	return &Solution{
		game:    g,
		opts:    opts,
		nodes:   sv.nodes,
		byID:    sv.byID,
		rootID:  root.ID,
		outcome: root.Outcome,
		stats: Stats{
			MemoStates:       len(sv.nodes),
			EdgeCount:        sv.stats.EdgeCount,
			MaxFanout:        sv.stats.MaxFanout,
			GoalTerminals:    sv.stats.GoalTerminals,
			HorizonTerminals: sv.stats.HorizonTerminals,
			PassNodes:        sv.stats.PassNodes,
		},
	}, nil
}

// normBudget 仅用于记忆化键规范化: 把“超过剩余守方半步可花上界”的
// 预算折叠到上界 (多出的部分不可能再被花掉, 局面等价); 小于上界的
// 真实预算保持不变, 因此不同可花预算绝不会被错误合并。
func (sv *solver) normBudget(ply, budget int) int {
	// 只折叠“超过剩余守方半步可花上界”的富余预算; 其余真实预算原样
	// 进入记忆键, 保证不同可花预算不会被合并。
	remaining := 0
	for p := ply; p < sv.game.Plies(); p++ {
		if sv.game.sideAt(p) == Defender {
			remaining++
		}
	}
	if remaining == 0 || sv.maxDefCost == 0 {
		return 0
	}
	capSpend := remaining * sv.maxDefCost
	if budget > capSpend {
		return capSpend
	}
	return budget
}

// Outcome 返回根局面的确定胜负。
func (s *Solution) Outcome() Outcome { return s.outcome }

// Stats 返回展开统计。
func (s *Solution) Stats() Stats { return s.stats }

// Root 返回根节点。
func (s *Solution) Root() *Node { return s.byID[s.rootID] }

// Node 按 ID 返回节点, 不存在时返回 nil。
func (s *Solution) Node(id int) *Node { return s.byID[id] }

// NodeAt 返回与 (状态, 已走半步, 剩余预算) 对应的已展开节点;
// 若该局面未出现在策略 DAG 中则返回 nil。
func (s *Solution) NodeAt(state State, ply, budget int) *Node {
	return s.nodes[memoKey{state: state, ply: ply, budget: s.normBudget(ply, budget)}]
}

// NextMove 是供演练宿主逐步调用的策略查询: 给定当前局面,
// 返回一个确定性选出的合法动作 ID。
//
//   - 守方必胜局面: 返回第一个可保住胜利的防护动作 (PolicyActionID)。
//   - 攻方必胜的攻方局面: 返回第一个攻方必胜动作 (反策略)。
//   - 守方必胜的攻方局面: 攻方走子不由我方决定, 返回第一个合法攻击
//     动作作为确定性代表, 宿主可据此枚举任意对手选择, 全部分支都已
//     在策略 DAG 中展开且导向守方胜。
//
// 若应自动停着则 actionID 为空且 pass 为 true; 若该局面是终止节点,
// 或不在策略 DAG 中, 则 ok 为 false。
func (s *Solution) NextMove(state State, ply, budget int) (actionID string, pass bool, ok bool) {
	n := s.NodeAt(state, ply, budget)
	if n == nil || n.Terminal {
		return "", false, false
	}
	if n.PolicyPass {
		return "", true, true
	}
	if n.PolicyActionID == "" {
		if len(n.Edges) == 0 || n.Edges[0].Pass {
			return "", true, true
		}
		return n.Edges[0].ActionID, false, true
	}
	return n.PolicyActionID, false, true
}

// WinningActions 返回节点上全部能保持本节点确定胜负的动作 ID,
// 即守方节点的全部可保住目标的防护动作, 或攻方节点的全部必胜动作。
// 停着节点返回 nil。
func (s *Solution) WinningActions(n *Node) []string {
	if n == nil {
		return nil
	}
	var out []string
	for _, e := range n.Edges {
		if e.Winning && !e.Pass {
			out = append(out, e.ActionID)
		}
	}
	return out
}

// normBudget 是 Solution 侧与 solver.normBudget 等价的键规范化,
// 保证 NextMove/NodeAt 查询能命中求解阶段写入的记忆条目。
func (s *Solution) normBudget(ply, budget int) int {
	maxCost := 0
	for i := range s.game.Actions {
		if a := &s.game.Actions[i]; a.Owner == Defender && a.Cost > maxCost {
			maxCost = a.Cost
		}
	}
	remaining := 0
	for p := ply; p < s.game.Plies(); p++ {
		if s.game.sideAt(p) == Defender {
			remaining++
		}
	}
	if remaining == 0 || maxCost == 0 {
		return 0
	}
	capSpend := remaining * maxCost
	if budget > capSpend {
		return capSpend
	}
	return budget
}

func (sv *solver) build(state State, ply, budget int) (*Node, error) {
	if sv.game.Goal.Satisfied(state) {
		return sv.terminal(state, ply, budget, true, AttackerWins)
	}
	if ply == sv.game.Plies() {
		return sv.terminal(state, ply, budget, false, DefenderWins)
	}
	keyBudget := sv.normBudget(ply, budget)
	key := memoKey{state: state, ply: ply, budget: keyBudget}
	if n, ok := sv.nodes[key]; ok {
		return n, nil
	}
	if sv.opts.MaxNodes >= 0 && len(sv.nodes) >= sv.opts.MaxNodes {
		return nil, fmt.Errorf("%w: 记忆化节点数达到上限 %d", ErrLimitExceeded, sv.opts.MaxNodes)
	}

	side := sv.game.sideAt(ply)
	moves := sv.game.LegalMoves(side, state, budget)

	// 第一阶段: 递归求出全部后继 (本节点尚未登记, 不会读到半成品)。
	type childRef struct {
		action Action
		pass   bool
		child  *Node
	}
	refs := make([]childRef, 0, len(moves))
	if len(moves) == 0 {
		child, err := sv.build(state, ply+1, budget)
		if err != nil {
			return nil, err
		}
		refs = append(refs, childRef{pass: true, child: child})
	} else {
		for i := range moves {
			a := &moves[i]
			nextBudget := budget
			if side == Defender {
				nextBudget = budget - a.Cost
			}
			child, err := sv.build(a.Apply(state), ply+1, nextBudget)
			if err != nil {
				return nil, err
			}
			refs = append(refs, childRef{action: *a, child: child})
		}
	}

	// 第二阶段: 递归期间兄弟分支可能已完整登记同一记忆键, 幂等复用。
	if existing, ok := sv.nodes[key]; ok {
		return existing, nil
	}
	n := &Node{
		ID:     sv.nextID,
		State:  state,
		Ply:    ply,
		Budget: budget,
		Side:   side,
	}
	sv.nextID++
	sv.nodes[key] = n
	sv.byID[n.ID] = n

	anyAttackerWin := false
	anyDefenderWin := false
	for _, ref := range refs {
		if ref.pass {
			n.Edges = append(n.Edges, Edge{Pass: true, To: ref.child.ID})
		} else {
			n.Edges = append(n.Edges, Edge{
				ActionID: ref.action.ID,
				To:       ref.child.ID,
				Cost:     ref.action.Cost,
			})
		}
		if ref.child.Outcome == AttackerWins {
			anyAttackerWin = true
		} else {
			anyDefenderWin = true
		}
	}

	if n.Side == Attacker {
		// 攻方是存在性玩家: 只要有一条必胜分支即可强制获胜。
		n.Outcome = DefenderWins
		if anyAttackerWin {
			n.Outcome = AttackerWins
		}
	} else {
		// 守方同样是选择自己动作的存在性玩家: 只要存在一个防护动作能
		// 导向守方胜, 守方就选它并保证目标安全。攻方的全称性体现在
		// 后续攻方节点会枚举其全部选择。
		if anyDefenderWin {
			n.Outcome = DefenderWins
		} else {
			n.Outcome = AttackerWins
		}
	}
	for i := range n.Edges {
		e := &n.Edges[i]
		e.Winning = sv.byID[e.To].Outcome == n.Outcome
	}
	if len(moves) == 0 {
		n.PolicyPass = true
		sv.stats.PassNodes++
	}
	if n.Outcome == AttackerWins && n.Side == Attacker ||
		n.Outcome == DefenderWins && n.Side == Defender {
		for _, e := range n.Edges {
			if e.Winning {
				n.PolicyActionID = e.ActionID
				break
			}
		}
	}

	sv.stats.EdgeCount += len(n.Edges)
	if len(n.Edges) > sv.stats.MaxFanout {
		sv.stats.MaxFanout = len(n.Edges)
	}
	return n, nil
}

func (sv *solver) terminal(state State, ply, budget int, goalHit bool, outcome Outcome) (*Node, error) {
	keyBudget := sv.normBudget(ply, budget)
	key := memoKey{state: state, ply: ply, budget: keyBudget}
	if n, ok := sv.nodes[key]; ok {
		return n, nil
	}
	if sv.opts.MaxNodes >= 0 && len(sv.nodes) >= sv.opts.MaxNodes {
		return nil, fmt.Errorf("%w: 记忆化节点数达到上限 %d", ErrLimitExceeded, sv.opts.MaxNodes)
	}
	side := Attacker
	if ply < sv.game.Plies() {
		side = sv.game.sideAt(ply)
	}
	n := &Node{
		ID:       sv.nextID,
		State:    state,
		Ply:      ply,
		Budget:   budget,
		Side:     side,
		Terminal: true,
		GoalHit:  goalHit,
		Outcome:  outcome,
	}
	sv.nextID++
	sv.nodes[key] = n
	sv.byID[n.ID] = n
	if goalHit {
		sv.stats.GoalTerminals++
	} else {
		sv.stats.HorizonTerminals++
	}
	return n, nil
}

// SortedNodeIDs 返回按 (半步, 状态, 预算) 排序的全部节点 ID, 便于宿主稳定遍历。
func (s *Solution) SortedNodeIDs() []int {
	ids := make([]int, 0, len(s.byID))
	for id := range s.byID {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := s.byID[ids[i]], s.byID[ids[j]]
		if a.Ply != b.Ply {
			return a.Ply < b.Ply
		}
		if a.State != b.State {
			return a.State < b.State
		}
		if a.Budget != b.Budget {
			return a.Budget < b.Budget
		}
		return a.ID < b.ID
	})
	return ids
}
