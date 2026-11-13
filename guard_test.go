package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
)

// testPKI 用 ed25519 模拟客户端公钥基础设施。
type testPKI struct {
	keys map[string]ed25519.PrivateKey
}

func newTestPKI(t *testing.T, users []string) *testPKI {
	t.Helper()
	pki := &testPKI{keys: make(map[string]ed25519.PrivateKey, len(users))}
	for _, id := range users {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("generate key for %s: %v", id, err)
		}
		pki.keys[id] = priv
	}
	return pki
}

func (p *testPKI) verifier() SignatureVerifier {
	return func(signer string, message, signature []byte) bool {
		priv, ok := p.keys[signer]
		if !ok {
			return false
		}
		return ed25519.Verify(priv.Public().(ed25519.PublicKey), message, signature)
	}
}

func (p *testPKI) sign(t *testing.T, signer string, aliveSet []string) Signature {
	t.Helper()
	priv, ok := p.keys[signer]
	if !ok {
		t.Fatalf("unknown signer %q", signer)
	}
	return Signature{
		SignerID: signer,
		Signed:   append([]string(nil), aliveSet...),
		Value:    ed25519.Sign(priv, CanonicalAliveSetMessage(aliveSet)),
	}
}

func (p *testPKI) signAll(t *testing.T, signers, aliveSet []string) []Signature {
	t.Helper()
	sigs := make([]Signature, 0, len(signers))
	for _, id := range signers {
		sigs = append(sigs, p.sign(t, id, aliveSet))
	}
	return sigs
}

// advanceToConsistency 走完广播密钥 -> 分享 -> 掩码输入，并记录 U2。
func advanceToConsistency(t *testing.T, g *Guard, u2 []string) {
	t.Helper()
	phases := []Phase{PhaseShareSeeds, PhaseMaskedInput}
	for _, ph := range phases {
		if err := g.AdvanceTo(ph); err != nil {
			t.Fatalf("advance to %s: %v", ph, err)
		}
	}
	if err := g.RecordAliveSet(u2); err != nil {
		t.Fatalf("record U2: %v", err)
	}
	if err := g.AdvanceTo(PhaseConsistencySign); err != nil {
		t.Fatalf("advance to consistency: %v", err)
	}
}

func confirmAndEnterDecrypt(t *testing.T, g *Guard, u3 []string, sigs []Signature) {
	t.Helper()
	if err := g.ConfirmAliveSet(u3, sigs); err != nil {
		t.Fatalf("confirm U3: %v", err)
	}
	if err := g.AdvanceTo(PhaseDecrypt); err != nil {
		t.Fatalf("advance to decrypt: %v", err)
	}
}

func assertErrorIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want error %v, got %v", target, err)
	}
}

func TestHappyPathDisclosesExpectedShareKinds(t *testing.T) {
	const threshold = 3
	u2 := []string{"u1", "u2", "u3", "u4", "u5"}
	u3 := []string{"u1", "u3", "u5"}
	pki := newTestPKI(t, u2)

	g, err := NewGuard(threshold, pki.verifier())
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	advanceToConsistency(t, g, u2)
	confirmAndEnterDecrypt(t, g, u3, pki.signAll(t, u3, u3))

	disclosures, err := g.HandleDecryptionRequest(&DecryptionRequest{
		Dropped:  []string{"u2", "u4"},
		Survived: []string{"u5", "u1", "u3"},
	})
	if err != nil {
		t.Fatalf("decryption request: %v", err)
	}

	want := map[string]ShareKind{
		"u1": ShareSelfMask,
		"u3": ShareSelfMask,
		"u5": ShareSelfMask,
		"u2": SharePairwiseSeed,
		"u4": SharePairwiseSeed,
	}
	if len(disclosures) != len(want) {
		t.Fatalf("disclosures count = %d, want %d", len(disclosures), len(want))
	}
	for _, d := range disclosures {
		if want[d.UserID] != d.Kind {
			t.Fatalf("user %s kind = %s, want %s", d.UserID, d.Kind, want[d.UserID])
		}
	}

	// 一次会话只回应一次解密请求：重复请求必须拒绝并终止。
	_, err = g.HandleDecryptionRequest(&DecryptionRequest{
		Dropped:  []string{"u2", "u4"},
		Survived: u3,
	})
	assertErrorIs(t, err, ErrDecryptionAlreadyHandled)
	if !g.Terminated() {
		t.Fatal("session must be terminated after repeated decryption request")
	}

	// 终止后任何披露都不再可能。
	_, err = g.HandleDecryptionRequest(&DecryptionRequest{})
	assertErrorIs(t, err, ErrSessionTerminated)
}

// 攻击一：同一用户先被放进 D（成对种子），再被放进 S（自掩码）。
// 单请求内同时出现即触发集合相交；跨请求重复请求也必须 fail-closed。
func TestSameUserFirstDThenSRejected(t *testing.T) {
	const threshold = 3
	u2 := []string{"u1", "u2", "u3", "u4", "u5"}
	u3 := []string{"u1", "u3", "u5"}
	pki := newTestPKI(t, u2)

	g, err := NewGuard(threshold, pki.verifier())
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	advanceToConsistency(t, g, u2)
	confirmAndEnterDecrypt(t, g, u3, pki.signAll(t, u3, u3))

	// 恶意服务器把存活用户 u1 同时声明为掉线和存活。
	_, err = g.HandleDecryptionRequest(&DecryptionRequest{
		Dropped:  []string{"u1", "u2", "u4"},
		Survived: []string{"u1", "u3", "u5"},
	})
	assertErrorIs(t, err, ErrSetsIntersect)
	if !g.Terminated() {
		t.Fatal("session must terminate when one user lands in both D and S")
	}

	// 终止后再索要 u1 的另一种份额也不会有任何披露。
	_, err = g.HandleDecryptionRequest(&DecryptionRequest{
		Dropped:  []string{"u2", "u4"},
		Survived: u3,
	})
	assertErrorIs(t, err, ErrSessionTerminated)
}

// 攻击二：S 与已签名的 U3 不相等。
func TestSurvivedSetNotEqualU3Rejected(t *testing.T) {
	const threshold = 3
	u2 := []string{"u1", "u2", "u3", "u4", "u5"}
	u3 := []string{"u1", "u3", "u5"}
	pki := newTestPKI(t, u2)

	g, err := NewGuard(threshold, pki.verifier())
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	advanceToConsistency(t, g, u2)
	confirmAndEnterDecrypt(t, g, u3, pki.signAll(t, u3, u3))

	// 服务器把掉线的 u2 混入存活集合 S，同时漏掉 u5。
	_, err = g.HandleDecryptionRequest(&DecryptionRequest{
		Dropped:  []string{"u4", "u5"},
		Survived: []string{"u1", "u2", "u3"},
	})
	assertErrorIs(t, err, ErrSurvivedNotEqualU3)
	if !g.Terminated() {
		t.Fatal("session must terminate when S differs from signed U3")
	}
}

// 攻击三：有效签名数量不足阈值 t。
func TestInsufficientSignaturesRejected(t *testing.T) {
	const threshold = 3
	u2 := []string{"u1", "u2", "u3", "u4", "u5"}
	u3 := []string{"u1", "u3", "u5"}
	pki := newTestPKI(t, u2)

	g, err := NewGuard(threshold, pki.verifier())
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	advanceToConsistency(t, g, u2)

	sigs := pki.signAll(t, []string{"u1", "u3"}, u3) // 只有 2 个，t=3
	err = g.ConfirmAliveSet(u3, sigs)
	assertErrorIs(t, err, ErrThresholdNotMet)
	if g.Terminated() {
		t.Fatal("consistency failure must not terminate the session; retry stays possible")
	}

	// 修正为 t 个签名后应当成功，证明失败未污染状态。
	if err := g.ConfirmAliveSet(u3, pki.signAll(t, u3, u3)); err != nil {
		t.Fatalf("retry ConfirmAliveSet: %v", err)
	}
}

// 攻击四：U3 不是 U2 的子集（服务器复活或捏造用户）。
func TestU3NotSubsetOfU2Rejected(t *testing.T) {
	const threshold = 3
	u2 := []string{"u1", "u2", "u3", "u4", "u5"}
	u3 := []string{"u1", "u3", "u6"} // u6 未在掩码阶段出现
	pki := newTestPKI(t, append(append([]string(nil), u2...), "u6"))

	g, err := NewGuard(threshold, pki.verifier())
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	advanceToConsistency(t, g, u2)

	err = g.ConfirmAliveSet(u3, pki.signAll(t, u3, u3))
	assertErrorIs(t, err, ErrU3NotSubsetOfU2)
	if g.U3() != nil {
		t.Fatal("U3 must not be locked after a failed check")
	}
}

// 攻击五：重复解密请求，第二次必须拒绝并终止会话。
func TestRepeatedDecryptionRequestRejected(t *testing.T) {
	const threshold = 3
	u2 := []string{"u1", "u2", "u3", "u4"}
	u3 := []string{"u1", "u2", "u3"}
	pki := newTestPKI(t, u2)

	g, err := NewGuard(threshold, pki.verifier())
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	advanceToConsistency(t, g, u2)
	confirmAndEnterDecrypt(t, g, u3, pki.signAll(t, u3, u3))

	first, err := g.HandleDecryptionRequest(&DecryptionRequest{
		Dropped:  []string{"u4"},
		Survived: u3,
	})
	if err != nil {
		t.Fatalf("first decryption request: %v", err)
	}
	if len(first) != 4 {
		t.Fatalf("first disclosures = %d, want 4", len(first))
	}

	_, err = g.HandleDecryptionRequest(&DecryptionRequest{
		Dropped:  []string{"u4"},
		Survived: u3,
	})
	assertErrorIs(t, err, ErrDecryptionAlreadyHandled)
	if !g.Terminated() {
		t.Fatal("repeated decryption request must terminate the session")
	}
}

func TestDroppedSetMustEqualU2MinusU3(t *testing.T) {
	const threshold = 2
	u2 := []string{"u1", "u2", "u3", "u4"}
	u3 := []string{"u1", "u2"}
	pki := newTestPKI(t, u2)

	g, err := NewGuard(threshold, pki.verifier())
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	advanceToConsistency(t, g, u2)
	confirmAndEnterDecrypt(t, g, u3, pki.signAll(t, u3, u3))

	// 正确的 D 应为 {u3,u4}，服务器只报了 u3，把 u4 藏进存活集合之外。
	_, err = g.HandleDecryptionRequest(&DecryptionRequest{
		Dropped:  []string{"u3"},
		Survived: u3,
	})
	assertErrorIs(t, err, ErrDroppedNotEqualU2MinusU3)
	if !g.Terminated() {
		t.Fatal("session must terminate when D is not U2 minus U3")
	}
}

func TestSignerOutsideU3Rejected(t *testing.T) {
	const threshold = 2
	u2 := []string{"u1", "u2", "u3"}
	u3 := []string{"u1", "u2"}
	pki := newTestPKI(t, u2)

	g, err := NewGuard(threshold, pki.verifier())
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	advanceToConsistency(t, g, u2)

	sigs := []Signature{
		pki.sign(t, "u1", u3),
		pki.sign(t, "u3", u3), // u3 不在 U3 内
	}
	err = g.ConfirmAliveSet(u3, sigs)
	assertErrorIs(t, err, ErrSignerNotInU3)
}

func TestForgedAndWrongSetSignaturesRejected(t *testing.T) {
	const threshold = 2
	u2 := []string{"u1", "u2", "u3"}
	u3 := []string{"u1", "u2"}
	pki := newTestPKI(t, u2)

	t.Run("forged signature", func(t *testing.T) {
		g, err := NewGuard(threshold, pki.verifier())
		if err != nil {
			t.Fatalf("NewGuard: %v", err)
		}
		advanceToConsistency(t, g, u2)

		forged := pki.sign(t, "u1", u3)
		forged.Value = append([]byte(nil), forged.Value...)
		forged.Value[0] ^= 0xFF
		sigs := []Signature{forged, pki.sign(t, "u2", u3)}
		assertErrorIs(t, g.ConfirmAliveSet(u3, sigs), ErrBadSignature)
	})

	t.Run("signature over a different alive set", func(t *testing.T) {
		g, err := NewGuard(threshold, pki.verifier())
		if err != nil {
			t.Fatalf("NewGuard: %v", err)
		}
		advanceToConsistency(t, g, u2)

		// u1 签的是包含 u3 的旧集合，与服务器提出的 U3 不符。
		wrong := pki.sign(t, "u1", []string{"u1", "u2", "u3"})
		sigs := []Signature{wrong, pki.sign(t, "u2", u3)}
		assertErrorIs(t, g.ConfirmAliveSet(u3, sigs), ErrBadSignature)
	})
}

func TestU3SmallerThanThresholdRejected(t *testing.T) {
	const threshold = 3
	u2 := []string{"u1", "u2", "u3", "u4"}
	u3 := []string{"u1", "u2"}
	pki := newTestPKI(t, u2)

	g, err := NewGuard(threshold, pki.verifier())
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	advanceToConsistency(t, g, u2)
	assertErrorIs(t, g.ConfirmAliveSet(u3, pki.signAll(t, u3, u3)), ErrThresholdNotMet)
}

func TestPhaseMachineAndTermination(t *testing.T) {
	pki := newTestPKI(t, []string{"u1", "u2", "u3"})
	g, err := NewGuard(2, pki.verifier())
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}

	// 不能在掩码输入阶段之前记录 U2。
	assertErrorIs(t, g.RecordAliveSet([]string{"u1"}), ErrInvalidPhase)

	// 不能跳阶段。
	assertErrorIs(t, g.AdvanceTo(PhaseMaskedInput), ErrInvalidPhase)

	if err := g.AdvanceTo(PhaseShareSeeds); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if err := g.AdvanceTo(PhaseMaskedInput); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if err := g.RecordAliveSet([]string{"u1", "u2"}); err != nil {
		t.Fatalf("record U2: %v", err)
	}

	g.Terminate()
	if !g.Terminated() {
		t.Fatal("expected terminated session")
	}
	assertErrorIs(t, g.AdvanceTo(PhaseConsistencySign), ErrSessionTerminated)
	assertErrorIs(t, g.RecordAliveSet([]string{"u1"}), ErrSessionTerminated)
	g.Terminate() // 幂等
}

func TestDecryptionRequiresConfirmedU3(t *testing.T) {
	pki := newTestPKI(t, []string{"u1", "u2", "u3"})
	g, err := NewGuard(2, pki.verifier())
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	advanceToConsistency(t, g, []string{"u1", "u2", "u3"})

	// 未确认 U3 直接进入解密阶段，任何请求都必须 fail-closed。
	if err := g.AdvanceTo(PhaseDecrypt); err != nil {
		t.Fatalf("advance to decrypt: %v", err)
	}
	_, err = g.HandleDecryptionRequest(&DecryptionRequest{Survived: []string{"u1", "u2"}})
	assertErrorIs(t, err, ErrU3NotConfirmed)
	if !g.Terminated() {
		t.Fatal("missing confirmed U3 must terminate the session")
	}
}

func TestNewGuardRejectsBadInputs(t *testing.T) {
	if _, err := NewGuard(0, func(string, []byte, []byte) bool { return true }); !errors.Is(err, ErrThresholdNotMet) {
		t.Fatalf("threshold 0: want %v, got %v", ErrThresholdNotMet, err)
	}
	if _, err := NewGuard(2, nil); err == nil {
		t.Fatal("nil verifier must be rejected")
	}
}

func TestNilDecryptionRequestDoesNotConsumeAttempt(t *testing.T) {
	const threshold = 2
	u2 := []string{"u1", "u2", "u3"}
	u3 := []string{"u1", "u2"}
	pki := newTestPKI(t, u2)

	g, err := NewGuard(threshold, pki.verifier())
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	advanceToConsistency(t, g, u2)
	confirmAndEnterDecrypt(t, g, u3, pki.signAll(t, u3, u3))

	_, err = g.HandleDecryptionRequest(nil)
	assertErrorIs(t, err, ErrNilDecryptionRequest)
	if g.Terminated() {
		t.Fatal("nil request is a caller bug and must not terminate the session")
	}

	// 唯一一次解密机会仍然可用。
	if _, err := g.HandleDecryptionRequest(&DecryptionRequest{
		Dropped:  []string{"u3"},
		Survived: u3,
	}); err != nil {
		t.Fatalf("valid request after nil: %v", err)
	}
}

// 白盒验证跨请求互斥防线：守卫内部已记录某种份额后，
// 同一用户索要另一种份额必须拒绝并终止（两个方向各测一次）。
func TestCrossRequestShareKindConflict(t *testing.T) {
	const threshold = 2
	u2 := []string{"u1", "u2", "u3"}
	u3 := []string{"u1", "u2"}

	cases := []struct {
		name       string
		preload    map[string]ShareKind
		req        *DecryptionRequest
		wantTarget error
	}{
		{
			name:    "pairwise seed already disclosed, self mask requested",
			preload: map[string]ShareKind{"u1": SharePairwiseSeed},
			req: &DecryptionRequest{
				Dropped:  []string{"u3"},
				Survived: u3,
			},
			wantTarget: ErrShareKindConflict,
		},
		{
			name:    "self mask already disclosed, pairwise seed requested",
			preload: map[string]ShareKind{"u3": ShareSelfMask},
			req: &DecryptionRequest{
				Dropped:  []string{"u3"},
				Survived: u3,
			},
			wantTarget: ErrShareKindConflict,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pki := newTestPKI(t, u2)
			g, err := NewGuard(threshold, pki.verifier())
			if err != nil {
				t.Fatalf("NewGuard: %v", err)
			}
			advanceToConsistency(t, g, u2)
			confirmAndEnterDecrypt(t, g, u3, pki.signAll(t, u3, u3))

			g.mu.Lock()
			for id, kind := range tc.preload {
				g.disclosed[id] = kind
			}
			g.mu.Unlock()

			_, err = g.HandleDecryptionRequest(tc.req)
			assertErrorIs(t, err, tc.wantTarget)
			if !g.Terminated() {
				t.Fatal("conflicting share kind request must terminate the session")
			}
			_, err = g.HandleDecryptionRequest(tc.req)
			assertErrorIs(t, err, ErrSessionTerminated)
		})
	}
}

func TestCanonicalAliveSetMessageIsOrderIndependent(t *testing.T) {
	a := CanonicalAliveSetMessage([]string{"u3", "u1", "u2"})
	b := CanonicalAliveSetMessage([]string{"u2", "u3", "u1"})
	if string(a) != string(b) {
		t.Fatalf("canonical messages differ: %q vs %q", a, b)
	}
}

func TestConcurrentAccessIsSafe(t *testing.T) {
	const threshold = 2
	u2 := []string{"u1", "u2", "u3"}
	u3 := []string{"u1", "u2"}
	pki := newTestPKI(t, u2)

	g, err := NewGuard(threshold, pki.verifier())
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	advanceToConsistency(t, g, u2)
	if err := g.ConfirmAliveSet(u3, pki.signAll(t, u3, u3)); err != nil {
		t.Fatalf("confirm U3: %v", err)
	}
	if err := g.AdvanceTo(PhaseDecrypt); err != nil {
		t.Fatalf("advance to decrypt: %v", err)
	}

	done := make(chan struct{})
	go func() {
		for range 100 {
			_ = g.Phase()
			_ = g.Terminated()
			_ = g.U2()
			_ = g.U3()
		}
		close(done)
	}()

	req := &DecryptionRequest{Dropped: []string{"u3"}, Survived: u3}
	successes := 0
	for range 100 {
		if _, err := g.HandleDecryptionRequest(req); err == nil {
			successes++
		}
	}
	<-done

	if successes != 1 {
		t.Fatalf("exactly one decryption request may succeed, got %d", successes)
	}
	if !g.Terminated() {
		t.Fatal("session must be terminated after the request storm")
	}
}
