package dsledger

import (
	"crypto/ed25519"
	"strconv"
	"testing"
)

type testKey struct {
	id   MemberID
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

func newTestKeys(t *testing.T, n int) []testKey {
	t.Helper()
	keys := make([]testKey, n)
	for i := range keys {
		pub, priv, err := ed25519.GenerateKey(nil)
		if err != nil {
			t.Fatalf("generate key: %v", err)
		}
		keys[i] = testKey{id: MemberID("m" + strconv.Itoa(i)), priv: priv, pub: pub}
	}
	return keys
}

func testRoster(t *testing.T, keys []testKey) *Roster {
	t.Helper()
	members := make([]Member, len(keys))
	for i, k := range keys {
		members[i] = Member{ID: k.id, PublicKey: k.pub}
	}
	genesis, _ := ParseDigest("01")
	roster, err := NewRoster("chain-1", genesis, members)
	if err != nil {
		t.Fatalf("new roster: %v", err)
	}
	return roster
}

func openTestLedger(t *testing.T, roster *Roster) *Ledger {
	t.Helper()
	l, err := Open(t.TempDir(), roster)
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func mustSign(t *testing.T, k testKey, chainID string, height, round uint64, phase Phase, block, parent Digest) Share {
	t.Helper()
	s, err := SignShare(k.priv, chainID, k.id, height, round, phase, block, parent)
	if err != nil {
		t.Fatalf("sign share: %v", err)
	}
	return s
}

func digestOf(b byte) Digest {
	var d Digest
	d[0] = b
	return d
}

func ingest(t *testing.T, l *Ledger, s Share) Result {
	t.Helper()
	res, err := l.Ingest(s)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	return res
}

func assertFrozen(t *testing.T, l *Ledger, id MemberID, want ConflictKind) *Evidence {
	t.Helper()
	frozen, ev, err := l.IsFrozen(id)
	if err != nil || !frozen {
		t.Fatalf("member %s frozen=%v err=%v, want frozen", id, frozen, err)
	}
	if ev.Kind != want {
		t.Fatalf("evidence kind = %s, want %s", ev.Kind, want)
	}
	if err := VerifyEvidence(l.Roster(), *ev); err != nil {
		t.Fatalf("evidence not independently verifiable: %v", err)
	}
	return ev
}

// TestOutOfOrderRelease：commit 先到、prepare 后到，补齐后自动发布。
func TestOutOfOrderRelease(t *testing.T) {
	keys := newTestKeys(t, 3)
	roster := testRoster(t, keys)
	l := openTestLedger(t, roster)
	k := keys[0]

	block, parent := digestOf(0x10), roster.GenesisDigest()
	commit := mustSign(t, k, roster.ChainID(), 1, 0, PhaseCommit, block, parent)
	prepare := mustSign(t, k, roster.ChainID(), 1, 0, PhasePrepare, block, parent)

	if res := ingest(t, l, commit); res.Outcome != OutcomeStaged {
		t.Fatalf("commit first = %s, want staged (%s)", res.Outcome, res.Reason)
	}
	res := ingest(t, l, prepare)
	if res.Outcome != OutcomeReleased {
		t.Fatalf("prepare after commit = %s, want released", res.Outcome)
	}
	if len(res.Released) != 1 || res.Released[0].Phase != PhaseCommit {
		t.Fatalf("released = %+v, want the staged commit", res.Released)
	}
	snap, _ := l.Snapshot(k.id)
	if snap.TipHeight != 1 || len(snap.Staged) != 0 {
		t.Fatalf("snapshot = %+v", snap)
	}
}

// TestRoundsAdvanceNoDoubleSign：同区块多轮 prepare/commit 合法，轮次不倒退，不构成双签。
func TestRoundsAdvanceNoDoubleSign(t *testing.T) {
	keys := newTestKeys(t, 2)
	roster := testRoster(t, keys)
	l := openTestLedger(t, roster)
	k := keys[0]
	block, parent := digestOf(0x20), roster.GenesisDigest()

	p0 := mustSign(t, k, roster.ChainID(), 1, 0, PhasePrepare, block, parent)
	p1 := mustSign(t, k, roster.ChainID(), 1, 1, PhasePrepare, block, parent)
	p0Again := mustSign(t, k, roster.ChainID(), 1, 0, PhasePrepare, block, parent)

	if res := ingest(t, l, p0); res.Outcome != OutcomeAccepted {
		t.Fatalf("p0 = %s", res.Outcome)
	}
	if res := ingest(t, l, p1); res.Outcome != OutcomeAccepted {
		t.Fatalf("p1 higher round = %s, want accepted (round advance)", res.Outcome)
	}
	// 相同字段重新签名得到字节完全相同的份额：幂等重复。
	if res := ingest(t, l, p0Again); res.Outcome != OutcomeDuplicate {
		t.Fatalf("identical re-sign = %s, want duplicate", res.Outcome)
	}
	// 已见证到 round 1 后，任何 <= 1 的同区块轮次都不能倒退（round 0 已被占用，
	// 这里用一条新的 round-1 同区块份额验证 MaxRound 防倒退）。
	p1Again := mustSign(t, k, roster.ChainID(), 1, 1, PhasePrepare, block, parent)
	if res := ingest(t, l, p1Again); res.Outcome != OutcomeStale {
		t.Fatalf("non-advancing round = %s, want stale", res.Outcome)
	}
	c1 := mustSign(t, k, roster.ChainID(), 1, 1, PhaseCommit, block, parent)
	if res := ingest(t, l, c1); res.Outcome != OutcomeAccepted {
		t.Fatalf("commit r1 = %s, want accepted", res.Outcome)
	}
	// 更高轮次的 commit 仅推进见证轮次，原始份额不被覆盖。
	c2 := mustSign(t, k, roster.ChainID(), 1, 2, PhaseCommit, block, parent)
	ingest(t, l, c2)
	snap, _ := l.Snapshot(k.id)
	for _, s := range snap.Accepted {
		if s.Phase == PhasePrepare && s.Round != 0 {
			t.Fatalf("first prepare must not be overwritten, got round %d", s.Round)
		}
	}
	if frozen, _, _ := l.IsFrozen(k.id); frozen {
		t.Fatal("honest round advance must not freeze member")
	}
}

// TestRoundConflictFreezes：commit 轮次低于其 prepare 轮次，同高度轮次冲突定罪。
func TestRoundConflictFreezes(t *testing.T) {
	keys := newTestKeys(t, 2)
	roster := testRoster(t, keys)
	l := openTestLedger(t, roster)
	k := keys[0]
	block, parent := digestOf(0x30), roster.GenesisDigest()

	ingest(t, l, mustSign(t, k, roster.ChainID(), 1, 2, PhasePrepare, block, parent))
	lowCommit := mustSign(t, k, roster.ChainID(), 1, 1, PhaseCommit, block, parent)
	res := ingest(t, l, lowCommit)
	if res.Outcome != OutcomeFrozen || res.Evidence == nil {
		t.Fatalf("low-round commit = %+v, want frozen with evidence", res)
	}
	assertFrozen(t, l, k.id, ConflictRound)

	// 冻结后所有后续份额一律拒绝，不能借新消息“洗白”。
	after := ingest(t, l, mustSign(t, k, roster.ChainID(), 2, 0, PhasePrepare, digestOf(0x31), block))
	if after.Outcome != OutcomeFrozen {
		t.Fatalf("ingest while frozen = %s, want frozen", after.Outcome)
	}
}

// TestSameHeightBlockConflict：同高度同阶段两个不同区块即双签；不同阶段不互相构成双签。
func TestSameHeightBlockConflict(t *testing.T) {
	keys := newTestKeys(t, 2)
	roster := testRoster(t, keys)
	l := openTestLedger(t, roster)
	k := keys[0]
	blockA, blockB, parent := digestOf(0x40), digestOf(0x41), roster.GenesisDigest()

	ingest(t, l, mustSign(t, k, roster.ChainID(), 1, 0, PhasePrepare, blockA, parent))
	// prepare 与 commit 同高不同阶段、不同区块：commit 缺前置，暂存，不定罪。
	res := ingest(t, l, mustSign(t, k, roster.ChainID(), 1, 0, PhaseCommit, blockB, parent))
	if res.Outcome != OutcomeStaged {
		t.Fatalf("commit for other block = %s, want staged", res.Outcome)
	}
	// 同高度同阶段的第二区块立即双签。
	conflict := mustSign(t, k, roster.ChainID(), 1, 0, PhasePrepare, blockB, parent)
	res = ingest(t, l, conflict)
	if res.Outcome != OutcomeFrozen || res.Evidence == nil {
		t.Fatalf("second prepare = %+v, want frozen", res)
	}
	ev := assertFrozen(t, l, k.id, ConflictSameHeight)
	if ev.First.Block != blockB || ev.Second.Block != blockA {
		t.Fatalf("evidence blocks wrong: %x / %x", ev.First.Block, ev.Second.Block)
	}
}
