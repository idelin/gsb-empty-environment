package dsledger

import (
	"bytes"
	"crypto/ed25519"
	"testing"
)

// TestEvidenceCannotBeForged：无私钥者篡改证据任一字段都会被独立验证拒绝。
func TestEvidenceCannotBeForged(t *testing.T) {
	keys := newTestKeys(t, 2)
	roster := testRoster(t, keys)
	l := openTestLedger(t, roster)
	k := keys[0]
	blockA, blockB, parent := digestOf(0xf0), digestOf(0xf1), roster.GenesisDigest()
	ingest(t, l, mustSign(t, k, roster.ChainID(), 1, 0, PhasePrepare, blockA, parent))
	res := ingest(t, l, mustSign(t, k, roster.ChainID(), 1, 0, PhasePrepare, blockB, parent))
	good := *res.Evidence
	if err := VerifyEvidence(roster, good); err != nil {
		t.Fatalf("good evidence: %v", err)
	}

	// 1. 篡改高度：签名仍对旧高度有效，结构校验必须失败。
	tampered := good
	tampered.First.Height = 99
	if err := VerifyEvidence(roster, tampered); err == nil {
		t.Fatal("tampered height must fail verification")
	}
	// 2. 伪造签名：无私钥，固定字节不可能通过 Ed25519。
	forged := good
	forged.First.Sig = bytes.Repeat([]byte{0x01}, ed25519.SignatureSize)
	if err := VerifyEvidence(roster, forged); err == nil {
		t.Fatal("forged signature must fail verification")
	}
	// 3. 张冠李戴：把证据安到另一成员名下。
	wrongSigner := good
	wrongSigner.Signer = keys[1].id
	wrongSigner.First.Signer = keys[1].id
	wrongSigner.Second.Signer = keys[1].id
	if err := VerifyEvidence(roster, wrongSigner); err == nil {
		t.Fatal("evidence under wrong signer must fail")
	}
	// 4. 两份相同份额不构成证据。
	same := good
	same.Second = same.First
	if err := VerifyEvidence(roster, same); err == nil {
		t.Fatal("identical shares must not be valid double-sign evidence")
	}
	// 5. 合法单份额无法拼成同高度冲突证据（区块相同）。
	honest := mustSign(t, k, roster.ChainID(), 1, 0, PhasePrepare, blockA, parent)
	fake := Evidence{Kind: ConflictSameHeight, Signer: k.id, First: honest, Second: honest}
	if err := VerifyEvidence(roster, fake); err == nil {
		t.Fatal("same block cannot be presented as same-height conflict")
	}
}

// TestOrdinaryForkDoesNotConvict：网络层普通分叉只要成员始终只签一条链，就不会被定罪。
func TestOrdinaryForkDoesNotConvict(t *testing.T) {
	keys := newTestKeys(t, 3)
	roster := testRoster(t, keys)
	l := openTestLedger(t, roster)
	g := roster.GenesisDigest()

	// 两个成员分别在同高度 prepare 不同区块（网络分叉），各自只签一个区块。
	a := digestOf(0x1a)
	b := digestOf(0x1b)
	r1 := ingest(t, l, mustSign(t, keys[0], roster.ChainID(), 1, 0, PhasePrepare, a, g))
	r2 := ingest(t, l, mustSign(t, keys[1], roster.ChainID(), 1, 0, PhasePrepare, b, g))
	if r1.Outcome != OutcomeAccepted || r2.Outcome != OutcomeAccepted {
		t.Fatalf("honest forks across members: %s / %s", r1.Outcome, r2.Outcome)
	}
	for _, key := range keys {
		if frozen, _, _ := l.IsFrozen(key.id); frozen {
			t.Fatalf("member %s frozen without double signing", key.id)
		}
	}

	// 同一成员沿自己的链继续 commit 与下一高度，全程单一父链，合法。
	ingest(t, l, mustSign(t, keys[0], roster.ChainID(), 1, 0, PhaseCommit, a, g))
	next := ingest(t, l, mustSign(t, keys[0], roster.ChainID(), 2, 0, PhasePrepare, digestOf(0x2a), a))
	if next.Outcome != OutcomeAccepted {
		t.Fatalf("honest continuation = %s (%s)", next.Outcome, next.Reason)
	}
}

// TestInvalidShareRejected：坏签名、错误链 ID 与未知签名者均被拒绝且不落任何状态。
func TestInvalidShareRejected(t *testing.T) {
	keys := newTestKeys(t, 2)
	roster := testRoster(t, keys)
	l := openTestLedger(t, roster)
	k := keys[0]

	s := mustSign(t, k, roster.ChainID(), 1, 0, PhasePrepare, digestOf(0x42), roster.GenesisDigest())
	badSig := s
	badSig.Sig = append([]byte(nil), s.Sig...)
	badSig.Sig[0] ^= 0xff
	if res, _ := l.Ingest(badSig); res.Outcome != OutcomeStale {
		t.Fatalf("bad signature = %s, want rejected", res.Outcome)
	}
	badChain := s
	badChain.ChainID = "other-chain"
	if res, _ := l.Ingest(badChain); res.Outcome != OutcomeStale {
		t.Fatalf("bad chain id = %s, want rejected", res.Outcome)
	}
	badSigner := s
	badSigner.Signer = MemberID("nobody")
	if res, _ := l.Ingest(badSigner); res.Outcome != OutcomeStale {
		t.Fatalf("unknown signer = %s, want rejected", res.Outcome)
	}
	snap, _ := l.Snapshot(k.id)
	if len(snap.Accepted) != 0 || len(snap.Staged) != 0 {
		t.Fatalf("invalid shares must not persist state: %+v", snap)
	}
}

// TestStagedRoundReplaceNoOverwrite：暂存中的低轮次被同区块高轮次替换，
// 高轮次先到时低轮次再被拒绝，证明任何路径都不能后到覆盖。
func TestStagedRoundReplaceNoOverwrite(t *testing.T) {
	keys := newTestKeys(t, 2)
	roster := testRoster(t, keys)
	l := openTestLedger(t, roster)
	k := keys[0]
	// 父块未知，两条 prepare 都进入暂存。
	unknown := digestOf(0x55)
	low := mustSign(t, k, roster.ChainID(), 2, 1, PhasePrepare, digestOf(0x56), unknown)
	high := mustSign(t, k, roster.ChainID(), 2, 3, PhasePrepare, digestOf(0x56), unknown)

	if res := ingest(t, l, low); res.Outcome != OutcomeStaged {
		t.Fatalf("low = %s", res.Outcome)
	}
	if res := ingest(t, l, high); res.Outcome != OutcomeStaged || res.Reason != "replaced lower-round staged share" {
		t.Fatalf("high replace = %s (%s)", res.Outcome, res.Reason)
	}
	if res := ingest(t, l, low); res.Outcome != OutcomeStale {
		t.Fatalf("old low again = %s, want stale (cannot overwrite higher round)", res.Outcome)
	}
	snap, _ := l.Snapshot(k.id)
	if len(snap.Staged) != 1 || snap.Staged[0].Round != 3 {
		t.Fatalf("staged = %+v, want only round 3", snap.Staged)
	}
}
