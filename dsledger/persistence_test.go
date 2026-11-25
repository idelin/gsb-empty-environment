package dsledger

import (
	"os"
	"testing"
)

func memberRoster(keys []testKey, chainID string, genesis Digest) *Roster {
	members := make([]Member, len(keys))
	for i, k := range keys {
		members[i] = Member{ID: k.id, PublicKey: k.pub}
	}
	roster, err := NewRoster(chainID, genesis, members)
	if err != nil {
		panic(err)
	}
	return roster
}

// TestRestartRebuildsState：重启后从每成员 WAL 重建已签索引、暂存和冻结证据。
func TestRestartRebuildsState(t *testing.T) {
	keys := newTestKeys(t, 2)
	roster := testRoster(t, keys)
	dir := t.TempDir()
	k := keys[0]

	l, err := Open(dir, roster)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	blockA, blockB, parent := digestOf(0xb0), digestOf(0xb1), roster.GenesisDigest()
	ingest(t, l, mustSign(t, k, roster.ChainID(), 1, 0, PhasePrepare, blockA, parent))
	stagedCommit := mustSign(t, k, roster.ChainID(), 3, 0, PhaseCommit, digestOf(0xb2), blockA)
	if res := ingest(t, l, stagedCommit); res.Outcome != OutcomeStaged {
		t.Fatalf("staged commit = %s", res.Outcome)
	}
	freezeRes := ingest(t, l, mustSign(t, k, roster.ChainID(), 1, 0, PhasePrepare, blockB, parent))
	if freezeRes.Outcome != OutcomeFrozen {
		t.Fatalf("conflict = %s", freezeRes.Outcome)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	l2, err := Open(dir, roster)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer l2.Close()

	frozen, ev, err := l2.IsFrozen(k.id)
	if err != nil || !frozen {
		t.Fatalf("after restart frozen=%v err=%v", frozen, err)
	}
	if err := VerifyEvidence(roster, *ev); err != nil {
		t.Fatalf("persisted evidence invalid: %v", err)
	}
	snap, _ := l2.Snapshot(k.id)
	if len(snap.Staged) != 1 || !sharesEqual(snap.Staged[0], stagedCommit) {
		t.Fatalf("staged not rebuilt: %+v", snap.Staged)
	}
	if len(snap.Accepted) != 1 {
		t.Fatalf("accepted index not rebuilt: %+v", snap.Accepted)
	}
	res, _ := l2.Ingest(mustSign(t, k, roster.ChainID(), 9, 0, PhasePrepare, digestOf(0xb3), blockA))
	if res.Outcome != OutcomeFrozen {
		t.Fatalf("ingest after restart = %s, want frozen", res.Outcome)
	}
}

// TestTornFrameTruncation：WAL 尾部半帧模拟批次中途崩溃，重放截断撕裂帧，
// 此前完整事件（含冻结）全部保留。
func TestTornFrameTruncation(t *testing.T) {
	keys := newTestKeys(t, 2)
	roster := testRoster(t, keys)
	dir := t.TempDir()
	k := keys[0]

	l, _ := Open(dir, roster)
	ingest(t, l, mustSign(t, k, roster.ChainID(), 1, 0, PhasePrepare, digestOf(0xc0), roster.GenesisDigest()))
	freezeRes := ingest(t, l, mustSign(t, k, roster.ChainID(), 1, 0, PhasePrepare, digestOf(0xc1), roster.GenesisDigest()))
	if freezeRes.Outcome != OutcomeFrozen {
		t.Fatalf("conflict = %s", freezeRes.Outcome)
	}
	wal := memberWALPath(dir, k.id)
	info, _ := os.Stat(wal)
	goodSize := info.Size()
	_ = l.Close()

	f, err := os.OpenFile(wal, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open wal: %v", err)
	}
	if _, err := f.Write([]byte{0xde, 0xad, 0xbe, 0xef, 0x00, 0x11, 0x22}); err != nil {
		t.Fatalf("append garbage: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close wal: %v", err)
	}

	l2, err := Open(dir, roster)
	if err != nil {
		t.Fatalf("reopen after torn write: %v", err)
	}
	defer l2.Close()
	frozen, _, _ := l2.IsFrozen(k.id)
	if !frozen {
		t.Fatal("confirmed freeze must survive a torn trailing frame")
	}
	info2, _ := os.Stat(wal)
	if info2.Size() != goodSize {
		t.Fatalf("wal size = %d, want truncated to %d", info2.Size(), goodSize)
	}
}

// TestRosterMismatchRejected：重启时清单被篡改必须拒绝打开。
func TestRosterMismatchRejected(t *testing.T) {
	keys := newTestKeys(t, 2)
	roster := testRoster(t, keys)
	dir := t.TempDir()
	l, _ := Open(dir, roster)
	_ = l.Close()

	tampered := memberRoster(keys, "other-chain", roster.GenesisDigest())
	if _, err := Open(dir, tampered); err == nil {
		t.Fatal("expected chain id mismatch error")
	}
	otherKeys := newTestKeys(t, 2)
	otherRoster := memberRoster(otherKeys, roster.ChainID(), roster.GenesisDigest())
	if _, err := Open(dir, otherRoster); err == nil {
		t.Fatal("expected public key mismatch error")
	}
}

// TestPurgeAndUnfreeze：冻结成员必须先清空暂存矛盾才能解冻，之后可继续参与。
func TestPurgeAndUnfreeze(t *testing.T) {
	keys := newTestKeys(t, 2)
	roster := testRoster(t, keys)
	l := openTestLedger(t, roster)
	k := keys[0]

	ingest(t, l, mustSign(t, k, roster.ChainID(), 1, 0, PhasePrepare, digestOf(0xd0), roster.GenesisDigest()))
	res := ingest(t, l, mustSign(t, k, roster.ChainID(), 2, 0, PhaseCommit, digestOf(0xd1), digestOf(0xd0)))
	if res.Outcome != OutcomeStaged {
		t.Fatalf("staged = %s", res.Outcome)
	}
	ingest(t, l, mustSign(t, k, roster.ChainID(), 1, 0, PhasePrepare, digestOf(0xd2), roster.GenesisDigest()))

	if err := l.Unfreeze(k.id); err == nil {
		t.Fatal("unfreeze with staged contradictions must fail")
	}
	n, err := l.PurgeStaged(k.id)
	if err != nil || n != 1 {
		t.Fatalf("purge n=%d err=%v", n, err)
	}
	if err := l.Unfreeze(k.id); err != nil {
		t.Fatalf("unfreeze after purge: %v", err)
	}
	if _, ok, _ := l.FrozenEvidence(k.id); !ok {
		t.Fatal("evidence must remain archived after unfreeze")
	}
	res = ingest(t, l, mustSign(t, k, roster.ChainID(), 3, 0, PhasePrepare, digestOf(0xd3), roster.GenesisDigest()))
	if res.Outcome != OutcomeAccepted {
		t.Fatalf("after unfreeze = %s (%s)", res.Outcome, res.Reason)
	}
}

// TestBatchStopsOnFreeze：批次中任一冲突即冻结并停止，已处理结果全部返回。
func TestBatchStopsOnFreeze(t *testing.T) {
	keys := newTestKeys(t, 2)
	roster := testRoster(t, keys)
	l := openTestLedger(t, roster)
	k := keys[0]
	g := roster.GenesisDigest()
	batch := []Share{
		mustSign(t, k, roster.ChainID(), 1, 0, PhasePrepare, digestOf(0xe0), g),
		mustSign(t, k, roster.ChainID(), 1, 0, PhasePrepare, digestOf(0xe1), g),
		mustSign(t, k, roster.ChainID(), 2, 0, PhasePrepare, digestOf(0xe2), g),
	}
	results, err := l.IngestBatch(batch)
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if len(results) != 2 || results[1].Outcome != OutcomeFrozen {
		t.Fatalf("results = %+v", results)
	}
}
