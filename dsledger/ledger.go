package dsledger

import (
	"fmt"
	"sync"
)

// Outcome 描述单条份额进入账本后的处理结果。
type Outcome string

const (
	// OutcomeAccepted 份额通过规则并进入已签索引（立即发布）。
	OutcomeAccepted Outcome = "accepted"
	// OutcomeReleased 份额此前暂存，本次因前置补齐而发布。
	OutcomeReleased Outcome = "released"
	// OutcomeStaged 份额因前置缺失进入暂存。
	OutcomeStaged Outcome = "staged"
	// OutcomeDuplicate 完全重复或等价重复，被幂等忽略。
	OutcomeDuplicate Outcome = "duplicate"
	// OutcomeStale 轮次倒退（同槽位同区块的更旧轮次），被拒绝且不覆盖。
	OutcomeStale Outcome = "stale"
	// OutcomeFrozen 成员已冻结，份额被拒绝。
	OutcomeFrozen Outcome = "frozen"
)

// Result 是 Ingest/IngestBatch 中单条份额的处理结果。
type Result struct {
	Share    Share     `json:"share"`
	Outcome  Outcome   `json:"outcome"`
	Reason   string    `json:"reason,omitempty"`
	Released []Share   `json:"released,omitempty"`
	Evidence *Evidence `json:"evidence,omitempty"`
}

// acceptedRecord 是已发布份额及其见证过的最大轮次。
// 份额本体（First）永不被后到消息覆盖。
type acceptedRecord struct {
	First    Share
	MaxRound uint64
}

// memberState 是单个成员的全部可重建状态。
type memberState struct {
	accepted map[slotKey]*acceptedRecord
	staged   []Share
	// committedHeights 记录每个已 commit 高度的区块摘要。
	committedHeights map[uint64]Digest
	// blockHeights 记录每个已 commit 区块摘要对应的高度，便于父项查找。
	blockHeights map[Digest]uint64
	// tipHeight 是最近一次（高度最大的）已 commit 高度；无 commit 时为 0。
	tipHeight uint64
	frozen    bool
	evidence  *Evidence
}

func newMemberState() *memberState {
	return &memberState{
		accepted:         make(map[slotKey]*acceptedRecord),
		committedHeights: make(map[uint64]Digest),
		blockHeights:     make(map[Digest]uint64),
	}
}

// Ledger 是签名份额防双签账本。并发安全。
type Ledger struct {
	mu     sync.Mutex
	dir    string
	roster *Roster
	store  *store
	member map[MemberID]*memberState
	closed bool
}

// Open 在 dir 打开或创建账本。
//
// 目录为空时使用 roster 初始化固定成员清单；目录已有数据时，roster 必须与
// 持久化清单完全一致（链 ID、创世摘要、成员 ID 与公钥）。随后重放每个成员
// 的 WAL，重建已签索引、暂存与冻结证据。
func Open(dir string, roster *Roster) (*Ledger, error) {
	if roster == nil {
		return nil, fmt.Errorf("dsledger: nil roster")
	}
	st, err := openStore(dir)
	if err != nil {
		return nil, err
	}
	l := &Ledger{
		dir:    dir,
		roster: roster,
		store:  st,
		member: make(map[MemberID]*memberState, len(roster.order)),
	}

	persisted, err := loadRoster(dir)
	if err != nil {
		// 首次初始化：原子写入清单。
		if err := st.saveRoster(roster); err != nil {
			return nil, err
		}
	} else {
		if err := ensureSameRoster(roster, persisted); err != nil {
			return nil, err
		}
	}

	for _, id := range roster.order {
		ms := newMemberState()
		if err := st.replay(id, func(ev walEvent) error {
			return l.applyReplay(ms, ev)
		}); err != nil {
			return nil, fmt.Errorf("dsledger: replay member %s: %w", id, err)
		}
		l.member[id] = ms
	}
	return l, nil
}

func ensureSameRoster(want, got *Roster) error {
	if want.ChainID() != got.ChainID() {
		return fmt.Errorf("dsledger: chain id mismatch with persisted roster")
	}
	if want.GenesisDigest() != got.GenesisDigest() {
		return fmt.Errorf("dsledger: genesis digest mismatch with persisted roster")
	}
	wantIDs := want.IDs()
	gotIDs := got.IDs()
	if len(wantIDs) != len(gotIDs) {
		return fmt.Errorf("dsledger: roster size mismatch")
	}
	for i, id := range wantIDs {
		if gotIDs[i] != id {
			return fmt.Errorf("dsledger: roster order/id mismatch at %d", i)
		}
		wm, _ := want.Member(id)
		gm, _ := got.Member(id)
		if !gm.PublicKey.Equal(wm.PublicKey) {
			return fmt.Errorf("dsledger: public key mismatch for member %q", id)
		}
	}
	return nil
}

// Close 刷新并关闭底层 WAL。
func (l *Ledger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	return l.store.close()
}

// Roster 返回账本绑定的固定成员清单。
func (l *Ledger) Roster() *Roster { return l.roster }

// Ingest 处理一条份额消息，返回处理结果。乱序、重复、冻结等情况均在此收敛。
// 返回 error 仅用于持久化 I/O 失败等宿主应重试的系统错误；规则拒绝通过 Result 表达。
func (l *Ledger) Ingest(s Share) (Result, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return Result{}, fmt.Errorf("dsledger: ledger is closed")
	}
	if err := VerifyShare(l.roster, s); err != nil {
		return Result{Share: s, Outcome: OutcomeStale, Reason: err.Error()}, nil
	}
	ms, ok := l.member[s.Signer]
	if !ok {
		return Result{Share: s, Outcome: OutcomeStale, Reason: "signer not in roster"}, nil
	}
	if ms.frozen {
		return Result{
			Share:    s,
			Outcome:  OutcomeFrozen,
			Reason:   "member is frozen due to double signing",
			Evidence: ms.evidence,
		}, nil
	}

	decision, reason := l.evaluate(ms, s)
	switch decision {
	case decConflict:
		ev := l.buildEvidence(ms, s, reason)
		if err := l.freeze(ms, s.Signer, ev); err != nil {
			return Result{}, err
		}
		return Result{Share: s, Outcome: OutcomeFrozen, Reason: string(ev.Kind), Evidence: ev}, nil
	case decDuplicate:
		return Result{Share: s, Outcome: OutcomeDuplicate, Reason: reason}, nil
	case decStale:
		return Result{Share: s, Outcome: OutcomeStale, Reason: reason}, nil
	case decStageReplace:
		idx := reasonIndex(reason)
		old := ms.staged[idx]
		if err := l.persist(s.Signer, walEvent{Type: evUnstaged, Share: &old}); err != nil {
			return Result{}, err
		}
		ms.staged = append(ms.staged[:idx], ms.staged[idx+1:]...)
		if err := l.persist(s.Signer, walEvent{Type: evStaged, Share: &s}); err != nil {
			return Result{}, err
		}
		ms.staged = append(ms.staged, s)
		return Result{Share: s, Outcome: OutcomeStaged, Reason: "replaced lower-round staged share"}, nil
	case decStage:
		if err := l.persist(s.Signer, walEvent{Type: evStaged, Share: &s}); err != nil {
			return Result{}, err
		}
		ms.staged = append(ms.staged, s)
		return Result{Share: s, Outcome: OutcomeStaged, Reason: reason}, nil
	case decAccept:
		selfWasStaged := isStaged(ms, s)
		released, ev, err := l.acceptAndPump(ms, s)
		if err != nil {
			return Result{}, err
		}
		if ev != nil {
			return Result{Share: s, Outcome: OutcomeFrozen, Reason: string(ev.Kind), Evidence: ev, Released: released}, nil
		}
		outcome := OutcomeAccepted
		// 本次份额自身此前处于暂存，或它带动了其他暂存份额发布，
		// 都属于“前置补齐后发布”的语义。
		if selfWasStaged || len(released) > 0 {
			outcome = OutcomeReleased
		}
		return Result{Share: s, Outcome: outcome, Released: released}, nil
	default:
		return Result{}, fmt.Errorf("dsledger: internal error: unknown decision")
	}
}

// IngestBatch 顺序处理一批份额。任一份额触发冻结即停止并返回已处理结果；
// 每条份额在状态变更前都先落盘，因此批次中途崩溃不会漏报已确认事件。
func (l *Ledger) IngestBatch(shares []Share) ([]Result, error) {
	results := make([]Result, 0, len(shares))
	for _, s := range shares {
		res, err := l.Ingest(s)
		if err != nil {
			return results, err
		}
		results = append(results, res)
		if res.Outcome == OutcomeFrozen {
			break
		}
	}
	return results, nil
}
