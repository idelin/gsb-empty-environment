package dsledger

import (
	"fmt"
	"sort"
	"strconv"
)

// decision 是规则引擎对一条新份额的判定。
type decision int

const (
	decAccept decision = iota
	decStage
	decStageReplace
	decDuplicate
	decStale
	decConflict
)

func reasonIndex(reason string) int {
	idx, err := strconv.Atoi(reason)
	if err != nil {
		panic(fmt.Sprintf("dsledger: internal error: bad stage-replace index %q", reason))
	}
	return idx
}

// evaluate 是无副作用的纯规则判定。decStageReplace 时 reason 为被替换暂存项下标。
func (l *Ledger) evaluate(ms *memberState, s Share) (decision, string) {
	key := s.slot()
	if rec, ok := ms.accepted[key]; ok {
		switch {
		case sharesEqual(rec.First, s):
			return decDuplicate, "identical share already accepted"
		case rec.First.Block == s.Block && s.Round <= rec.MaxRound:
			return decStale, "round regression in an accepted slot"
		case rec.First.Block == s.Block:
			// 同槽位同区块的更高轮次：仅推进见证轮次，份额本体不覆盖。
			return decAccept, "advance round in accepted slot"
		default:
			return decConflict, string(conflictKindFor(s, rec.First))
		}
	}

	for i, st := range ms.staged {
		if st.slot() != key {
			continue
		}
		switch {
		case sharesEqual(st, s):
			return decDuplicate, "identical share already staged"
		case st.Block == s.Block && s.Round <= st.Round:
			return decStale, "round regression against staged share"
		case st.Block == s.Block:
			return decStageReplace, strconv.Itoa(i)
		default:
			return decConflict, string(conflictKindFor(s, st))
		}
	}

	if s.Phase == PhasePrepare {
		if parentOK, kind := l.checkPrepareParent(ms, s); !parentOK {
			if kind == "" {
				return decStage, "parent block not yet committed"
			}
			return decConflict, string(kind)
		}
		return decAccept, ""
	}

	// commit：必须已有同高度同区块、轮次不高于 commit 的 prepare。
	prep, ok := ms.accepted[slotKey{Height: s.Height, Phase: PhasePrepare}]
	if !ok {
		return decStage, "prepare prerequisite missing"
	}
	if prep.First.Block != s.Block {
		return decStage, "prepare for a different block; prerequisite cannot be satisfied"
	}
	if prep.First.Round > s.Round {
		return decConflict, string(ConflictRound)
	}
	return decAccept, ""
}

// checkPrepareParent 判定 prepare 的父摘要是否合法。
// (true, "") 合法；(false, "") 父块未知应暂存；(false, kind) 构成分叉冲突。
func (l *Ledger) checkPrepareParent(ms *memberState, s Share) (bool, ConflictKind) {
	required := l.roster.GenesisDigest()
	if ms.tipHeight > 0 {
		required = ms.committedHeights[ms.tipHeight]
	}
	if s.Parent == required {
		return true, ""
	}
	if ms.tipHeight == 0 {
		// 尚无 commit：非创世父项的父块必然未知，等待提交，绝不提前定罪。
		return false, ""
	}
	if _, known := ms.blockHeights[s.Parent]; known {
		// 父块是本成员已 commit 链上的旧块而非最新块：跨高度父摘要分叉。
		return false, ConflictParentFork
	}
	// 父块尚未出现：暂存等待，将来不匹配再定罪。
	return false, ""
}

func conflictKindFor(incoming, existing Share) ConflictKind {
	if incoming.Height == existing.Height && incoming.Phase == existing.Phase {
		return ConflictSameHeight
	}
	return ConflictParentFork
}

// acceptAndPump 落盘并应用一条可接受份额，随后循环发布所有已满足前置的暂存。
// 返回值 released 仅包含被泵循环从暂存中释放的“其他”份额；本次份额自身
// 是否来自暂存由调用方在调用前判定。
func (l *Ledger) acceptAndPump(ms *memberState, s Share) ([]Share, *Evidence, error) {
	var released []Share

	ev, err := l.acceptOne(ms, s)
	if err != nil {
		return nil, nil, err
	}
	if ev != nil {
		return released, ev, nil
	}

	for {
		progressed := false
		if len(ms.staged) == 0 {
			break
		}
		st := ms.staged[0]
		// 先把待评估项移出暂存，避免其命中自身的去重分支。
		ms.staged = ms.staged[1:]
		decision, reason := l.evaluate(ms, st)
		switch decision {
		case decAccept:
			if err := l.persist(st.Signer, walEvent{Type: evUnstaged, Share: &st}); err != nil {
				return nil, nil, err
			}
			ev, err := l.acceptOne(ms, st)
			if err != nil {
				return nil, nil, err
			}
			if ev != nil {
				return released, ev, nil
			}
			released = append(released, st)
			progressed = true
		case decStage:
			// 前置仍未满足：放回暂存末尾，继续看其他项。
			ms.staged = append(ms.staged, st)
		case decStale, decDuplicate:
			// 被更高轮次取代或与已接受项等价：记录离开并丢弃。
			if err := l.persist(st.Signer, walEvent{Type: evUnstaged, Share: &st}); err != nil {
				return nil, nil, err
			}
			progressed = true
		case decStageReplace:
			idx := reasonIndex(reason)
			old := ms.staged[idx]
			if err := l.persist(st.Signer, walEvent{Type: evUnstaged, Share: &old}); err != nil {
				return nil, nil, err
			}
			ms.staged = append(ms.staged[:idx], ms.staged[idx+1:]...)
			if err := l.persist(st.Signer, walEvent{Type: evStaged, Share: &st}); err != nil {
				return nil, nil, err
			}
			ms.staged = append(ms.staged, st)
			progressed = true
		case decConflict:
			ev := l.buildEvidence(ms, st, reason)
			if err := l.freeze(ms, st.Signer, ev); err != nil {
				return nil, nil, err
			}
			return released, ev, nil
		}
		if !progressed {
			break
		}
	}
	return released, nil, nil
}

// acceptOne 落盘并应用一条已判定可接受的份额，然后检查 tip 推进引发的分叉。
func (l *Ledger) acceptOne(ms *memberState, s Share) (*Evidence, error) {
	key := s.slot()
	if rec, ok := ms.accepted[key]; ok && rec.First.Block == s.Block && s.Round > rec.MaxRound {
		if err := l.persist(s.Signer, walEvent{Type: evRoundAdvanced, Share: &s, MaxRound: s.Round}); err != nil {
			return nil, err
		}
		rec.MaxRound = s.Round
		return nil, nil
	}
	if err := l.persist(s.Signer, walEvent{Type: evAccepted, Share: &s}); err != nil {
		return nil, err
	}
	l.applyAccepted(ms, s)
	ev, err := l.checkAcceptedPrepares(ms)
	return ev, err
}

// applyAccepted 在内存中应用一条已落盘的已接受份额。
func (l *Ledger) applyAccepted(ms *memberState, s Share) {
	key := s.slot()
	ms.accepted[key] = &acceptedRecord{First: s, MaxRound: s.Round}
	if s.Phase == PhaseCommit {
		if s.Height > ms.tipHeight {
			ms.tipHeight = s.Height
		}
		ms.committedHeights[s.Height] = s.Block
		ms.blockHeights[s.Block] = s.Height
	}
}

// checkAcceptedPrepares 在 tip 推进后扫描仍高于 tip 的已接受 prepare，
// 若其父摘要不再是最新已 commit 块（且父块是链上旧块），立即定罪。
func (l *Ledger) checkAcceptedPrepares(ms *memberState) (*Evidence, error) {
	if ms.tipHeight == 0 {
		return nil, nil
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
	for _, key := range keys {
		if key.Phase != PhasePrepare || key.Height <= ms.tipHeight {
			continue
		}
		rec := ms.accepted[key]
		ok, kind := l.checkPrepareParent(ms, rec.First)
		if ok || kind == "" {
			continue
		}
		commitBlock := ms.committedHeights[ms.tipHeight]
		commitShare := l.findCommitForBlock(ms, commitBlock)
		ev := &Evidence{
			Kind:   kind,
			Signer: rec.First.Signer,
			First:  rec.First,
			Second: commitShare,
		}
		if err := l.freeze(ms, rec.First.Signer, ev); err != nil {
			return nil, err
		}
		return ev, nil
	}
	return nil, nil
}

func (l *Ledger) findCommitForBlock(ms *memberState, block Digest) Share {
	for _, rec := range ms.accepted {
		if rec.First.Phase == PhaseCommit && rec.First.Block == block {
			return rec.First
		}
	}
	panic("dsledger: internal error: tip commit missing from accepted index")
}

// buildEvidence 为冲突构造两份有效签名的最小证据。
func (l *Ledger) buildEvidence(ms *memberState, incoming Share, reason string) *Evidence {
	kind := ConflictKind(reason)
	key := incoming.slot()
	var other Share
	if kind == ConflictRound {
		// 轮次冲突：incoming 是低轮次 commit，第二份是同高度同区块的 prepare。
		// 证据约定 First=prepare、Second=commit，便于独立验证。
		if rec, ok := ms.accepted[slotKey{Height: incoming.Height, Phase: PhasePrepare}]; ok {
			return &Evidence{
				Kind:   kind,
				Signer: incoming.Signer,
				First:  rec.First,
				Second: incoming,
			}
		}
	} else if rec, ok := ms.accepted[key]; ok {
		other = rec.First
	} else {
		for _, st := range ms.staged {
			if st.slot() == key && st.Block != incoming.Block {
				other = st
				break
			}
		}
	}
	if kind == ConflictParentFork && isZeroShare(other) {
		// 跨高度父项分叉：用 tip 上的 commit 作为第二份签名。
		commitBlock := ms.committedHeights[ms.tipHeight]
		other = l.findCommitForBlock(ms, commitBlock)
	}
	return &Evidence{
		Kind:   kind,
		Signer: incoming.Signer,
		First:  incoming,
		Second: other,
	}
}

func (l *Ledger) freeze(ms *memberState, id MemberID, ev *Evidence) error {
	if err := VerifyEvidence(l.roster, *ev); err != nil {
		return fmt.Errorf("dsledger: refusing to persist invalid evidence: %w", err)
	}
	if err := l.persist(id, walEvent{Type: evFrozen, Evidence: ev}); err != nil {
		return err
	}
	ms.frozen = true
	ms.evidence = ev
	return nil
}

func (l *Ledger) persist(id MemberID, ev walEvent) error {
	return l.store.append(id, ev)
}

// applyReplay 在不重新落盘的前提下把 WAL 事件应用到内存状态。
func (l *Ledger) applyReplay(ms *memberState, ev walEvent) error {
	switch ev.Type {
	case evAccepted:
		if ev.Share == nil {
			return fmt.Errorf("dsledger: corrupt wal: accepted without share")
		}
		l.applyAccepted(ms, *ev.Share)
	case evRoundAdvanced:
		if ev.Share == nil {
			return fmt.Errorf("dsledger: corrupt wal: round_advanced without share")
		}
		rec, ok := ms.accepted[ev.Share.slot()]
		if !ok {
			return fmt.Errorf("dsledger: corrupt wal: round_advanced without accepted slot")
		}
		rec.MaxRound = ev.MaxRound
	case evStaged:
		if ev.Share == nil {
			return fmt.Errorf("dsledger: corrupt wal: staged without share")
		}
		ms.staged = append(ms.staged, *ev.Share)
	case evUnstaged:
		if ev.Share == nil {
			return fmt.Errorf("dsledger: corrupt wal: unstaged without share")
		}
		for i, st := range ms.staged {
			if sharesEqual(st, *ev.Share) {
				ms.staged = append(ms.staged[:i], ms.staged[i+1:]...)
				break
			}
		}
	case evPurged:
		ms.staged = nil
	case evFrozen:
		if ev.Evidence == nil {
			return fmt.Errorf("dsledger: corrupt wal: frozen without evidence")
		}
		ms.frozen = true
		ms.evidence = ev.Evidence
	case evUnfrozen:
		ms.frozen = false
	default:
		return fmt.Errorf("dsledger: corrupt wal: unknown event type %q", ev.Type)
	}
	return nil
}

func sharesEqual(a, b Share) bool {
	return a.Signer == b.Signer && a.Height == b.Height && a.Round == b.Round &&
		a.Phase == b.Phase && a.Block == b.Block && a.Parent == b.Parent
}

func isZeroShare(s Share) bool {
	return s.Signer == "" && s.Height == 0 && s.Round == 0 && s.Phase == 0 &&
		s.Block == ZeroDigest && s.Parent == ZeroDigest && s.Sig == nil
}

func isStaged(ms *memberState, s Share) bool {
	for _, st := range ms.staged {
		if sharesEqual(st, s) {
			return true
		}
	}
	return false
}
