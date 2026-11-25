package dsledger

import (
	"fmt"
	"sort"
)

// MemberSnapshot 是某一成员当前账本状态的只读视图。
type MemberSnapshot struct {
	ID               MemberID  `json:"id"`
	Frozen           bool      `json:"frozen"`
	Evidence         *Evidence `json:"evidence,omitempty"`
	TipHeight        uint64    `json:"tip_height"`
	CommittedHeights []uint64  `json:"committed_heights"`
	Accepted         []Share   `json:"accepted"`
	Staged           []Share   `json:"staged"`
}

// Snapshot 返回成员的状态副本（拷贝，调用方可自由持有）。
func (l *Ledger) Snapshot(id MemberID) (MemberSnapshot, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	ms, ok := l.member[id]
	if !ok {
		return MemberSnapshot{}, fmt.Errorf("dsledger: unknown member %q", id)
	}

	keys := make([]slotKey, 0, len(ms.accepted))
	for key := range ms.accepted {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Height != keys[j].Height {
			return keys[i].Height < keys[j].Height
		}
		return keys[i].Phase < keys[j].Phase
	})
	accepted := make([]Share, 0, len(keys))
	for _, key := range keys {
		accepted = append(accepted, ms.accepted[key].First)
	}

	heights := make([]uint64, 0, len(ms.committedHeights))
	for h := range ms.committedHeights {
		heights = append(heights, h)
	}
	sort.Slice(heights, func(i, j int) bool { return heights[i] < heights[j] })

	staged := make([]Share, len(ms.staged))
	copy(staged, ms.staged)

	snap := MemberSnapshot{
		ID:               id,
		Frozen:           ms.frozen,
		TipHeight:        ms.tipHeight,
		CommittedHeights: heights,
		Accepted:         accepted,
		Staged:           staged,
	}
	if ms.evidence != nil {
		evCopy := *ms.evidence
		snap.Evidence = &evCopy
	}
	return snap, nil
}

// IsFrozen 报告成员是否已被冻结，冻结时一并返回双签证据副本。
func (l *Ledger) IsFrozen(id MemberID) (bool, *Evidence, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	ms, ok := l.member[id]
	if !ok {
		return false, nil, fmt.Errorf("dsledger: unknown member %q", id)
	}
	if !ms.frozen {
		return false, nil, nil
	}
	evCopy := *ms.evidence
	return true, &evCopy, nil
}

// FrozenEvidence 返回成员的冻结证据（无论当前是否已解冻，证据均保留）。
func (l *Ledger) FrozenEvidence(id MemberID) (*Evidence, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	ms, ok := l.member[id]
	if !ok {
		return nil, false, fmt.Errorf("dsledger: unknown member %q", id)
	}
	if ms.evidence == nil {
		return nil, false, nil
	}
	evCopy := *ms.evidence
	return &evCopy, true, nil
}

// PurgeStaged 清空已冻结成员的暂存矛盾，返回被清理的份额数。
// 仅允许对冻结成员执行，防止用清理掩盖尚未定罪的问题。
func (l *Ledger) PurgeStaged(id MemberID) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	ms, ok := l.member[id]
	if !ok {
		return 0, fmt.Errorf("dsledger: unknown member %q", id)
	}
	if !ms.frozen {
		return 0, fmt.Errorf("dsledger: PurgeStaged is only allowed for frozen member %q", id)
	}
	n := len(ms.staged)
	if n == 0 {
		return 0, nil
	}
	if err := l.persist(id, walEvent{Type: evPurged}); err != nil {
		return 0, err
	}
	ms.staged = nil
	return n, nil
}

// Unfreeze 在暂存矛盾已全部清理后解除成员冻结。
// 已签索引与历史证据保留；解冻后成员可从当前 tip 继续参与。
func (l *Ledger) Unfreeze(id MemberID) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	ms, ok := l.member[id]
	if !ok {
		return fmt.Errorf("dsledger: unknown member %q", id)
	}
	if !ms.frozen {
		return fmt.Errorf("dsledger: member %q is not frozen", id)
	}
	if len(ms.staged) != 0 {
		return fmt.Errorf("dsledger: cannot unfreeze %q: %d staged contradictions remain", id, len(ms.staged))
	}
	if err := l.persist(id, walEvent{Type: evUnfrozen}); err != nil {
		return err
	}
	ms.frozen = false
	return nil
}
