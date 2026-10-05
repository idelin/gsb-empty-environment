// Package reclaimsim 模拟复制日志的前缀回收。
//
// 核心规则：
//  1. 只有所有活跃副本的已复制水位都越过某前缀后，该前缀才可回收。
//  2. 被剔除的成员在确认剔除之前仍参与水位计算（继续阻挡回收）。
//  3. 重加入的副本以新代次（generation）恢复，旧代次的心跳不能提升水位。
//  4. 乱序心跳（matched 不高于当前水位）被忽略，水位只增不减。
package reclaimsim

import (
	"fmt"
	"sort"
)

// MemberState 表示成员在水位计算中的角色。
type MemberState int

const (
	// Active 正常参与复制与水位计算。
	Active MemberState = iota
	// PendingRemoval 已发起剔除但未确认，仍阻挡回收。
	PendingRemoval
)

func (s MemberState) String() string {
	if s == PendingRemoval {
		return "pending-removal"
	}
	return "active"
}

// Member 记录单个副本的复制进度。
type Member struct {
	ID         string
	Generation uint64
	Matched    uint64 // 已确认复制的最高日志下标（绝对下标）
	State      MemberState
}

// Simulator 维护日志、成员集合与回收水位。
type Simulator struct {
	baseIndex uint64 // 已回收前缀长度；有效日志为 (baseIndex, baseIndex+logLen]
	logLen    uint64
	members   map[string]*Member
}

// New 创建一个模拟器，initialMembers 为初始成员（代次从 1 开始，水位为 0）。
func New(initialMembers ...string) *Simulator {
	s := &Simulator{members: make(map[string]*Member)}
	for _, id := range initialMembers {
		s.members[id] = &Member{ID: id, Generation: 1, State: Active}
	}
	return s
}

// Append 向日志追加 n 条记录，返回追加后的最高下标。
func (s *Simulator) Append(n uint64) uint64 {
	s.logLen += n
	return s.baseIndex + s.logLen
}

// BaseIndex 返回已回收前缀的长度（即可回收下界）。
func (s *Simulator) BaseIndex() uint64 { return s.baseIndex }

// LastIndex 返回当前日志的最高绝对下标。
func (s *Simulator) LastIndex() uint64 { return s.baseIndex + s.logLen }

// Member 返回成员快照，不存在时 ok=false。
func (s *Simulator) Member(id string) (m Member, ok bool) {
	if mm, found := s.members[id]; found {
		return *mm, true
	}
	return Member{}, false
}

// Heartbeat 处理副本心跳。仅当代次匹配且 matched 严格推进时才提升水位；
// 旧代次或乱序（matched 回退）的心跳被忽略并返回错误。
func (s *Simulator) Heartbeat(id string, generation, matched uint64) error {
	m, ok := s.members[id]
	if !ok {
		return fmt.Errorf("heartbeat from unknown member %q", id)
	}
	if generation != m.Generation {
		return fmt.Errorf("stale heartbeat from %q: generation %d, current %d", id, generation, m.Generation)
	}
	if matched > s.LastIndex() {
		return fmt.Errorf("heartbeat from %q: matched %d beyond last index %d", id, matched, s.LastIndex())
	}
	if matched <= m.Matched {
		return fmt.Errorf("out-of-order heartbeat from %q: matched %d, current %d", id, matched, m.Matched)
	}
	m.Matched = matched
	return nil
}

// AddMember 以代次 1 加入新成员，水位从已回收前缀处起算。
func (s *Simulator) AddMember(id string) error {
	if _, ok := s.members[id]; ok {
		return fmt.Errorf("member %q already exists", id)
	}
	s.members[id] = &Member{ID: id, Generation: 1, Matched: s.baseIndex, State: Active}
	return nil
}

// RemoveMember 发起剔除：成员转入 PendingRemoval，确认前仍参与水位计算。
func (s *Simulator) RemoveMember(id string) error {
	m, ok := s.members[id]
	if !ok {
		return fmt.Errorf("member %q not found", id)
	}
	if m.State == PendingRemoval {
		return fmt.Errorf("member %q already pending removal", id)
	}
	m.State = PendingRemoval
	return nil
}

// ConfirmRemoval 确认剔除，成员彻底退出水位计算。
func (s *Simulator) ConfirmRemoval(id string) error {
	m, ok := s.members[id]
	if !ok {
		return fmt.Errorf("member %q not found", id)
	}
	if m.State != PendingRemoval {
		return fmt.Errorf("member %q is not pending removal", id)
	}
	delete(s.members, id)
	return nil
}

// Rejoin 使成员以新代次重加入。代次必须大于当前代次；
// 水位重置到已回收前缀处（更早的数据已被回收，需从新代次的快照恢复）。
func (s *Simulator) Rejoin(id string, newGeneration uint64) error {
	m, ok := s.members[id]
	if !ok {
		return fmt.Errorf("member %q not found", id)
	}
	if newGeneration <= m.Generation {
		return fmt.Errorf("rejoin of %q: generation %d not greater than current %d", id, newGeneration, m.Generation)
	}
	m.Generation = newGeneration
	m.Matched = s.baseIndex
	m.State = Active
	return nil
}

// Reclaimable 返回当前可回收的前缀（绝对下标）与阻挡节点列表。
// 可回收前缀 = 所有成员（含待确认剔除者）水位的最小值；
// 水位等于该最小值的成员即阻挡节点，按 ID 排序返回。
func (s *Simulator) Reclaimable() (prefix uint64, blockers []string) {
	first := true
	for _, m := range s.members {
		if first || m.Matched < prefix {
			prefix = m.Matched
			first = false
		}
	}
	if first { // 无成员时不可回收任何数据，保持已回收位置
		return s.baseIndex, nil
	}
	for _, m := range s.members {
		if m.Matched == prefix {
			blockers = append(blockers, m.ID)
		}
	}
	sort.Strings(blockers)
	return prefix, blockers
}

// Reclaim 执行回收：截断 (baseIndex, Reclaimable()] 之外不允许回收。
// 返回实际回收到的下标。
func (s *Simulator) Reclaim() (uint64, error) {
	prefix, _ := s.Reclaimable()
	if prefix <= s.baseIndex {
		return s.baseIndex, nil
	}
	if prefix > s.LastIndex() {
		return s.baseIndex, fmt.Errorf("reclaim prefix %d beyond last index %d", prefix, s.LastIndex())
	}
	s.logLen -= prefix - s.baseIndex
	s.baseIndex = prefix
	return prefix, nil
}
