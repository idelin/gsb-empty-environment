package desim

import (
	"fmt"
	"sort"
	"strings"
)

// Compensation 表示一个不可撤销的外部已确认效果所产生的补偿要求。
type Compensation struct {
	EventID uint64
	Name    string
	Reason  string
}

// ReplayEntry 是回滚重放日志的一条记录。
type ReplayEntry struct {
	EventID uint64
	Name    string
	Key     Key
	Action  string // "replayed" 或 "skipped-undone"
}

// Compensations 返回撤销过程中累积的全部补偿要求。
func (s *Sim) Compensations() []Compensation {
	return append([]Compensation(nil), s.compensations...)
}

// ReplayLog 返回最近一次回滚的重放日志。
func (s *Sim) ReplayLog() []ReplayEntry {
	return append([]ReplayEntry(nil), s.replayLog...)
}

// Undo 撤销一个事件：
//   - 待执行事件：直接取消；
//   - 已执行事件：级联撤销其全部因果后继，然后从最近的有效检查点
//     恢复状态并重放日志中未被撤销的部分；
//   - 因果闭包中已执行的外部事件不可撤销，只登记补偿要求并保留。
func (s *Sim) Undo(id uint64) error {
	target, ok := s.events[id]
	if !ok {
		return ErrNotFound
	}
	switch target.Status {
	case StatusPending:
		target.Status = StatusCancelled
		return nil
	case StatusExecuted:
	default:
		return fmt.Errorf("desim: cannot undo %s in status %s", target, target.Status)
	}

	// 因果闭包：目标事件及其全部后继。
	var closure []*Event
	var walk func(e *Event)
	walk = func(e *Event) {
		closure = append(closure, e)
		for _, c := range e.children {
			walk(c)
		}
	}
	walk(target)

	index := make(map[*Event]int, len(s.journal))
	for i, ev := range s.journal {
		index[ev] = i
	}

	earliest := len(s.journal)
	for _, ev := range closure {
		switch {
		case ev.External && ev.Status == StatusExecuted:
			// 外部已确认效果不可撤销，只产生补偿要求。
			s.compensations = append(s.compensations, Compensation{
				EventID: ev.ID,
				Name:    ev.Name,
				Reason:  "externally confirmed effect cannot be undone; compensation required",
			})
		case ev.Status == StatusExecuted:
			ev.Status = StatusUndone
			if i, ok := index[ev]; ok && i < earliest {
				earliest = i
			}
		case ev.Status == StatusPending:
			ev.Status = StatusCancelled
		}
	}
	if earliest == len(s.journal) {
		return nil // 闭包中没有需要回滚的内部已执行事件
	}
	return s.rollback(earliest)
}

// rollback 恢复到不晚于 fromIndex 的最近检查点，并重放其后的日志。
func (s *Sim) rollback(fromIndex int) error {
	ci := 0
	for i, cp := range s.checkpoints {
		if cp.journalLen <= fromIndex {
			ci = i
		} else {
			break
		}
	}
	cp := s.checkpoints[ci]
	// 检查点之后的检查点状态可能依赖被撤销的事件，全部作废。
	s.checkpoints = s.checkpoints[:ci+1]

	tail := append([]*Event(nil), s.journal[cp.journalLen:]...)
	s.state = s.clone(cp.state)
	s.journal = append([]*Event(nil), s.journal[:cp.journalLen]...)
	s.last = cp.key

	s.replayLog = s.replayLog[:0]
	s.replaying = true
	defer func() { s.replaying = false }()
	for _, ev := range tail {
		if ev.Status == StatusUndone {
			s.replayLog = append(s.replayLog, ReplayEntry{ev.ID, ev.Name, ev.Key, "skipped-undone"})
			continue
		}
		s.replayLog = append(s.replayLog, ReplayEntry{ev.ID, ev.Name, ev.Key, "replayed"})
		s.execute(ev, true)
	}
	return nil
}

// CausalChain 以树形文本输出全部事件的因果链及其状态。
func (s *Sim) CausalChain() string {
	var roots []*Event
	for _, ev := range s.events {
		if ev.parent == nil {
			roots = append(roots, ev)
		}
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].ID < roots[j].ID })

	var b strings.Builder
	var rec func(e *Event, depth int)
	rec = func(e *Event, depth int) {
		mark := ""
		if e.External {
			mark = " [external]"
		}
		fmt.Fprintf(&b, "%s%s %v [%s]%s\n",
			strings.Repeat("  ", depth), e.Name, e.Key, e.Status, mark)
		for _, c := range e.children {
			rec(c, depth+1)
		}
	}
	for _, r := range roots {
		rec(r, 0)
	}
	return b.String()
}

// Verify 用全量从头模拟核对当前状态：从初始状态出发，按日志顺序
// 重放所有未被撤销的事件（抑制再调度），比较最终状态是否一致。
// 不一致时返回的错误中附带因果链，便于定位分歧。
func (s *Sim) Verify() error {
	scratch := &Sim{state: s.clone(s.checkpoints[0].state), replaying: true}
	for _, ev := range s.journal {
		if ev.Status == StatusUndone {
			continue
		}
		ctx := &Context{sim: scratch, Event: ev, Replay: true}
		if err := ev.handler(ctx); err != nil {
			return fmt.Errorf("desim: verify replay of %s failed: %v", ev, err)
		}
	}
	if !s.equal(scratch.state, s.state) {
		return fmt.Errorf("desim: verification mismatch\nfrom-scratch: %v\nrolled-back:  %v\ncausal chain:\n%s",
			scratch.state, s.state, s.CausalChain())
	}
	return nil
}
