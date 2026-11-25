package migration

import (
	"errors"
	"fmt"

	"prescreen.local/ddd-10554-v3/spec"
)

const maxGeneration = spec.Generation(^uint32(0))

// recoveryCeiling 返回位置上每个范围允许烧录的最大代次：
// 取所有恢复组合中覆盖该位置所有者的恢复映像版本的最小值。
func (m *Model) recoveryCeiling(p spec.Position) map[spec.Scope]spec.Generation {
	ceiling := map[spec.Scope]spec.Generation{}
	for _, sc := range p.Scopes {
		ceiling[sc] = maxGeneration
	}
	for _, combo := range m.chip.Recovery {
		img := m.image(combo.Images[p.Owner])
		for _, sc := range p.Scopes {
			if v := img.Versions[sc]; v < ceiling[sc] {
				ceiling[sc] = v
			}
		}
	}
	return ceiling
}

// burnCandidates 枚举某位置所有合法的单调烧录目标代次向量。
// 旧位置与影子位置都可烧录；目标必须严格升高至少一个范围，
// 且不超过恢复上限，避免把指定恢复组合烧穿。
func (m *Model) burnCandidates(state *spec.State, p spec.Position) []map[spec.Scope]spec.Generation {
	current := state.Counters[p.ID]
	ceiling := m.recoveryCeiling(p)
	type rng struct{ lo, hi spec.Generation }
	ranges := map[spec.Scope]rng{}
	anyHeadroom := false
	for _, sc := range p.Scopes {
		lo := current[sc]
		hi := ceiling[sc]
		if hi < lo {
			return nil
		}
		ranges[sc] = rng{lo: lo, hi: hi}
		if hi > lo {
			anyHeadroom = true
		}
	}
	if !anyHeadroom {
		return nil
	}

	var out []map[spec.Scope]spec.Generation
	var rec func(i int, acc map[spec.Scope]spec.Generation, raised bool)
	rec = func(i int, acc map[spec.Scope]spec.Generation, raised bool) {
		if i == len(p.Scopes) {
			if raised {
				dup := map[spec.Scope]spec.Generation{}
				for sc, g := range acc {
					dup[sc] = g
				}
				out = append(out, dup)
			}
			return
		}
		sc := p.Scopes[i]
		r := ranges[sc]
		for g := r.lo; g <= r.hi; g++ {
			acc[sc] = g
			rec(i+1, acc, raised || g > r.lo)
		}
	}
	rec(0, map[spec.Scope]spec.Generation{}, false)
	return out
}

// LegalActions 枚举具体状态与剩余预算下所有合法动作（含幂等动作）。
func (m *Model) LegalActions(state *spec.State, budgetLeft int) []*spec.Action {
	acts := []*spec.Action{{Kind: spec.ActionYield}}

	if budgetLeft > 0 {
		for _, p := range m.chip.Positions {
			for _, gen := range m.burnCandidates(state, p) {
				acts = append(acts, &spec.Action{
					Kind:     spec.ActionBurn,
					Position: p.ID,
					Gen:      gen,
				})
			}
		}
	}

	for _, owner := range m.chip.Owners {
		phase := state.Pointers[owner].Phase
		switch phase {
		case spec.PhaseStable:
			acts = append(acts, &spec.Action{Kind: spec.ActionRequest, Owner: owner})
		case spec.PhasePending:
			// 请求与确认在 pending 下均可重放，表达掉电恢复的幂等性。
			acts = append(acts, &spec.Action{Kind: spec.ActionRequest, Owner: owner})
			acts = append(acts, &spec.Action{Kind: spec.ActionConfirm, Owner: owner})
		case spec.PhaseCommitted:
			acts = append(acts, &spec.Action{Kind: spec.ActionConfirm, Owner: owner})
		}
	}
	return acts
}

// IsLegal 按完整语义校验单个动作在具体状态与剩余预算下是否合法。
func (m *Model) IsLegal(state *spec.State, action *spec.Action, budgetLeft int) bool {
	for _, a := range m.LegalActions(state, budgetLeft) {
		if sameAction(a, action) {
			return true
		}
	}
	return false
}

func sameAction(a, b *spec.Action) bool {
	if a.Kind != b.Kind || a.Owner != b.Owner || a.Position != b.Position {
		return false
	}
	if len(a.Gen) != len(b.Gen) {
		return false
	}
	for sc, g := range a.Gen {
		if b.Gen[sc] != g {
			return false
		}
	}
	return true
}

// Apply 执行动作并返回确定性的后继具体状态（不含掉电分支）。
// 后继状态必须仍然满足安全不变量，否则动作在该状态下被拒绝。
func (m *Model) Apply(state *spec.State, action *spec.Action) (*spec.State, error) {
	next := cloneState(state)
	switch action.Kind {
	case spec.ActionYield:
		// 纯观测动作，状态不变。
	case spec.ActionBurn:
		p, ok := m.positions[action.Position]
		if !ok {
			return nil, fmt.Errorf("未知位置 %q", action.Position)
		}
		if len(action.Gen) != len(p.Scopes) {
			return nil, errors.New("烧录目标代次向量与位置范围不匹配")
		}
		raised := false
		for _, sc := range p.Scopes {
			target, ok := action.Gen[sc]
			if !ok {
				return nil, fmt.Errorf("烧录目标缺少范围 %q", sc)
			}
			cur := next.Counters[p.ID][sc]
			if target < cur {
				return nil, errors.New("回滚烧录被拒绝：代次只能单调升高")
			}
			if target > cur {
				raised = true
			}
			next.Counters[p.ID][sc] = target
		}
		if !raised {
			return nil, errors.New("空烧录被拒绝：至少一个范围必须升高")
		}
	case spec.ActionRequest:
		phase := next.Pointers[action.Owner].Phase
		if phase == spec.PhaseCommitted {
			return nil, errors.New("已确认切换后不能再请求切换")
		}
		next.Pointers[action.Owner] = spec.Pointer{Phase: spec.PhasePending}
	case spec.ActionConfirm:
		phase := next.Pointers[action.Owner].Phase
		if phase == spec.PhaseStable {
			return nil, errors.New("尚未请求切换，不能确认")
		}
		next.Pointers[action.Owner] = spec.Pointer{Phase: spec.PhaseCommitted}
	default:
		return nil, fmt.Errorf("未知动作类型 %q", action.Kind)
	}
	if !m.Safe(next) {
		return nil, errors.New("动作会破坏安全不变量，拒绝执行")
	}
	return next, nil
}

// Outcome 是动作之后环境可能给出的结果。
type Outcome struct {
	Observation *spec.Observation
	State       *spec.State
	// Crashed 为 true 表示掉电重启（boot 观测），false 表示芯片确认（ack）。
	Crashed bool
	// Burns 为该分支消耗的烧录次数（0 或 1）。
	Burns int
}

// Outcomes 返回动作之后最坏环境可选择的全部结果。
//
// burn/request/confirm 都是可掉电的持久动作：要么芯片确认（ack，
// 无内部信息外泄），要么掉电重启（boot，仅公开读回）。
// yield 只产生 boot，且预算重置由会话层处理。
func (m *Model) Outcomes(state *spec.State, action *spec.Action) ([]*Outcome, error) {
	next, err := m.Apply(state, action)
	if err != nil {
		return nil, err
	}
	burns := 0
	if action.Kind == spec.ActionBurn {
		burns = 1
	}
	if action.Kind == spec.ActionYield {
		return []*Outcome{{
			Observation: m.BootObservation(next),
			State:       next,
			Crashed:     true,
			Burns:       0,
		}}, nil
	}
	return []*Outcome{
		{Observation: &spec.Observation{Kind: spec.ObsAck}, State: next, Crashed: false, Burns: burns},
		{Observation: m.BootObservation(next), State: next, Crashed: true, Burns: burns},
	}, nil
}
