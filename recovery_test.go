package netsec

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"hash"
	"math/big"
	"testing"
)

// mintSetup 找到 p <= 2^31 且 p-1 被 lcm(qs) 整除的素数，并返回阶恰为 qs[i] 的元素 g_i。
func mintSetup(t *testing.T, qs ...int) (*big.Int, []*big.Int) {
	t.Helper()
	need := 1
	for _, q := range qs {
		need = lcmInt(need, q)
	}
	step := big.NewInt(int64(need))
	// 起点取较大值，保证 p-1 为 need 的倍数且 p 为素数候选。
	k := big.NewInt(1)
	var p *big.Int
	for {
		cand := new(big.Int).Add(big.NewInt(1), new(big.Int).Mul(step, k))
		if cand.Cmp(maxPrime) > 0 {
			t.Fatalf("no prime <= 2^31 with p-1 divisible by %d", need)
		}
		if cand.ProbablyPrime(20) {
			p = cand
			break
		}
		k.Add(k, big.NewInt(1))
	}
	// p-1 = need * k0，用原根式构造：取随机基 a，g = a^((p-1)/q) 后验证阶。
	pMinus1 := new(big.Int).Sub(p, big.NewInt(1))
	gs := make([]*big.Int, len(qs))
	// 先找到模 p 的一个原根：对 p-1 的每个素因子 r 验证 a^((p-1)/r) != 1。
	var base *big.Int
	for a := big.NewInt(2); a.Cmp(p) < 0; a.Add(a, big.NewInt(1)) {
		primitive := true
		for _, factor := range primeFactors(int(pMinus1.Int64())) {
			if new(big.Int).Exp(a, new(big.Int).Quo(pMinus1, big.NewInt(int64(factor))), p).Cmp(big.NewInt(1)) == 0 {
				primitive = false
				break
			}
		}
		if primitive {
			base = new(big.Int).Set(a)
			break
		}
	}
	if base == nil {
		t.Fatalf("no primitive root found modulo p=%s", p)
	}
	for i, q := range qs {
		exp := new(big.Int).Quo(pMinus1, big.NewInt(int64(q)))
		g := new(big.Int).Exp(base, exp, p)
		if err := ExactOrder(p, g, q); err != nil {
			t.Fatalf("mintSetup: element order mismatch for q=%d: %v", q, err)
		}
		gs[i] = g
	}
	return p, gs
}

func lcmInt(a, b int) int {
	g := new(big.Int).GCD(nil, nil, big.NewInt(int64(a)), big.NewInt(int64(b))).Int64()
	return a / int(g) * b
}

func ch(b byte) []byte { return []byte{b} }

func TestValidateAndBound(t *testing.T) {
	if _, err := NewSession(big.NewInt(0), VulnerableCallback(big.NewInt(1))); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("B=0 must be rejected, got %v", err)
	}
	if _, err := NewSession(new(big.Int).Lsh(big.NewInt(1), 32), VulnerableCallback(big.NewInt(1))); err != nil {
		t.Fatalf("B=2^32 must be accepted: %v", err)
	}
	if _, err := NewSession(new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 32), big.NewInt(1)), VulnerableCallback(big.NewInt(1))); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("B>2^32 must be rejected")
	}
	if _, err := NewSession(big.NewInt(10), nil); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("nil callback must be rejected")
	}

	p, gs := mintSetup(t, 7)
	g := gs[0]
	cases := []struct {
		name string
		hp   HandshakeParams
	}{
		{"zero g", HandshakeParams{P: p, G: big.NewInt(0), Order: 7}},
		{"g equals p", HandshakeParams{P: p, G: new(big.Int).Set(p), Order: 7}},
		{"q too large", HandshakeParams{P: p, G: g, Order: MaxOrder + 1}},
		{"q zero", HandshakeParams{P: p, G: g, Order: 0}},
		{"non-prime p", HandshakeParams{P: big.NewInt(100), G: big.NewInt(3), Order: 7}},
	}
	for _, tc := range cases {
		if err := ValidateParams(&tc.hp); !errors.Is(err, ErrInvalidParams) {
			t.Fatalf("%s: expected ErrInvalidParams, got %v", tc.name, err)
		}
	}
}

func TestForgedOrderRejected(t *testing.T) {
	p, gs := mintSetup(t, 6)
	g := gs[0] // 真实阶为 6

	// 声称阶为真实阶的倍数：g^q=1 成立，但 g^(q/r)=1，必须被识破。
	forged := HandshakeParams{P: p, G: g, Order: 12, Challenge: ch(1)}
	if err := ExactOrder(p, g, 12); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("forged order 12 (real 6) must fail, got %v", err)
	}

	sess, err := NewSession(big.NewInt(1<<20), VulnerableCallback(big.NewInt(12345)))
	if err != nil {
		t.Fatal(err)
	}
	obs := sess.Observe(0, forged)
	if obs.Status != StatusBadOrder {
		t.Fatalf("forged-order observation must be StatusBadOrder, got %s", obs.Status)
	}
	res := sess.Derive()
	if len(res.MergeSteps) != 0 || !res.Truncated ||
		res.Progressions[0].Step.Int64() != 1 ||
		res.Progressions[0].Count.Int64() != 1<<20 {
		t.Fatalf("bad-order observation must produce no congruence")
	}

	// 白名单同样拒绝伪造阶元素。
	approvals := NewApprovalSet()
	if err := approvals.Allow(forged); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("Allow must reject forged order, got %v", err)
	}

	// 取一个阶恰为 3 的元素并伪造为阶 6：g^6=1 且 g^(6/2)=g^3=1。
	_, gs3 := mintSetup(t, 3)
	forgedDivisor := HandshakeParams{P: p, G: gs3[0], Order: 6, Challenge: ch(2)}
	if err := ExactOrder(p, gs3[0], 6); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("forged order 6 (real 3) must fail, got %v", err)
	}
	_ = forgedDivisor
}

func TestNonCoprimeCompatibility(t *testing.T) {
	// q=6 与 q=10 非互素，gcd=2；真实 d 同时满足两条同余，合并模数应为 lcm=30。
	p, gs := mintSetup(t, 6, 10)
	secret := big.NewInt(1234567)
	sess, err := NewSession(big.NewInt(1<<24), VulnerableCallback(secret))
	if err != nil {
		t.Fatal(err)
	}
	obs1 := sess.Observe(1, HandshakeParams{P: p, G: gs[0], Order: 6, Challenge: ch(0x11)})
	obs2 := sess.Observe(2, HandshakeParams{P: p, G: gs[1], Order: 10, Challenge: ch(0x22)})
	if obs1.Status != StatusUnique || obs2.Status != StatusUnique {
		t.Fatalf("expected unique residues, got %s/%s", obs1.Status, obs2.Status)
	}
	wantR1 := new(big.Int).Mod(secret, big.NewInt(6)).Int64()
	wantR2 := new(big.Int).Mod(secret, big.NewInt(10)).Int64()
	if int64(obs1.Residues[0]) != wantR1 || int64(obs2.Residues[0]) != wantR2 {
		t.Fatalf("residues %d,%d want %d,%d", obs1.Residues[0], obs2.Residues[0], wantR1, wantR2)
	}
	res := sess.Derive()
	if len(res.Conflicts) != 0 || len(res.Progressions) != 1 {
		t.Fatalf("compatible merge expected, conflicts=%v progressions=%d", res.Conflicts, len(res.Progressions))
	}
	if res.Progressions[0].Step.Int64() != 30 {
		t.Fatalf("merged step must be lcm(6,10)=30, got %s", res.Progressions[0].Step)
	}
	if res.Progressions[0].First.Int64() != new(big.Int).Mod(secret, big.NewInt(30)).Int64() {
		t.Fatalf("merged residue mismatch")
	}
	// 每个候选都必须同时满足两条同余。
	for _, cand := range res.Candidates {
		if new(big.Int).Mod(cand, big.NewInt(6)).Int64() != wantR1 ||
			new(big.Int).Mod(cand, big.NewInt(10)).Int64() != wantR2 {
			t.Fatalf("candidate %s violates an observation", cand)
		}
	}

	// mergeCRT 直测：兼容、非互素、不同余表示归一。
	r, mod, ok := mergeCRT(big.NewInt(1), big.NewInt(6), big.NewInt(3), big.NewInt(10))
	if !ok || mod.Int64() != 30 || r.Int64() != 13 {
		t.Fatalf("CRT(1 mod 6, 3 mod 10) = %s mod %s ok=%v, want 13 mod 30", r, mod, ok)
	}
}

func TestConflictOrderIdentifiesExperiments(t *testing.T) {
	p, gs := mintSetup(t, 6, 10)
	sess, err := NewSession(big.NewInt(1<<20), VulnerableCallback(big.NewInt(42)))
	if err != nil {
		t.Fatal(err)
	}
	obs1 := sess.Observe(11, HandshakeParams{P: p, G: gs[0], Order: 6, Challenge: ch(1)})
	obs2 := sess.Observe(12, HandshakeParams{P: p, G: gs[1], Order: 10, Challenge: ch(2)})

	// 白盒篡改第二条观测的同余，制造 d 奇偶性矛盾（gcd(6,10)=2）。
	obs2.Residues = []int{(obs1.Residues[0] + 1) % 10}
	// 确保确实与 obs1 不同奇偶。
	if obs2.Residues[0]%2 == obs1.Residues[0]%2 {
		obs2.Residues[0] = (obs2.Residues[0] + 1) % 10
	}
	res := sess.Derive()
	if len(res.Conflicts) != 1 {
		t.Fatalf("expected exactly one conflict, got %d", len(res.Conflicts))
	}
	c := res.Conflicts[0]
	if c.ObservationID != 12 {
		t.Fatalf("conflict must name incoming experiment #12, got #%d", c.ObservationID)
	}
	if c.GCD.Int64() != 2 {
		t.Fatalf("conflict gcd must be 2, got %s", c.GCD)
	}
	if res.Success || res.UniqueSecret != nil || len(res.Candidates) != 0 {
		t.Fatalf("conflicting observations must not yield a recovered secret")
	}
	if len(res.MergeSteps) == 0 || len(res.MergeSteps[1].Conflicts) != 1 {
		t.Fatalf("merge steps must retain the conflict for replay")
	}

	// 同模数直接矛盾：d ≡ 1 (mod 6) 与 d ≡ 4 (mod 6)。
	if _, _, ok := mergeCRT(big.NewInt(1), big.NewInt(6), big.NewInt(4), big.NewInt(6)); ok {
		t.Fatalf("same-modulus contradiction must be reported as conflict")
	}
}

func TestBBoundary(t *testing.T) {
	p, gs := mintSetup(t, 8)
	g := gs[0]
	cases := []struct {
		name       string
		B, secret  *big.Int
		wantCount  int64
		wantUnique bool
	}{
		{"B=1 forces only candidate 0", big.NewInt(1), big.NewInt(0), 1, true},
		{"B smaller than modulus", big.NewInt(5), big.NewInt(3), 1, true},
		{"B equals 2^32", new(big.Int).Lsh(big.NewInt(1), 32), new(big.Int).SetInt64(4000000000), 1 << 29, false},
	}
	for _, tc := range cases {
		sess, err := NewSession(tc.B, VulnerableCallback(tc.secret))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		obs := sess.Observe(1, HandshakeParams{P: p, G: g, Order: 8, Challenge: ch(7)})
		if obs.Status != StatusUnique {
			t.Fatalf("%s: %s", tc.name, obs.Status)
		}
		res := sess.Derive()
		if res.Progressions[0].Count.Int64() != tc.wantCount {
			t.Fatalf("%s: count=%s want %d", tc.name, res.Progressions[0].Count, tc.wantCount)
		}
		for _, cand := range res.Candidates {
			if cand.Cmp(tc.B) >= 0 {
				t.Fatalf("%s: candidate %s not smaller than B", tc.name, cand)
			}
		}
		if res.Success != tc.wantUnique {
			t.Fatalf("%s: success=%v want %v", tc.name, res.Success, tc.wantUnique)
		}
		if tc.wantUnique && res.UniqueSecret.Cmp(tc.secret) != 0 {
			// B=1 时秘密只能为 0；其余情形唯一候选须等于真实秘密。
			if !(tc.name == "B=1 forces only candidate 0" && res.UniqueSecret.Sign() == 0) {
				t.Fatalf("%s: recovered %s want %s", tc.name, res.UniqueSecret, tc.secret)
			}
		}
	}
}

func TestMultiHandshakeUnique(t *testing.T) {
	// 模数两两互素，7*11*13=1001；B=1500 时单个/两个握手都不唯一，三次握手后模数乘积推过 B 才唯一。
	qs := []int{7, 11, 13}
	p, gs := mintSetup(t, qs...)
	secret := big.NewInt(777)
	sess, err := NewSession(big.NewInt(1500), VulnerableCallback(secret))
	if err != nil {
		t.Fatal(err)
	}
	prev := int64(1 << 62)
	for i, q := range qs {
		obs := sess.Observe(i+1, HandshakeParams{P: p, G: gs[i], Order: q, Challenge: ch(byte(i + 1))})
		if obs.Status != StatusUnique {
			t.Fatalf("handshake %d: %s", i+1, obs.Status)
		}
		res := sess.Derive()
		if len(res.Conflicts) != 0 {
			t.Fatalf("unexpected conflict: %v", res.Conflicts[0].Message)
		}
		cnt := res.Progressions[0].Count.Int64()
		if cnt >= prev {
			t.Fatalf("candidate count must shrink each handshake: %d -> %d", prev, cnt)
		}
		prev = cnt
		if i < len(qs)-1 && res.Success {
			t.Fatalf("must not claim success before enough handshakes")
		}
	}
	res := sess.Derive()
	if !res.Success || res.UniqueSecret.Cmp(secret) != 0 {
		t.Fatalf("expected unique recovery of %s, got success=%v secret=%v", secret, res.Success, res.UniqueSecret)
	}
	if res.Progressions[0].Step.Int64() != 1001 {
		t.Fatalf("final step must be product 1001, got %s", res.Progressions[0].Step)
	}
}

// collisionHash 让同一宽度的所有输入摘要相同，用于确定性地制造多匹配与零匹配。
type collisionHash struct {
	width int
	seen  int
}

func (h *collisionHash) Write(p []byte) (int, error) {
	h.seen += len(p)
	return len(p), nil
}
func (h *collisionHash) Sum(b []byte) []byte {
	out := make([]byte, sha256.Size)
	out[0], out[1] = 0xAB, byte(h.width)
	return append(b, out...)
}
func (h *collisionHash) Reset()         { h.seen = 0 }
func (h *collisionHash) Size() int      { return sha256.Size }
func (h *collisionHash) BlockSize() int { return 1 }

func TestZeroAndMultipleMatches(t *testing.T) {
	origHash := hashFunc
	defer func() { hashFunc = origHash }()
	hashFunc = func() hash.Hash { return &collisionHash{width: 1} }

	p, gs := mintSetup(t, 6)
	g := gs[0]
	hp := HandshakeParams{P: p, G: g, Order: 6, Challenge: ch(9)}
	sess, err := NewSession(big.NewInt(100), VulnerableCallback(big.NewInt(5)))
	if err != nil {
		t.Fatal(err)
	}
	obs := sess.Observe(1, hp)
	if obs.Status != StatusCollision || len(obs.Residues) != 6 {
		t.Fatalf("collision hash must report all 6 matches explicitly, got %s n=%d", obs.Status, len(obs.Residues))
	}
	res := sess.Derive()
	if res.Success {
		t.Fatalf("multiple matches must never be treated as unique recovery")
	}
	if len(res.Progressions) != 6 {
		t.Fatalf("each matched residue is its own branch, got %d", len(res.Progressions))
	}

	// 零匹配：篡改保留摘要后重放，必须显式报告 no-match 且不产生同余。
	tampered := *obs
	tampered.Digest = bytes.Repeat([]byte{0x00}, sha256.Size)
	tampered.Status = StatusUnique
	tampered.Residues = enumerateResidues(hp, tampered.Digest)
	tampered.Status = statusOf(len(tampered.Residues))
	if tampered.Status != StatusNoMatch || len(tampered.Residues) != 0 {
		t.Fatalf("expected explicit no-match, got %s", tampered.Status)
	}
	replayed, err := Replay(big.NewInt(100), []*Observation{&tampered}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(replayed.MergeSteps) != 0 || len(replayed.Candidates) != 100 {
		t.Fatalf("no-match observation must contribute no congruence")
	}
}

func statusOf(n int) string {
	switch n {
	case 0:
		return StatusNoMatch
	case 1:
		return StatusUnique
	default:
		return StatusCollision
	}
}

func TestReplayPreservesChallengesAndDerivation(t *testing.T) {
	p, gs := mintSetup(t, 6, 10)
	secret := big.NewInt(98765)
	sess, err := NewSession(big.NewInt(1<<20), VulnerableCallback(secret))
	if err != nil {
		t.Fatal(err)
	}
	sess.Observe(1, HandshakeParams{P: p, G: gs[0], Order: 6, Challenge: []byte("challenge-alpha")})
	sess.Observe(2, HandshakeParams{P: p, G: gs[1], Order: 10, Challenge: []byte("challenge-beta")})
	original := sess.Derive()

	// 重放只依赖保留的观测：挑战、宽度、摘要全部在其中，不调用任何回调。
	replayed, err := Replay(big.NewInt(1<<20), sess.Observations(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if string(replayed.Observations[0].Handshake.Challenge) != "challenge-alpha" ||
		string(replayed.Observations[1].Handshake.Challenge) != "challenge-beta" {
		t.Fatalf("challenges must be retained for replay")
	}
	if replayed.Progressions[0].First.Cmp(original.Progressions[0].First) != 0 ||
		replayed.Progressions[0].Step.Cmp(original.Progressions[0].Step) != 0 ||
		replayed.Progressions[0].Count.Cmp(original.Progressions[0].Count) != 0 {
		t.Fatalf("replay derivation must match live derivation")
	}

	// 挑战被改动后，保留的摘要不再匹配：重放显式变为 no-match。
	tampered := make([]*Observation, len(sess.Observations()))
	for i, obs := range sess.Observations() {
		clone := *obs
		clone.Handshake = copyHandshake(obs.Handshake)
		clone.Handshake.Challenge = []byte("rotated-challenge")
		clone.Residues = enumerateResidues(clone.Handshake, clone.Digest)
		clone.Status = statusOf(len(clone.Residues))
		tampered[i] = &clone
	}
	rotated, err := Replay(big.NewInt(1<<20), tampered, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, obs := range rotated.Observations {
		if obs.Status != StatusNoMatch {
			t.Fatalf("rotated challenge must yield no-match, got %s", obs.Status)
		}
	}
}

func TestHardenedCallbackRejectsUnapproved(t *testing.T) {
	p, gs := mintSetup(t, 6, 10)
	approvals := NewApprovalSet()
	approved := HandshakeParams{P: p, G: gs[0], Order: 6, Challenge: ch(3)}
	if err := approvals.Allow(approved); err != nil {
		t.Fatal(err)
	}
	cb, err := HardenedCallback(big.NewInt(99), approvals)
	if err != nil {
		t.Fatal(err)
	}

	// 已批准元素正常返回摘要，且与规范摘要一致。
	got, err := cb(approved)
	if err != nil {
		t.Fatalf("approved element must be accepted: %v", err)
	}
	shared := new(big.Int).Exp(approved.G, big.NewInt(99), p)
	want := HandshakeDigest(p, shared, approved.Challenge)
	if !bytes.Equal(got, want) {
		t.Fatalf("hardened digest mismatch")
	}

	// 未批准但阶合法的小子群元素被拒绝，拒绝是错误而非同余。
	unapproved := HandshakeParams{P: p, G: gs[1], Order: 10, Challenge: ch(4)}
	if _, err := cb(unapproved); !errors.Is(err, ErrElementNotApproved) {
		t.Fatalf("unapproved element must be rejected, got %v", err)
	}
	sess, err := NewSession(big.NewInt(1<<16), cb)
	if err != nil {
		t.Fatal(err)
	}
	obs := sess.Observe(7, unapproved)
	if obs.Status != StatusRejected || !errors.Is(obs.Err, ErrElementNotApproved) {
		t.Fatalf("rejection must be recorded, got %s", obs.Status)
	}
	res := sess.Derive()
	if len(res.MergeSteps) != 0 || !res.Truncated ||
		res.Progressions[0].Step.Int64() != 1 ||
		res.Progressions[0].Count.Int64() != 1<<16 || res.Success {
		t.Fatalf("rejection must produce no congruence")
	}

	// 伪造阶元素在进入白名单前即被拒绝。
	forged := HandshakeParams{P: p, G: gs[0], Order: 12, Challenge: ch(5)}
	if err := approvals.Allow(forged); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("forged order cannot be approved, got %v", err)
	}
}

func TestFixedWidthDigest(t *testing.T) {
	// 选一个跨越字节宽度边界的素数，验证共享秘密以 p 的字节长度固定宽度大端编码。
	p, gs := mintSetup(t, 5)
	g := gs[0]
	secret := big.NewInt(17)
	shared := new(big.Int).Exp(g, secret, p)
	challenge := []byte("nonce")
	width := byteLen(p)
	buf := make([]byte, width+len(challenge))
	shared.FillBytes(buf[:width])
	copy(buf[width:], challenge)
	sum := sha256.Sum256(buf)
	if !bytes.Equal(HandshakeDigest(p, shared, challenge), sum[:]) {
		t.Fatalf("digest must be SHA-256 over fixed-width big-endian shared secret || challenge")
	}

	sess, err := NewSession(big.NewInt(1<<12), VulnerableCallback(secret))
	if err != nil {
		t.Fatal(err)
	}
	obs := sess.Observe(1, HandshakeParams{P: p, G: g, Order: 5, Challenge: challenge})
	if obs.Width != width || obs.Status != StatusUnique {
		t.Fatalf("width=%d status=%s", obs.Width, obs.Status)
	}
}
