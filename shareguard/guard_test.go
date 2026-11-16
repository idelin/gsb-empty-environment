package shareguard

import (
	"errors"
	"fmt"
	"testing"
)

const testThreshold = 3

// newTestVerifier 为 a..e 五个客户端生成独立 HMAC 密钥。
func newTestVerifier(t *testing.T) *HMACVerifier {
	t.Helper()
	keys := make(map[string][]byte)
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		keys[id] = []byte("hmac-key-" + id)
	}
	return NewHMACVerifier(keys)
}

// signU3 让 signers 中的每个客户端对 u3 产出有效签名。
func signU3(t *testing.T, v *HMACVerifier, u3 []string, signers []string) []Signature {
	t.Helper()
	sigs := make([]Signature, 0, len(signers))
	for _, signer := range signers {
		tag, err := v.SignAliveSet(signer, u3)
		if err != nil {
			t.Fatalf("sign for %s: %v", signer, err)
		}
		sigs = append(sigs, Signature{Signer: signer, Value: tag})
	}
	return sigs
}

// newGuardAtConsistency 构建一个已经推进到一致性阶段、并记录好 U2 的守卫。
func newGuardAtConsistency(t *testing.T, u2 []string) (*Guard, *HMACVerifier) {
	t.Helper()
	v := newTestVerifier(t)
	g, err := NewGuard(testThreshold, v)
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	for _, phase := range []Phase{PhaseShare, PhaseMaskedInput} {
		if err := g.AdvanceTo(phase); err != nil {
			t.Fatalf("AdvanceTo %s: %v", phase, err)
		}
	}
	for _, id := range u2 {
		if err := g.ObserveParticipant(id); err != nil {
			t.Fatalf("ObserveParticipant %s: %v", id, err)
		}
	}
	if err := g.AdvanceTo(PhaseConsistency); err != nil {
		t.Fatalf("AdvanceTo consistency: %v", err)
	}
	return g, v
}

// newAcceptedGuard 构建一致性校验已通过、停在解密阶段的守卫，
// 默认 U2={a,b,c,d}，U3={a,b,c}，D={d}。
func newAcceptedGuard(t *testing.T) (*Guard, *HMACVerifier) {
	t.Helper()
	u2 := []string{"a", "b", "c", "d"}
	u3 := []string{"a", "b", "c"}
	g, v := newGuardAtConsistency(t, u2)
	sigs := signU3(t, v, u3, u3)
	if err := g.AcceptConsistencyStatement(u3, sigs); err != nil {
		t.Fatalf("AcceptConsistencyStatement: %v", err)
	}
	if err := g.AdvanceTo(PhaseDecryption); err != nil {
		t.Fatalf("AdvanceTo decryption: %v", err)
	}
	return g, v
}

func assertErrIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("expected error wrapping %v, got %v", target, err)
	}
}

func assertNilErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// assertTerminated 验证 err 已终止会话，且 TerminationReason 中保留目标根因。
func assertTerminated(t *testing.T, g *Guard, err error, rootCause error) {
	t.Helper()
	assertErrIs(t, err, ErrSessionTerminated)
	assertErrIs(t, g.TerminationReason(), rootCause)
}

// TestHappyPath 验证正常会话：D 披露成对份额、S 披露自掩码份额，
// 集合与签名全部合法，且同类型重复披露幂等。
func TestHappyPath(t *testing.T) {
	g, _ := newAcceptedGuard(t)

	assertNilErr(t, g.AcceptDecryptionRequest([]string{"d"}, []string{"a", "b", "c"}))

	assertNilErr(t, g.DisclosePairwise("d"))
	// 网络重试时再次请求同一类份额应当幂等放行。
	assertNilErr(t, g.DisclosePairwise("d"))

	for _, id := range []string{"a", "b", "c"} {
		assertNilErr(t, g.DiscloseSelfMask(id))
	}

	if got := g.DisclosedShareKind("d"); got != kindPairwise.String() {
		t.Fatalf("d disclosed kind = %q, want %q", got, kindPairwise)
	}
	if got := g.DisclosedShareKind("a"); got != kindSelfMask.String() {
		t.Fatalf("a disclosed kind = %q, want %q", got, kindSelfMask)
	}
	if g.Terminated() {
		t.Fatal("session should remain alive after a well-formed request")
	}
	if got := g.DroppedSet(); len(got) != 1 || got[0] != "d" {
		t.Fatalf("DroppedSet = %v, want [d]", got)
	}
	if got := g.SurvivingSet(); fmt.Sprint(got) != "[a b c]" {
		t.Fatalf("SurvivingSet = %v, want [a b c]", got)
	}
}

// TestDoubleDisclosure_DroppedThenSurviving 复现攻击：服务器试图对同一用户
// 先索取成对种子份额、再索取自掩码份额。跨类型请求必须被拒绝且会话立即
// 终止，此后任何份额都无法再披露。
func TestDoubleDisclosure_DroppedThenSurviving(t *testing.T) {
	t.Run("dropped user then claimed surviving", func(t *testing.T) {
		g, _ := newAcceptedGuard(t)
		assertNilErr(t, g.AcceptDecryptionRequest([]string{"d"}, []string{"a", "b", "c"}))

		// 服务器先谎称 d 掉线，拿到成对种子份额。
		assertNilErr(t, g.DisclosePairwise("d"))
		// 再谎称 d 在线索取自掩码份额：互斥逻辑优先拦截并终止会话。
		err := g.DiscloseSelfMask("d")
		assertTerminated(t, g, err, ErrDoubleDisclosure)

		// 终止后任何份额披露都必须被拒绝，即使是此前合法的请求。
		assertErrIs(t, g.DiscloseSelfMask("b"), ErrSessionTerminated)
		assertErrIs(t, g.DisclosePairwise("d"), ErrSessionTerminated)
	})

	t.Run("surviving user then claimed dropped", func(t *testing.T) {
		g, _ := newAcceptedGuard(t)
		assertNilErr(t, g.AcceptDecryptionRequest([]string{"d"}, []string{"a", "b", "c"}))

		// 存活用户 a 先披露自掩码份额，再被索要成对种子份额。
		assertNilErr(t, g.DiscloseSelfMask("a"))
		err := g.DisclosePairwise("a")
		assertTerminated(t, g, err, ErrDoubleDisclosure)
		if !g.Terminated() {
			t.Fatal("session must be terminated after double disclosure attempt")
		}
	})
}

// TestSurvivingSetNotEqualToU3 验证 S 与已签名 U3 不相等时拒绝并终止。
func TestSurvivingSetNotEqualToU3(t *testing.T) {
	g, _ := newAcceptedGuard(t)

	// 服务器试图把掉线用户 d 塞进存活集合 S。
	err := g.AcceptDecryptionRequest(nil, []string{"a", "b", "c", "d"})
	assertTerminated(t, g, err, ErrSurvivingSetMismatch)
	if !g.Terminated() {
		t.Fatal("session must terminate when S != U3")
	}

	// 终止后即使补发正确请求也不会被接受。
	err = g.AcceptDecryptionRequest([]string{"d"}, []string{"a", "b", "c"})
	assertErrIs(t, err, ErrSessionTerminated)
}

// TestDroppedSetMismatch 验证 D 不等于 U2\U3 时拒绝并终止。
func TestDroppedSetMismatch(t *testing.T) {
	g, _ := newAcceptedGuard(t)

	// U2\U3 只有 d，服务器却声称没有掉线用户。
	err := g.AcceptDecryptionRequest(nil, []string{"a", "b", "c"})
	assertTerminated(t, g, err, ErrDroppedSetMismatch)
}

// TestConsistency_NotEnoughSignatures 验证有效签名数小于 t 时一致性校验失败。
func TestConsistency_NotEnoughSignatures(t *testing.T) {
	u3 := []string{"a", "b", "c"}
	g, v := newGuardAtConsistency(t, []string{"a", "b", "c", "d"})

	// 只有 2 个有效签名，t=3。
	sigs := signU3(t, v, u3, []string{"a", "b"})
	err := g.AcceptConsistencyStatement(u3, sigs)
	assertErrIs(t, err, ErrBelowThreshold)

	// 重复签名去重后仍只有 2 个签名者，不能凑数。
	sigs = append(sigs, sigs[0])
	err = g.AcceptConsistencyStatement(u3, sigs)
	assertErrIs(t, err, ErrBelowThreshold)

	// 伪造一条 c 的签名（用 b 的密钥标签冒充）。
	forged := Signature{Signer: "c", Value: sigs[0].Value}
	sigs = append(signU3(t, v, u3, []string{"a", "b"}), forged)
	err = g.AcceptConsistencyStatement(u3, sigs)
	assertErrIs(t, err, ErrBadSignature)

	if g.SignedAliveSet() != nil {
		t.Fatal("U3 must not be locked after failed consistency checks")
	}

	// 一致性失败不终止会话，修正后仍可通过。
	good := signU3(t, v, u3, u3)
	assertNilErr(t, g.AcceptConsistencyStatement(u3, good))
	if got := g.SignedAliveSet(); fmt.Sprint(got) != "[a b c]" {
		t.Fatalf("SignedAliveSet = %v, want [a b c]", got)
	}
}

// TestConsistency_U3NotSubsetOfU2 验证 U3 含有掩码阶段未见到的用户时被拒绝。
func TestConsistency_U3NotSubsetOfU2(t *testing.T) {
	g, v := newGuardAtConsistency(t, []string{"a", "b", "c"})

	// e 从未在掩码输入阶段出现，服务器却把它放进 U3。
	u3 := []string{"a", "b", "e"}
	sigs := signU3(t, v, u3, u3)
	err := g.AcceptConsistencyStatement(u3, sigs)
	assertErrIs(t, err, ErrAliveSetNotSubset)

	// 签名者不在 U3 内同样非法。
	u3 = []string{"a", "b", "c"}
	bad := signU3(t, v, u3, []string{"a", "b", "c"})
	dTag, err := v.SignAliveSet("d", u3)
	if err != nil {
		t.Fatalf("sign d: %v", err)
	}
	bad = append(bad, Signature{Signer: "d", Value: dTag})
	err = g.AcceptConsistencyStatement(u3, bad)
	assertErrIs(t, err, ErrSignerOutsideAliveSet)
}

// TestConsistency_U3BelowThreshold 验证 |U3| < t 时被拒绝。
func TestConsistency_U3BelowThreshold(t *testing.T) {
	u3 := []string{"a", "b"}
	g, v := newGuardAtConsistency(t, []string{"a", "b", "c", "d"})
	sigs := signU3(t, v, u3, u3)
	err := g.AcceptConsistencyStatement(u3, sigs)
	assertErrIs(t, err, ErrBelowThreshold)
}

// TestDuplicateDecryptionRequest 验证一次会话只回应一次解密请求：
// 第二次请求（哪怕内容相同）必须拒绝并终止。
func TestDuplicateDecryptionRequest(t *testing.T) {
	g, _ := newAcceptedGuard(t)
	req := func() error {
		return g.AcceptDecryptionRequest([]string{"d"}, []string{"a", "b", "c"})
	}
	assertNilErr(t, req())

	err := req()
	assertTerminated(t, g, err, ErrDecryptionAlreadyRequested)
	if !g.Terminated() {
		t.Fatal("session must terminate on the second decryption request")
	}
}

// TestDisclosureBeforeRequestRejected 验证未接受解密请求前不能披露任何份额。
func TestDisclosureBeforeRequestRejected(t *testing.T) {
	g, _ := newAcceptedGuard(t)

	err := g.DisclosePairwise("d")
	assertTerminated(t, g, err, ErrInvalidPhase)
	if !g.Terminated() {
		t.Fatal("disclosure without an accepted request must terminate the session")
	}
}

// TestManualTerminateBlocksEverything 验证手动终止后所有写操作均被拒绝。
func TestManualTerminateBlocksEverything(t *testing.T) {
	g, _ := newAcceptedGuard(t)
	g.Terminate(errors.New("network anomaly"))

	if !g.Terminated() {
		t.Fatal("Terminated should be true after Terminate")
	}
	assertErrIs(t, g.AcceptDecryptionRequest(nil, nil), ErrSessionTerminated)
	assertErrIs(t, g.DiscloseSelfMask("a"), ErrSessionTerminated)

	// 重复 Terminate 不覆盖首次原因。
	g.Terminate(errors.New("second reason"))
	if g.TerminationReason().Error() != "network anomaly" {
		t.Fatalf("termination reason overwritten: %v", g.TerminationReason())
	}
}

// TestPhaseEnforcement 验证阶段只能按顺序单步推进，且 U2 只能在掩码阶段记录。
func TestPhaseEnforcement(t *testing.T) {
	v := newTestVerifier(t)
	g, err := NewGuard(testThreshold, v)
	assertNilErr(t, err)

	// 不能跳阶段。
	assertErrIs(t, g.AdvanceTo(PhaseMaskedInput), ErrInvalidPhase)
	// 还没进入掩码阶段不能记录 U2。
	assertErrIs(t, g.ObserveParticipant("a"), ErrInvalidPhase)

	assertNilErr(t, g.AdvanceTo(PhaseShare))
	assertErrIs(t, g.ObserveParticipant("a"), ErrInvalidPhase)
	assertNilErr(t, g.AdvanceTo(PhaseMaskedInput))
	assertNilErr(t, g.ObserveParticipant("a"))
	assertNilErr(t, g.ObserveParticipant("a")) // 幂等

	if got := g.ObservedAliveSet(); len(got) != 1 || got[0] != "a" {
		t.Fatalf("ObservedAliveSet = %v, want [a]", got)
	}

	// 一致性阶段不能再补记 U2。
	assertNilErr(t, g.AdvanceTo(PhaseConsistency))
	assertErrIs(t, g.ObserveParticipant("b"), ErrInvalidPhase)
}

// TestNewGuardValidation 验证构造参数校验。
func TestNewGuardValidation(t *testing.T) {
	v := newTestVerifier(t)
	if _, err := NewGuard(0, v); !errors.Is(err, ErrInvalidThreshold) {
		t.Fatalf("threshold 0: got %v, want ErrInvalidThreshold", err)
	}
	if _, err := NewGuard(testThreshold, nil); err == nil {
		t.Fatal("nil verifier must be rejected")
	}
}

// TestCanonicalAliveSet 验证规范序列化的确定性。
func TestCanonicalAliveSet(t *testing.T) {
	got := string(CanonicalAliveSet([]string{"c", "a", "b"}))
	want := "a,b,c"
	if got != want {
		t.Fatalf("canonical encoding = %q, want %q", got, want)
	}
}

// TestGettersAndEdgeCases 覆盖简单 getter 与若干防御分支。
func TestGettersAndEdgeCases(t *testing.T) {
	v := newTestVerifier(t)
	g, err := NewGuard(testThreshold, v)
	assertNilErr(t, err)

	if g.Phase() != PhaseBroadcastKey {
		t.Fatalf("initial phase = %v", g.Phase())
	}
	if g.Threshold() != testThreshold {
		t.Fatalf("threshold = %d", g.Threshold())
	}
	if g.Terminated() || g.TerminationReason() != nil {
		t.Fatal("fresh guard must be alive")
	}
	if got := g.ObservedAliveSet(); len(got) != 0 {
		t.Fatalf("U2 should start empty, got %v", got)
	}
	if g.SignedAliveSet() != nil || g.DroppedSet() != nil || g.SurvivingSet() != nil {
		t.Fatal("locked/request sets should be nil before use")
	}
	if g.DisclosedShareKind("nobody") != "" {
		t.Fatal("no share should be recorded initially")
	}
	if Phase(99).String() == "" {
		t.Fatal("unknown phase should still render")
	}

	// 一致性阶段空 U3、以及通过后重复加锁都应报错。
	assertNilErr(t, g.AdvanceTo(PhaseShare))
	assertNilErr(t, g.AdvanceTo(PhaseMaskedInput))
	assertNilErr(t, g.ObserveParticipant("a"))
	assertNilErr(t, g.ObserveParticipant("b"))
	assertNilErr(t, g.ObserveParticipant("c"))
	assertNilErr(t, g.AdvanceTo(PhaseConsistency))

	assertErrIs(t, g.AcceptConsistencyStatement(nil, nil), ErrEmptyAliveSet)

	u3 := []string{"a", "b", "c"}
	sigs := signU3(t, v, u3, u3)
	assertNilErr(t, g.AcceptConsistencyStatement(u3, sigs))
	assertErrIs(t, g.AcceptConsistencyStatement(u3, sigs), ErrInvalidPhase)

	// 解密请求接受前，D/S getter 返回 nil；接受后返回有序集合。
	assertNilErr(t, g.AdvanceTo(PhaseDecryption))
	if g.DroppedSet() != nil || g.SurvivingSet() != nil {
		t.Fatal("D/S must be nil before the request is accepted")
	}
	assertNilErr(t, g.AcceptDecryptionRequest(nil, u3))
	if got := g.DroppedSet(); len(got) != 0 {
		t.Fatalf("empty D should be an empty non-nil-less slice, got %v", got)
	}
	if got := g.SurvivingSet(); fmt.Sprint(got) != "[a b c]" {
		t.Fatalf("S = %v", got)
	}
}

// TestHMACVerifierErrors 覆盖 HMAC 验证器的未知签名者与错误标签分支。
func TestHMACVerifierErrors(t *testing.T) {
	v := newTestVerifier(t)
	u3 := []string{"a", "b", "c"}

	if _, err := v.SignAliveSet("unknown", u3); err == nil {
		t.Fatal("signing with an unregistered signer must fail")
	}
	if err := v.VerifyAliveSet("unknown", u3, []byte("tag")); err == nil {
		t.Fatal("verifying an unregistered signer must fail")
	}
	tag, err := v.SignAliveSet("a", u3)
	assertNilErr(t, err)
	if err := v.VerifyAliveSet("a", u3, append(tag, 0x01)); err == nil {
		t.Fatal("a tampered tag must fail verification")
	}
	assertNilErr(t, v.VerifyAliveSet("a", u3, tag))
}

// TestDisjointDefense 验证 D∩S 非空的防御分支（即使绕过相等性检查也会拦截）。
func TestDisjointDefense(t *testing.T) {
	// 直接构造内部状态，模拟 S=U3 但 D 与 S 相交的畸形请求路径：
	// 正常流程下集合相等性检查会先命中，这里验证 intersects 工具自身。
	a := map[string]struct{}{"x": {}, "y": {}}
	b := map[string]struct{}{"y": {}, "z": {}}
	if !intersects(a, b) {
		t.Fatal("intersects must detect the shared element")
	}
	c := map[string]struct{}{"p": {}}
	if intersects(a, c) {
		t.Fatal("disjoint sets must not intersect")
	}
	if !setEquals(setDifference(a, c), a) {
		t.Fatal("difference with a disjoint set should equal the left operand")
	}
}

// TestTerminateNilReason 验证 nil 原因也能安全终止会话。
func TestTerminateNilReason(t *testing.T) {
	g, _ := newAcceptedGuard(t)
	g.Terminate(nil)
	if !g.Terminated() || g.TerminationReason() == nil {
		t.Fatal("nil reason must be replaced by a default termination reason")
	}
}
