// Package reclaim 实现复制日志回收模拟器。
//
// 核心语义：
//   - 只有所有活跃副本（含待确认剔除的副本）都越过某水位后，
//     水位之前的日志前缀才允许回收。
//   - 剔除成员需两步：RemoveReplica 标记为待确认（仍阻挡回收），
//     ConfirmRemoval 之后才不再参与水位计算。
//   - 副本重加入时分配新代次，从当前水位开始恢复；
//     携带旧代次的心跳一律拒绝，不能提升水位。
//   - 乱序心跳不会使副本位置回退，水位单调不降。
package reclaim

import (
	"errors"
	"fmt"
	"sort"
)

// ReplicaID 副本标识。
type ReplicaID string

var (
	ErrUnknownReplica   = errors.New("reclaim: unknown replica")
	ErrStaleGeneration  = errors.New("reclaim: stale generation heartbeat")
	ErrFutureGeneration = errors.New("reclaim: heartbeat from unregistered future generation")
	ErrAlreadyActive    = errors.New("reclaim: replica already active")
	ErrNotPending       = errors.New("reclaim: replica is not pending removal")
	ErrPositionBeyond   = errors.New("reclaim: heartbeat position beyond log end")
)

// Replica 单个副本的复制状态。
type Replica struct {
	ID         ReplicaID
	Generation uint64 // 当前代次，重加入时递增
	Position   uint64 // 已复制到的最高连续日志位置（LSN）
}

// Blocker 阻挡回收的节点信息。
type Blocker struct {
	Replica
	PendingRemoval bool // 是否为待确认剔除的成员
}

func (b Blocker) String() string {
	state := "active"
	if b.PendingRemoval {
		state = "pending-removal"
	}
	return fmt.Sprintf("%s(gen=%d pos=%d %s)", b.ID, b.Generation, b.Position, state)
}

// Simulator 复制日志回收模拟器。
type Simulator struct {
	active    map[ReplicaID]*Replica // 活跃成员
	pending   map[ReplicaID]*Replica // 已剔除待确认，仍参与水位计算
	logEnd    uint64                 // 日志末尾（已追加的最大位置，开区间）
	watermark uint64                 // 回收水位，单调不降
	nextGen   uint64                 // 全局代次分配器，保证代次严格递增
}

// New 创建模拟器。
func New() *Simulator {
	return &Simulator{
		active:  make(map[ReplicaID]*Replica),
		pending: make(map[ReplicaID]*Replica),
		nextGen: 1,
	}
}

// Append 向日志追加 n 条记录，推进日志末尾。
func (s *Simulator) Append(n uint64) {
	s.logEnd += n
}

// AddReplica 加入新副本，从当前水位开始恢复。
func (s *Simulator) AddReplica(id ReplicaID) error {
	if _, ok := s.active[id]; ok {
		return ErrAlreadyActive
	}
	delete(s.pending, id)
	s.active[id] = &Replica{ID: id, Generation: s.allocGen(), Position: s.watermark}
	return nil
}

// RemoveReplica 剔除成员：转入待确认状态，仍阻挡回收，
// 必须 ConfirmRemoval 后才真正移出水位计算。
func (s *Simulator) RemoveReplica(id ReplicaID) error {
	r, ok := s.active[id]
	if !ok {
		return ErrUnknownReplica
	}
	delete(s.active, id)
	s.pending[id] = r
	return nil
}

// ConfirmRemoval 确认剔除，副本彻底退出，不再阻挡回收。
func (s *Simulator) ConfirmRemoval(id ReplicaID) error {
	if _, ok := s.pending[id]; !ok {
		return ErrNotPending
	}
	delete(s.pending, id)
	s.advance()
	return nil
}

// Rejoin 副本重加入：分配新代次，从当前水位开始恢复。
// 此后旧代次的心跳一律被拒绝，无法提升水位。
// 无论副本当前是活跃、待确认剔除还是已彻底移除，都可重加入。
func (s *Simulator) Rejoin(id ReplicaID) error {
	delete(s.pending, id)
	s.active[id] = &Replica{ID: id, Generation: s.allocGen(), Position: s.watermark}
	return nil
}

// Heartbeat 上报副本复制进度。
// 代次必须匹配当前代次；乱序（位置回退）的心跳被忽略但不报错。
func (s *Simulator) Heartbeat(id ReplicaID, generation, position uint64) error {
	r, ok := s.active[id]
	if !ok {
		return ErrUnknownReplica
	}
	if generation < r.Generation {
		return ErrStaleGeneration
	}
	if generation > r.Generation {
		return ErrFutureGeneration
	}
	if position > s.logEnd {
		return ErrPositionBeyond
	}
	if position > r.Position {
		r.Position = position
		s.advance()
	}
	return nil
}

// Watermark 当前回收水位：所有计入成员都越过的最大位置，单调不降。
func (s *Simulator) Watermark() uint64 {
	return s.watermark
}

// ReclaimablePrefix 返回可回收的日志前缀 [start, end)。
// start 恒为 0（日志从头回收），end 为当前水位。
func (s *Simulator) ReclaimablePrefix() (start, end uint64) {
	return 0, s.watermark
}

// BlockingNodes 返回阻挡水位推进的节点：
// 即位置等于当前水位的全部成员（活跃或待确认剔除），按 ID 排序。
func (s *Simulator) BlockingNodes() []Blocker {
	var out []Blocker
	for _, r := range s.active {
		if r.Position == s.watermark {
			out = append(out, Blocker{Replica: *r})
		}
	}
	for _, r := range s.pending {
		if r.Position == s.watermark {
			out = append(out, Blocker{Replica: *r, PendingRemoval: true})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// PendingRemoval 返回待确认剔除的成员列表，按 ID 排序。
func (s *Simulator) PendingRemoval() []ReplicaID {
	out := make([]ReplicaID, 0, len(s.pending))
	for id := range s.pending {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Replica 查询活跃副本状态。
func (s *Simulator) Replica(id ReplicaID) (Replica, bool) {
	if r, ok := s.active[id]; ok {
		return *r, true
	}
	return Replica{}, false
}

// advance 在所有计入成员（活跃 + 待确认剔除）都越过后推进水位。
func (s *Simulator) advance() {
	if len(s.active) == 0 && len(s.pending) == 0 {
		return
	}
	wm := s.logEnd
	for _, r := range s.active {
		if r.Position < wm {
			wm = r.Position
		}
	}
	for _, r := range s.pending {
		if r.Position < wm {
			wm = r.Position
		}
	}
	if wm > s.watermark {
		s.watermark = wm
	}
}

func (s *Simulator) allocGen() uint64 {
	g := s.nextGen
	s.nextGen++
	return g
}
