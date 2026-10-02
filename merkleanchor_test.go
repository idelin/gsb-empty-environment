package merkleanchor

import (
	"fmt"
	"testing"
)

func makeLeaves(n int) [][32]byte {
	leaves := make([][32]byte, n)
	for i := 0; i < n; i++ {
		leaves[i] = HashLeaf([]byte(fmt.Sprintf("record-%d", i)))
	}
	return leaves
}

func TestMerkleProofRoundTrip(t *testing.T) {
	for _, n := range []int{1, 2, 3, 4, 5, 7, 8, 13, 100} {
		leaves := makeLeaves(n)
		root := MerkleRoot(leaves)
		for i := 0; i < n; i++ {
			proof := InclusionProof(leaves, i)
			if !VerifyInclusion(leaves[i], proof, root) {
				t.Fatalf("n=%d i=%d: valid proof rejected", n, i)
			}
		}
	}
}

func TestMerkleProofTamperDetected(t *testing.T) {
	leaves := makeLeaves(6)
	root := MerkleRoot(leaves)
	proof := InclusionProof(leaves, 2)

	// 篡改叶子数据
	forged := HashLeaf([]byte("forged-record"))
	if VerifyInclusion(forged, proof, root) {
		t.Fatal("forged leaf accepted")
	}
	// 篡改根
	badRoot := root
	badRoot[0] ^= 0xff
	if VerifyInclusion(leaves[2], proof, badRoot) {
		t.Fatal("proof accepted against wrong root")
	}
	// 篡改路径
	badProof := make([]ProofStep, len(proof))
	copy(badProof, proof)
	badProof[0].Sibling[5] ^= 0xff
	if VerifyInclusion(leaves[2], badProof, root) {
		t.Fatal("proof with tampered sibling accepted")
	}
}

func TestAnchorSignatureAndTimestamp(t *testing.T) {
	a, err := NewAnchorer("ledger-A")
	if err != nil {
		t.Fatal(err)
	}
	a.Now = func() int64 { return 1000 }
	root := MerkleRoot(makeLeaves(4))
	anchor, err := a.Anchor(root)
	if err != nil {
		t.Fatal(err)
	}
	if anchor.Timestamp != 1000 {
		t.Fatalf("timestamp = %d, want 1000", anchor.Timestamp)
	}

	v := NewVerifier(a.PublicKey(), "ledger-A", 60)
	if err := v.VerifyAnchor(anchor, 1000); err != nil {
		t.Fatalf("valid anchor rejected: %v", err)
	}

	// 篡改时间戳 -> 签名失效
	tampered := *anchor
	tampered.Timestamp = 999
	if err := NewVerifier(a.PublicKey(), "ledger-A", 60).VerifyAnchor(&tampered, 1000); err != ErrBadSignature {
		t.Fatalf("tampered timestamp: got %v, want ErrBadSignature", err)
	}
	// 篡改根 -> 签名失效
	tampered2 := *anchor
	tampered2.Root[0] ^= 0xff
	if err := NewVerifier(a.PublicKey(), "ledger-A", 60).VerifyAnchor(&tampered2, 1000); err != ErrBadSignature {
		t.Fatalf("tampered root: got %v, want ErrBadSignature", err)
	}
	// 未来时间戳 -> 拒绝
	a2, _ := NewAnchorer("ledger-A")
	a2.Now = func() int64 { return 5000 }
	future, _ := a2.Anchor(root)
	if err := NewVerifier(a2.PublicKey(), "ledger-A", 60).VerifyAnchor(future, 1000); err != ErrFutureAnchor {
		t.Fatalf("future anchor: got %v, want ErrFutureAnchor", err)
	}
	// 错误链标识 -> 拒绝
	if err := NewVerifier(a.PublicKey(), "ledger-B", 60).VerifyAnchor(anchor, 1000); err != ErrWrongChain {
		t.Fatalf("wrong chain: got %v, want ErrWrongChain", err)
	}
}

func TestAnchorReplayRejected(t *testing.T) {
	a, _ := NewAnchorer("ledger-A")
	a.Now = func() int64 { return 1000 }
	root := MerkleRoot(makeLeaves(3))
	anchor1, _ := a.Anchor(root)
	anchor2, _ := a.Anchor(root)

	v := NewVerifier(a.PublicKey(), "ledger-A", 60)
	if err := v.VerifyAnchor(anchor1, 1000); err != nil {
		t.Fatal(err)
	}
	// 原样重放同一条锚定记录
	if err := v.VerifyAnchor(anchor1, 1000); err != ErrReplay {
		t.Fatalf("replayed anchor: got %v, want ErrReplay", err)
	}
	// 先收 seq=2 再收 seq=1（乱序/回滚攻击）
	v2 := NewVerifier(a.PublicKey(), "ledger-A", 60)
	if err := v2.VerifyAnchor(anchor2, 1000); err != nil {
		t.Fatal(err)
	}
	if err := v2.VerifyAnchor(anchor1, 1000); err != ErrReplay {
		t.Fatalf("out-of-order old anchor: got %v, want ErrReplay", err)
	}
}

// 验收场景：t=1000 锚定根哈希；t=2000 出示审计凭据；
// 凭据证明业务事实在 t=1000 前已入库固定，且无法事后伪造。
func TestAcceptanceScenario(t *testing.T) {
	// ---- t=1000：业务数据入库，锚定器定时提交根哈希 ----
	records := [][]byte{
		[]byte("tx-001: alice pays bob 10"),
		[]byte("tx-002: carol pays dave 3"),
		[]byte("tx-003: erin pays frank 7"),
	}
	leaves := make([][32]byte, len(records))
	for i, r := range records {
		leaves[i] = HashLeaf(r)
	}
	anchorService, _ := NewAnchorer("biz-ledger")
	anchorService.Now = func() int64 { return 1000 }
	anchor, err := anchorService.Anchor(MerkleRoot(leaves))
	if err != nil {
		t.Fatal(err)
	}

	// ---- t=2000：审计方要求出示 tx-002 的存证证书 ----
	cert, err := IssueCertificate(records[1], leaves, anchor)
	if err != nil {
		t.Fatal(err)
	}
	verifier := NewVerifier(anchorService.PublicKey(), "biz-ledger", 60)
	existedBefore, err := VerifyCertificate(cert, records[1], verifier, 2000)
	if err != nil {
		t.Fatalf("valid certificate rejected: %v", err)
	}
	if existedBefore != 1000 {
		t.Fatalf("certificate proves existence before t=%d, want t=1000", existedBefore)
	}

	// 事后伪造的数据无法获得有效证书
	if _, err := IssueCertificate([]byte("tx-004: forged"), leaves, anchor); err != ErrDataNotAnchored {
		t.Fatalf("forged data issued certificate: got %v, want ErrDataNotAnchored", err)
	}
	// 用伪造数据冒充证书中的叶子 -> 校验失败
	v2 := NewVerifier(anchorService.PublicKey(), "biz-ledger", 60)
	if _, err := VerifyCertificate(cert, []byte("tx-004: forged"), v2, 2000); err != ErrBadProof {
		t.Fatalf("forged data verified: got %v, want ErrBadProof", err)
	}
	// 同一张证书重放提交 -> 防重放拒绝
	if _, err := VerifyCertificate(cert, records[1], verifier, 2000); err != ErrReplay {
		t.Fatalf("replayed certificate: got %v, want ErrReplay", err)
	}
	// 未被锚定覆盖的数据（锚定后新入库）不能回溯到 t=1000
	lateLeaves := append(append([][32]byte{}, leaves...), HashLeaf([]byte("tx-004: late")))
	if _, err := IssueCertificate([]byte("tx-004: late"), lateLeaves, anchor); err != ErrDataNotAnchored {
		t.Fatalf("late data backdated: got %v, want ErrDataNotAnchored", err)
	}
}
