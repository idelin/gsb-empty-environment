package netsec

import (
	"fmt"
	"sort"
)

// Condition 描述一条状态迁移边所需的触发条件。
type Condition int

const (
	// ButtonPress 是物理按键等本地触发条件，不需要管理授权。
	ButtonPress Condition = iota
	// SignatureProof 要求管理签名证明，经过该边视为已获得管理授权。
	SignatureProof
)

// RequiresManagementAuth 报告经过该条件是否构成管理授权。
func (c Condition) RequiresManagementAuth() bool { return c == SignatureProof }

// DigestSet 是镜像摘要集合。
type DigestSet map[string]bool

// Digests 由若干摘要构造 DigestSet。
func Digests(ds ...string) DigestSet {
	set := make(DigestSet, len(ds))
	for _, d := range ds {
		set[d] = true
	}
	return set
}

// IsSubsetOf 报告 s 是否为 base 的子集。
func (s DigestSet) IsSubsetOf(base DigestSet) bool {
	for d := range s {
		if !base[d] {
			return false
		}
	}
	return true
}

// Edge 是状态机的一条迁移边。
type Edge struct {
	Event string    // 触发事件名，用于还原事件序列
	To    string    // 目标状态名
	Cond  Condition // 迁移条件
}

// State 是启动模式状态机的一个状态。
type State struct {
	Name   string
	Images DigestSet // 该状态允许加载的镜像摘要集合
	Edges  []Edge
}

// Machine 是启动模式的有限状态机。
type Machine struct {
	Entry  string
	States map[string]*State
}

// State 按名取状态，不存在时返回错误。
func (m *Machine) State(name string) (*State, error) {
	s, ok := m.States[name]
	if !ok {
		return nil, fmt.Errorf("netsec: unknown state %q", name)
	}
	return s, nil
}

// Violation 描述一次未授权扩权：不具备管理授权，
// 经 Events 事件序列到达了镜像集合超出基线的状态 State。
type Violation struct {
	State  string   // 到达的宽权限状态
	Events []string // 最短事件序列
	Extra  []string // 超出基线的镜像摘要，升序
}

func (v *Violation) Error() string {
	return fmt.Sprintf("netsec: recovery policy violated: state %q reachable without management authorization via %v (extra digests %v)",
		v.State, v.Events, v.Extra)
}

// FindWideningPath 从 Entry 出发，只经过不需要管理授权的边做广度优先搜索，
// 返回到达首个镜像集合超出 baseline 的状态的最短事件序列。
// 不存在这样的路径时返回 nil。重启边等成环边通过已访问集合终止。
func (m *Machine) FindWideningPath(baseline DigestSet) *Violation {
	type node struct {
		state  string
		events []string
	}
	entry, err := m.State(m.Entry)
	if err != nil {
		return &Violation{State: m.Entry, Extra: []string{fmt.Sprintf("unknown entry state: %v", err)}}
	}
	if v := widening(entry, nil, baseline); v != nil {
		return v
	}
	visited := map[string]bool{m.Entry: true}
	queue := []node{{state: m.Entry}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		cs, err := m.State(cur.state)
		if err != nil {
			continue
		}
		for _, e := range cs.Edges {
			if e.Cond.RequiresManagementAuth() {
				continue // 无管理授权时签名证明失败，该边不可通过
			}
			if visited[e.To] {
				continue
			}
			visited[e.To] = true
			next, err := m.State(e.To)
			if err != nil {
				continue
			}
			events := append(append([]string{}, cur.events...), e.Event)
			if v := widening(next, events, baseline); v != nil {
				return v
			}
			queue = append(queue, node{state: e.To, events: events})
		}
	}
	return nil
}

func widening(s *State, events []string, baseline DigestSet) *Violation {
	if s.Images.IsSubsetOf(baseline) {
		return nil
	}
	extra := make([]string, 0, len(s.Images))
	for d := range s.Images {
		if !baseline[d] {
			extra = append(extra, d)
		}
	}
	sort.Strings(extra)
	return &Violation{State: s.Name, Events: events, Extra: extra}
}

// CheckRecoveryPolicy 以 normal 状态的镜像集合为基线，校验恢复路径
// 不会在不具备管理授权的情况下加载更宽的镜像集合。
// 违反策略时返回 *Violation 错误，否则返回 nil。
func (m *Machine) CheckRecoveryPolicy(normal string) error {
	ns, err := m.State(normal)
	if err != nil {
		return err
	}
	if v := m.FindWideningPath(ns.Images); v != nil {
		return v
	}
	return nil
}
