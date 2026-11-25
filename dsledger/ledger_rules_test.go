package dsledger

import "testing"

// TestCrossHeightParentFork：prepare 父项指向已 commit 链上的旧块（非 tip），定罪。
func TestCrossHeightParentFork(t *testing.T) {
	keys := newTestKeys(t, 2)
	roster := testRoster(t, keys)
	l := openTestLedger(t, roster)
	k := keys[0]
	genesis := roster.GenesisDigest()
	b1, b2 := digestOf(0x51), digestOf(0x52)

	ingest(t, l, mustSign(t, k, roster.ChainID(), 1, 0, PhasePrepare, b1, genesis))
	ingest(t, l, mustSign(t, k, roster.ChainID(), 1, 0, PhaseCommit, b1, genesis))
	ingest(t, l, mustSign(t, k, roster.ChainID(), 2, 0, PhasePrepare, b2, b1))
	ingest(t, l, mustSign(t, k, roster.ChainID(), 2, 0, PhaseCommit, b2, b1))

	// 高度3 的 prepare 却把 b1（链上旧块）当父项：跨高度父摘要分叉。
	fork := mustSign(t, k, roster.ChainID(), 3, 0, PhasePrepare, digestOf(0x53), b1)
	res := ingest(t, l, fork)
	if res.Outcome != OutcomeFrozen || res.Evidence == nil {
		t.Fatalf("fork prepare = %+v, want frozen with evidence", res)
	}
	assertFrozen(t, l, k.id, ConflictParentFork)
}

// TestStagedPrepareReleasedAfterCommit：父项未知的 prepare 先暂存，
// 当父块成为最新 commit 后自动发布，证明乱序到达会被正确补齐而非误判。
func TestStagedPrepareReleasedAfterCommit(t *testing.T) {
	keys := newTestKeys(t, 2)
	roster := testRoster(t, keys)
	l := openTestLedger(t, roster)
	k := keys[0]
	genesis := roster.GenesisDigest()

	b1 := digestOf(0x61)
	pending := mustSign(t, k, roster.ChainID(), 2, 0, PhasePrepare, digestOf(0x63), b1)
	if res := ingest(t, l, pending); res.Outcome != OutcomeStaged {
		t.Fatalf("pending prepare = %s, want staged", res.Outcome)
	}

	ingest(t, l, mustSign(t, k, roster.ChainID(), 1, 0, PhasePrepare, b1, genesis))
	res := ingest(t, l, mustSign(t, k, roster.ChainID(), 1, 0, PhaseCommit, b1, genesis))
	if res.Outcome != OutcomeReleased || len(res.Released) != 1 {
		t.Fatalf("commit result = %+v, want released pending prepare", res)
	}
	if res.Released[0].Height != 2 {
		t.Fatalf("released share height = %d, want 2", res.Released[0].Height)
	}
	if frozen, _, _ := l.IsFrozen(k.id); frozen {
		t.Fatal("legitimately chained prepare must never be convicted")
	}
	snap, _ := l.Snapshot(k.id)
	if len(snap.Staged) != 0 || snap.TipHeight != 1 {
		t.Fatalf("snapshot after release = %+v", snap)
	}
}

// TestUnknownParentStaysStaged：父块始终未知时，账本不臆测冲突，不定罪。
func TestUnknownParentStaysStaged(t *testing.T) {
	keys := newTestKeys(t, 2)
	roster := testRoster(t, keys)
	l := openTestLedger(t, roster)
	k := keys[0]
	genesis := roster.GenesisDigest()
	unknownParent, b1 := digestOf(0x71), digestOf(0x72)

	pending := mustSign(t, k, roster.ChainID(), 2, 0, PhasePrepare, digestOf(0x73), unknownParent)
	if res := ingest(t, l, pending); res.Outcome != OutcomeStaged {
		t.Fatalf("unknown-parent prepare = %s, want staged", res.Outcome)
	}
	ingest(t, l, mustSign(t, k, roster.ChainID(), 1, 0, PhasePrepare, b1, genesis))
	ingest(t, l, mustSign(t, k, roster.ChainID(), 1, 0, PhaseCommit, b1, genesis))

	if frozen, _, _ := l.IsFrozen(k.id); frozen {
		t.Fatal("unknown parent must not convict before the parent block is observed")
	}
	snap, _ := l.Snapshot(k.id)
	if len(snap.Staged) != 1 {
		t.Fatalf("staged len = %d, want 1", len(snap.Staged))
	}
}

// TestSkippedHeightsAllowed：允许中间高度缺席，prepare 只需父项等于最近一次 commit。
func TestSkippedHeightsAllowed(t *testing.T) {
	keys := newTestKeys(t, 2)
	roster := testRoster(t, keys)
	l := openTestLedger(t, roster)
	k := keys[0]
	genesis := roster.GenesisDigest()
	b1, b5 := digestOf(0x81), digestOf(0x85)

	ingest(t, l, mustSign(t, k, roster.ChainID(), 1, 0, PhasePrepare, b1, genesis))
	ingest(t, l, mustSign(t, k, roster.ChainID(), 1, 0, PhaseCommit, b1, genesis))
	// 跳过 2/3/4，直接在高度5基于 b1 出 prepare。
	res := ingest(t, l, mustSign(t, k, roster.ChainID(), 5, 0, PhasePrepare, b5, b1))
	if res.Outcome != OutcomeAccepted {
		t.Fatalf("skipped-height prepare = %s (%s), want accepted", res.Outcome, res.Reason)
	}
}

// TestDuplicateIdempotent：完全重复消息幂等忽略，且重复不改变状态。
func TestDuplicateIdempotent(t *testing.T) {
	keys := newTestKeys(t, 2)
	roster := testRoster(t, keys)
	l := openTestLedger(t, roster)
	k := keys[0]
	block := digestOf(0x90)

	p := mustSign(t, k, roster.ChainID(), 1, 0, PhasePrepare, block, roster.GenesisDigest())
	ingest(t, l, p)
	before, _ := l.Snapshot(k.id)
	res := ingest(t, l, p)
	if res.Outcome != OutcomeDuplicate {
		t.Fatalf("duplicate = %s, want duplicate", res.Outcome)
	}
	after, _ := l.Snapshot(k.id)
	if len(after.Accepted) != len(before.Accepted) || len(after.Staged) != len(before.Staged) {
		t.Fatal("duplicate message must not mutate state")
	}
}

// TestDifferentPhaseNotConflict：同高度 prepare 与 commit 是不同阶段，不构成同阶段双签。
func TestDifferentPhaseNotConflict(t *testing.T) {
	keys := newTestKeys(t, 2)
	roster := testRoster(t, keys)
	l := openTestLedger(t, roster)
	k := keys[0]
	block, parent := digestOf(0xa0), roster.GenesisDigest()

	ingest(t, l, mustSign(t, k, roster.ChainID(), 1, 0, PhasePrepare, block, parent))
	res := ingest(t, l, mustSign(t, k, roster.ChainID(), 1, 0, PhaseCommit, block, parent))
	if res.Outcome != OutcomeAccepted {
		t.Fatalf("commit same block = %s, want accepted", res.Outcome)
	}
	if frozen, _, _ := l.IsFrozen(k.id); frozen {
		t.Fatal("prepare and commit are different phases; must not be a double sign")
	}
}
