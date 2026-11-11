package adgame

import (
	"errors"
	"fmt"
	"math/bits"
)

// FactCount 是一个博弈最多允许的事实数。
const FactCount = 16

// Fact 是事实编号, 取值范围为 [0, 15]。
type Fact uint8

// State 是至多 16 个事实的位图集合。第 f 位为 1 表示事实 f 成立。
type State uint16

// Side 标识行棋方。
type Side uint8

const (
	// Attacker 是攻方。
	Attacker Side = iota
	// Defender 是守方。
	Defender
)

// String 返回行棋方的可读名称。
func (s Side) String() string {
	switch s {
	case Attacker:
		return "attacker"
	case Defender:
		return "defender"
	default:
		return fmt.Sprintf("side(%d)", int(s))
	}
}

// Has 报告事实 f 是否在状态中。
func (s State) Has(f Fact) bool { return s&(1<<f) != 0 }

// With 返回加入事实 f 后的新状态。
func (s State) With(f Fact) State { return s | (1 << f) }

// Without 返回移除事实 f 后的新状态。
func (s State) Without(f Fact) State { return s &^ (1 << f) }

// Facts 按编号升序返回状态中成立的事实列表。
func (s State) Facts() []Fact {
	out := make([]Fact, 0, bits.OnesCount16(uint16(s)))
	for f := Fact(0); f < FactCount; f++ {
		if s.Has(f) {
			out = append(out, f)
		}
	}
	return out
}

// Size 返回状态中成立事实的数量。
func (s State) Size() int { return bits.OnesCount16(uint16(s)) }

// Contains 报告 s 是否包含 other 的全部事实 (other ⊆ s)。
func (s State) Contains(other State) bool { return s&other == other }

// Action 是一个带前置条件的增删动作。
//
// 动作可执行当且仅当当前状态包含 Require 的全部事实, 且不包含
// Forbid 中的任何事实; 守方动作还要求剩余预算不小于 Cost。
// 执行后状态变为 (state | Add) &^ Delete。
type Action struct {
	// ID 是动作在单个 Game 内唯一的非空标识。
	ID string
	// Owner 是动作的行棋方。
	Owner Side
	// Require 是必须全部成立的前置事实。
	Require State
	// Forbid 是必须全部不成立的前置事实。
	Forbid State
	// Add 是执行时加入的事实集合。
	Add State
	// Delete 是执行时移除的事实集合。
	Delete State
	// Cost 是守方动作的非负整数成本; 攻方动作必须为 0。
	Cost int
}

// Applicable 报告动作在状态 s 上的前置条件是否满足 (不考虑预算)。
func (a Action) Applicable(s State) bool {
	return s.Contains(a.Require) && s&a.Forbid == 0
}

// Apply 执行动作并返回后继状态。它不检查前置条件与预算,
// 调用方应先通过 Game.LegalMoves 取得合法动作。
func (a Action) Apply(s State) State {
	return (s | a.Add) &^ a.Delete
}

// NoOp 报告动作在满足前置条件的状态上执行后是否不改变任何事实。
func (a Action) NoOp(s State) bool { return a.Apply(s) == s }

// Goal 描述攻方的获胜目标: 全部 Required 事实成立,
// 且全部 Forbidden 事实不成立。
type Goal struct {
	Required  State
	Forbidden State
}

// Satisfied 报告状态 s 是否达成目标。
func (g Goal) Satisfied(s State) bool {
	return s.Contains(g.Required) && s&g.Forbidden == 0
}

// Game 是一个有限回合攻防博弈的静态定义。
type Game struct {
	// Name 是可选的博弈名称。
	Name string
	// Initial 是初始事实集合。
	Initial State
	// Goal 是攻方目标。
	Goal Goal
	// Rounds 是回合数; 每回合包含攻方与守方各一个半步。
	Rounds int
	// Start 是第一个半步的行棋方, 零值为攻方先手。
	Start Side
	// Budget 是守方的非负整数预算。
	Budget int
	// Actions 是全部动作; 同一行棋方的动作顺序决定确定性的策略选择。
	Actions []Action
}

// 求解与校验阶段返回的静态错误。
var (
	// ErrInvalidGame 表示博弈定义未通过 Validate 检查, 具体原因见错误文本。
	ErrInvalidGame = errors.New("adgame: invalid game")
	// ErrLimitExceeded 表示展开规模超过 Options 中配置的上限。
	ErrLimitExceeded = errors.New("adgame: expansion limit exceeded")
)

// Validate 检查事实编号、动作标识、成本与回合数等静态约束。
func (g *Game) Validate() error {
	if g.Rounds <= 0 {
		return fmt.Errorf("%w: Rounds 必须为正整数, 得到 %d", ErrInvalidGame, g.Rounds)
	}
	if g.Budget < 0 {
		return fmt.Errorf("%w: Budget 不能为负, 得到 %d", ErrInvalidGame, g.Budget)
	}
	if g.Start != Attacker && g.Start != Defender {
		return fmt.Errorf("%w: 非法 Start 取值 %d", ErrInvalidGame, g.Start)
	}
	if g.Goal.Required&g.Goal.Forbidden != 0 {
		return fmt.Errorf("%w: 目标的 Required 与 Forbidden 存在交集", ErrInvalidGame)
	}
	seenIDs := make(map[string]struct{}, len(g.Actions))
	for i := range g.Actions {
		a := &g.Actions[i]
		if a.ID == "" {
			return fmt.Errorf("%w: Actions[%d] 缺少 ID", ErrInvalidGame, i)
		}
		if _, ok := seenIDs[a.ID]; ok {
			return fmt.Errorf("%w: 动作 ID 重复: %q", ErrInvalidGame, a.ID)
		}
		seenIDs[a.ID] = struct{}{}
		if a.Owner != Attacker && a.Owner != Defender {
			return fmt.Errorf("%w: 动作 %q 的 Owner 非法", ErrInvalidGame, a.ID)
		}
		if a.Cost < 0 {
			return fmt.Errorf("%w: 动作 %q 的 Cost 不能为负", ErrInvalidGame, a.ID)
		}
		if a.Owner == Attacker && a.Cost != 0 {
			return fmt.Errorf("%w: 攻方动作 %q 的 Cost 必须为 0", ErrInvalidGame, a.ID)
		}
		if a.Require&a.Forbid != 0 {
			return fmt.Errorf("%w: 动作 %q 的 Require 与 Forbid 存在交集", ErrInvalidGame, a.ID)
		}
	}
	return nil
}

// Plies 返回总半步数; 每回合攻守各一个半步。
func (g *Game) Plies() int { return g.Rounds * 2 }

// sideAt 返回第 ply 个半步 (从 0 开始) 的行棋方。
func (g *Game) sideAt(ply int) Side {
	if g.Start == Attacker {
		if ply%2 == 0 {
			return Attacker
		}
		return Defender
	}
	if ply%2 == 0 {
		return Defender
	}
	return Attacker
}

// LegalMoves 返回行棋方 side 在状态 s、剩余预算 budget 下可执行的动作,
// 保持 Game.Actions 的声明顺序。返回空切片表示无合法动作 (调用方按停着处理)。
func (g *Game) LegalMoves(side Side, s State, budget int) []Action {
	var out []Action
	for i := range g.Actions {
		a := &g.Actions[i]
		if a.Owner != side || !a.Applicable(s) {
			continue
		}
		if side == Defender && a.Cost > budget {
			continue
		}
		out = append(out, *a)
	}
	return out
}

// actionByID 按 ID 查找动作, 未找到时返回 false。
func (g *Game) actionByID(id string) (Action, bool) {
	for i := range g.Actions {
		if g.Actions[i].ID == id {
			return g.Actions[i], true
		}
	}
	return Action{}, false
}
