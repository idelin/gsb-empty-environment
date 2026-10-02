package anchor

import (
	"testing"
)

// fakeClock 返回可手动推进的业务时钟。
func fakeClock(start int64) (func() int64, *int64) {
	t := new(int64)
	*t = start
	return func() int64 { return *t }, t
}

func newTestAnchorer(t *testing.T, start int64) (*Anchorer, *int64) {
	t.Helper()
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	now, clock := fakeClock(start)
	return NewAnchorer(key, now), clock
}

// TestMerkleProofRoundTrip 对各种树规模验证包含证明。
func TestMerkleProofRoundTrip(t *testing.T) {
	for n := 1; n <= 17; n++ {
		data := make([][]byte, n)
		for i := range data {
			data[i] = []byte{byte(i), byte(n)}
		}
		tree := NewMerkleTree(data)
		for i := 0; i < n; i++ {
			proof, ok := tree.Proof(i)
			if !ok {
				t.Fatalf("n=%d: no proof for leaf %d", n, i)
			}
			if !VerifyProof(HashLeaf(data[i]), proof, tree.Root()) {
				t.Fatalf("n=%d: proof for leaf %d does not verify", n, i)
			}
		}
	}
}

// TestAcceptanceScenario 验收边界：
// t=1000 锚定根哈希 → 后续生成审计凭据 → 凭据证明数据在 t=1000 前已入库固定。
func TestAcceptanceScenario(t *testing.T) {
	anchorer, clock := newTestAnchorer(t, 1000)
	verifier := NewVerifier(anchorer.PublicKey())

	// 业务数据入库，构建默克尔树。
	records := [][]byte{
		[]byte("order#1 pay 100 CNY"),
		[]byte("order#2 pay 200 CNY"),
		[]byte("order#3 refund 50 CNY"),
	}
	tree := NewMerkleTree(records)

	// t=1000：定时任务将根哈希与时间戳打包签名并锚定。
	anc, err := anchorer.AnchorRoot(tree.Root())
	if err != nil {
		t.Fatalf("AnchorRoot: %v", err)
	}
	if anc.Timestamp != 1000 {
		t.Fatalf("anchor timestamp = %d, want 1000", anc.Timestamp)
	}
	if err := verifier.RegisterAnchor(anc); err != nil {
		t.Fatalf("RegisterAnchor: %v", err)
	}

	// 时间推进到 t=5000：后续生成审计凭据。
	*clock = 5000
	cert, err := IssueCertificate(tree, records[1], 1, anc)
	if err != nil {
		t.Fatalf("IssueCertificate: %v", err)
	}

	// 凭据强力证明：该业务事实早在 t=1000 前已入库固定。
	if err := VerifyCertificate(anchorer.PublicKey(), cert, 1000); err != nil {
		t.Fatalf("VerifyCertificate: %v", err)
	}
}

// TestCertificateRejectsLaterAnchor 锚定时间晚于要求时刻时，证明不成立。
func TestCertificateRejectsLaterAnchor(t *testing.T) {
	anchorer, _ := newTestAnchorer(t, 1000)
	tree := NewMerkleTree([][]byte{[]byte("record")})
	anc, err := anchorer.AnchorRoot(tree.Root())
	if err != nil {
		t.Fatalf("AnchorRoot: %v", err)
	}
	cert, err := IssueCertificate(tree, []byte("record"), 0, anc)
	if err != nil {
		t.Fatalf("IssueCertificate: %v", err)
	}
	// 要求证明 t=999 前已存在：锚定在 t=1000，无法满足。
	if err := VerifyCertificate(anchorer.PublicKey(), cert, 999); err == nil {
		t.Fatal("expected failure: anchor timestamp later than required bound")
	}
}

// TestTamperedDataFails 事后篡改数据无法通过验证。
func TestTamperedDataFails(t *testing.T) {
	anchorer, _ := newTestAnchorer(t, 1000)
	tree := NewMerkleTree([][]byte{[]byte("pay 100"), []byte("pay 200")})
	anc, err := anchorer.AnchorRoot(tree.Root())
	if err != nil {
		t.Fatalf("AnchorRoot: %v", err)
	}
	cert, err := IssueCertificate(tree, []byte("pay 100"), 0, anc)
	if err != nil {
		t.Fatalf("IssueCertificate: %v", err)
	}
	cert.Data = []byte("pay 999") // 事后篡改金额
	if err := VerifyCertificate(anchorer.PublicKey(), cert, 1000); err == nil {
		t.Fatal("expected failure: tampered data must not verify")
	}
}

// TestForgedSignatureFails 伪造签名/篡改时间戳无法通过验证。
func TestForgedSignatureFails(t *testing.T) {
	anchorer, _ := newTestAnchorer(t, 1000)
	tree := NewMerkleTree([][]byte{[]byte("record")})
	anc, err := anchorer.AnchorRoot(tree.Root())
	if err != nil {
		t.Fatalf("AnchorRoot: %v", err)
	}
	cert, err := IssueCertificate(tree, []byte("record"), 0, anc)
	if err != nil {
		t.Fatalf("IssueCertificate: %v", err)
	}
	// 攻击者试图把锚定时间改早以伪造“更早存在”。
	cert.Anchor.Timestamp = 1
	if err := VerifyCertificate(anchorer.PublicKey(), cert, 1000); err == nil {
		t.Fatal("expected failure: backdated timestamp breaks the signature")
	}
}

// TestReplayRejected 重放同一锚定记录被拒绝。
func TestReplayRejected(t *testing.T) {
	anchorer, _ := newTestAnchorer(t, 1000)
	verifier := NewVerifier(anchorer.PublicKey())
	tree := NewMerkleTree([][]byte{[]byte("a")})
	anc, err := anchorer.AnchorRoot(tree.Root())
	if err != nil {
		t.Fatalf("AnchorRoot: %v", err)
	}
	if err := verifier.RegisterAnchor(anc); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := verifier.RegisterAnchor(anc); err == nil {
		t.Fatal("expected replay of the same anchor to be rejected")
	}
}

// TestOutOfOrderAndBrokenChainRejected 乱序与断链的锚定被拒绝。
func TestOutOfOrderAndBrokenChainRejected(t *testing.T) {
	anchorer, clock := newTestAnchorer(t, 1000)
	verifier := NewVerifier(anchorer.PublicKey())
	tree := NewMerkleTree([][]byte{[]byte("a")})

	a1, err := anchorer.AnchorRoot(tree.Root())
	if err != nil {
		t.Fatalf("anchor 1: %v", err)
	}
	*clock = 2000
	a2, err := anchorer.AnchorRoot(tree.Root())
	if err != nil {
		t.Fatalf("anchor 2: %v", err)
	}

	// 乱序：先提交 seq=2。
	if err := verifier.RegisterAnchor(a2); err == nil {
		t.Fatal("expected out-of-order anchor to be rejected")
	}
	// 断链：篡改前向哈希。
	bad := *a1
	bad.PrevHash[0] ^= 0xff
	if err := verifier.RegisterAnchor(&bad); err == nil {
		t.Fatal("expected broken-chain anchor to be rejected")
	}
	// 正常顺序登记两条。
	if err := verifier.RegisterAnchor(a1); err != nil {
		t.Fatalf("register a1: %v", err)
	}
	if err := verifier.RegisterAnchor(a2); err != nil {
		t.Fatalf("register a2: %v", err)
	}
}

// TestWrongKeyFails 用错误公钥验证不通过。
func TestWrongKeyFails(t *testing.T) {
	anchorer, _ := newTestAnchorer(t, 1000)
	tree := NewMerkleTree([][]byte{[]byte("record")})
	anc, err := anchorer.AnchorRoot(tree.Root())
	if err != nil {
		t.Fatalf("AnchorRoot: %v", err)
	}
	cert, err := IssueCertificate(tree, []byte("record"), 0, anc)
	if err != nil {
		t.Fatalf("IssueCertificate: %v", err)
	}
	other, _ := newTestAnchorer(t, 1000)
	if err := VerifyCertificate(other.PublicKey(), cert, 1000); err == nil {
		t.Fatal("expected failure: wrong public key")
	}
}
