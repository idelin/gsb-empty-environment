package recovery

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// State 是隐藏状态编号。隐藏状态记录真实的发送历史(例如最后一帧是否
// 实际上线、持久回执是否与当前事务匹配、秘密任务是否提前结束)。
type State int

// Action 是恢复动作编号。动作语义由模型转移表给出。
type Action int

// Transition 是一条对抗性非确定转移:在某一隐藏状态下执行某动作后,
// 环境(相容发送历史)可选择转移到 To,产生 Cost 个额外帧,并公开
// 观察 Obs。Cost 必须 >= 1,以保证"有限值即可终止"的论证成立。
type Transition struct {
	To   State
	Cost int
	Obs  Observation
}

// Model 描述一个恢复博弈实例。
type Model struct {
	Name string
	// Start 是初始信念:掉线时刻与共同公开历史相容的所有隐藏状态。
	Start []State
	// Goal 标记已完成的隐藏状态。信念中所有状态均为 Goal 时恢复完成。
	// Goal 状态不参与转移:恢复动作只受未完成状态约束。
	Goal map[State]bool
	// Steps 是转移表:Steps[s][a] 为状态 s 下动作 a 的可能转移集合。
	// 某状态缺少某动作(或转移为空)表示该动作在该状态下无效;
	// 对信念内任一未完成状态无效的动作对整个信念无效。
	Steps map[State]map[Action][]Transition
	// StateNames 与 ActionNames 仅用于报告,可省略。
	StateNames  map[State]string
	ActionNames map[Action]string
}

func (m *Model) validate() error {
	if len(m.Start) == 0 {
		return errors.New("recovery: 初始信念为空")
	}
	for s, acts := range m.Steps {
		for a, trs := range acts {
			if len(trs) == 0 {
				return fmt.Errorf("recovery: 状态 %d 动作 %d 的转移集合为空", s, a)
			}
			for _, t := range trs {
				if t.Cost < 1 {
					return fmt.Errorf("recovery: 状态 %d 动作 %d 的转移成本 %d 小于 1", s, a, t.Cost)
				}
			}
		}
	}
	return nil
}

// BeliefKey 返回一组隐藏状态构成的信念的规范键。信念即公开历史等价类:
// 两个隐藏状态属于同一信念,当且仅当它们与同一共同公开历史相容。
func BeliefKey(states ...State) string {
	sorted := sortedUnique(states)
	parts := make([]string, len(sorted))
	for i, s := range sorted {
		parts[i] = strconv.Itoa(int(s))
	}
	return strings.Join(parts, ",")
}

func sortedUnique(states []State) []State {
	s := append([]State(nil), states...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}
