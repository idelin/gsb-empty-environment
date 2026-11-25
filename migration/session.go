package migration

import (
	"errors"
	"fmt"

	"prescreen.local/ddd-10554-v3/spec"
)

// Session 是启动宿主侧的迁移会话。
//
// 会话只保存公开信息：当前信念指纹与剩余烧录预算，
// 不假设宿主能够读取指针阶段或非公开位置。每次掉电重启后，
// 宿主用新的公开读回调用 Observe 即可恢复到正确信念。
type Session struct {
	planner    *Planner
	beliefKey  string
	budgetLeft int
}

// NewSession 在已求解的规划器上开启一次迁移会话。
func (p *Planner) NewSession() *Session {
	return &Session{
		planner:    p,
		beliefKey:  p.rootKey,
		budgetLeft: p.model.chip.MaxBurns,
	}
}

// Done 报告当前信念是否已确定迁移完成（所有相容状态均为终止态）。
func (s *Session) Done() bool {
	return s.planner.allTerminal(s.planner.nodes[s.beliefKey])
}

// BudgetLeft 返回本会话剩余烧录预算。
func (s *Session) BudgetLeft() int { return s.budgetLeft }

// BeliefKey 返回当前信念的规范指纹（可用于审计日志）。
func (s *Session) BeliefKey() string { return s.beliefKey }

// NextAction 返回策略在当前信念与剩余预算下选择的动作。
// 迁移已完成时返回 nil。
func (s *Session) NextAction() *spec.Action {
	if s.Done() {
		return nil
	}
	choice, ok := s.planner.choice[s.beliefKey][s.budgetLeft]
	if !ok {
		return nil
	}
	return choice.action
}

// Observe 在动作执行后用实际观测推进会话。
//
// ack 观测保持信念不变，并按动作类型扣减烧录预算；
// boot 观测按读回键切换到后继信念，且烧录预算重置为芯片上限
// （掉电开启新的升级会话）。重复恢复时若动作已经生效，
// 规划器中的动作均为幂等重放，不会重复计费。
func (s *Session) Observe(action *spec.Action, obs *spec.Observation) error {
	if s.Done() {
		return errors.New("迁移已完成，不再接受动作")
	}
	n, ok := s.planner.nodes[s.beliefKey]
	if !ok {
		return fmt.Errorf("未知信念 %q", s.beliefKey)
	}
	ak := actionFingerprint(action)
	switch obs.Kind {
	case spec.ObsAck:
		boot, ok := n.edges[ak]
		if !ok {
			return fmt.Errorf("动作 %q 在当前信念下没有枚举到 ack 分支", ak)
		}
		child, ok := boot[observationFingerprint(obs)]
		if !ok {
			return errors.New("ack 与任何枚举后继都不匹配，芯片规格或观测被破坏")
		}
		s.beliefKey = child
		if action.Kind == spec.ActionBurn {
			if s.budgetLeft < 1 {
				return errors.New("烧录预算不足")
			}
			s.budgetLeft--
		}
	case spec.ObsBoot:
		boot, ok := n.edges[ak]
		if !ok {
			return fmt.Errorf("动作 %q 在当前信念下没有枚举到 boot 分支", ak)
		}
		child, ok := boot[observationFingerprint(obs)]
		if !ok {
			return errors.New("读回与任何枚举后继都不匹配，芯片规格或观测被破坏")
		}
		s.beliefKey = child
		s.budgetLeft = s.planner.model.chip.MaxBurns
	default:
		return fmt.Errorf("未知观测类型 %q", obs.Kind)
	}
	return nil
}

// PolicyValue 返回当前（信念，预算）格点策略保证的最坏剩余烧录次数。
func (s *Session) PolicyValue() (int, bool) {
	if s.Done() {
		return 0, true
	}
	choice, ok := s.planner.choice[s.beliefKey][s.budgetLeft]
	if !ok {
		return 0, false
	}
	return choice.value, true
}
