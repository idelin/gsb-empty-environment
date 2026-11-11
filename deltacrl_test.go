package deltacrl

import (
	"crypto/x509"
	"errors"
	"math/big"
	"testing"
	"time"
)

const dpURL = "http://ca.example/crl/main.crl"

// 固定时钟与 CRL 有效期，保证测试可重复。
var (
	now0        = time.Date(2026, 11, 11, 8, 0, 0, 0, time.UTC)
	crlThis     = now0.Add(-30 * time.Minute)
	crlNext     = now0.Add(24 * time.Hour)
	expiredNext = now0.Add(-time.Minute)
	revokedAt   = now0.Add(-2 * time.Hour)
)

func newCheckerAt(t *testing.T, ca *testCA) *Checker {
	t.Helper()
	return NewChecker(ca.cert).WithClock(func() time.Time { return now0 })
}

func mustIngest(t *testing.T, c *Checker, der []byte) {
	t.Helper()
	if err := c.IngestDER(der); err != nil {
		t.Fatalf("IngestDER failed: %v", err)
	}
}

func assertErrIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("expected error %v, got %v", target, err)
	}
}

func assertStatus(t *testing.T, c *Checker, serial int64, want Status) {
	t.Helper()
	got := c.Check(big.NewInt(serial))
	if got.Status != want {
		t.Fatalf("serial %d: expected status %s, got %s", serial, want, got.Status)
	}
}

func baseSpec(number int64, entries ...x509.RevocationListEntry) crlSpec {
	return crlSpec{
		number:     number,
		thisUpdate: crlThis,
		nextUpdate: crlNext,
		entries:    entries,
		dpURL:      dpURL,
	}
}

func deltaSpec(number, baseRef int64, entries ...x509.RevocationListEntry) crlSpec {
	s := baseSpec(number, entries...)
	s.deltaBase = baseRef
	return s
}

// TestMergeAndHoldRelease 覆盖基础+增量合并、certificateHold 暂停与
// removeFromCRL 解除暂停的完整链路。
func TestMergeAndHoldRelease(t *testing.T) {
	ca, err := newTestCA("Test Merge CA")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []int64{101, 102, 103} {
		if _, err := ca.leaf(s); err != nil {
			t.Fatal(err)
		}
	}

	// 基础 CRL #10：101 永久吊销，102 证书暂停。
	base1, err := ca.signCRL(baseSpec(10,
		revokedEntry(101, ReasonKeyCompromise, revokedAt),
		revokedEntry(102, ReasonCertificateHold, revokedAt),
	))
	if err != nil {
		t.Fatal(err)
	}
	// 增量 #11（引用基础 #10）：解除 102 的暂停，追加吊销 103。
	delta11, err := ca.signCRL(deltaSpec(11, 10,
		revokedEntry(102, ReasonRemoveFromCRL, now0),
		revokedEntry(103, ReasonCessationOfOp, now0),
	))
	if err != nil {
		t.Fatal(err)
	}

	c := newCheckerAt(t, ca)
	mustIngest(t, c, base1)
	assertStatus(t, c, 101, Revoked)
	assertStatus(t, c, 102, Revoked)
	assertStatus(t, c, 103, Good)

	mustIngest(t, c, delta11)
	if c.DeltaCount() != 1 {
		t.Fatalf("expected 1 accepted delta, got %d", c.DeltaCount())
	}
	assertStatus(t, c, 101, Revoked)
	assertStatus(t, c, 102, Good) // 暂停被 removeFromCRL 解除
	assertStatus(t, c, 103, Revoked)

	r := c.Check(big.NewInt(103))
	if r.Reason != ReasonCessationOfOp || !r.RevokedAt.Equal(now0) {
		t.Fatalf("unexpected merged result for 103: %+v", r)
	}
}

// TestOutOfOrderArrival 核对增量乱序到达：无论接入顺序如何，视图按
// cRLNumber 语义合并，结果一致。
func TestOutOfOrderArrival(t *testing.T) {
	ca, err := newTestCA("Test OOO CA")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []int64{201, 202, 203} {
		if _, err := ca.leaf(s); err != nil {
			t.Fatal(err)
		}
	}

	base1, err := ca.signCRL(baseSpec(100, revokedEntry(201, ReasonKeyCompromise, revokedAt)))
	if err != nil {
		t.Fatal(err)
	}
	delta101, err := ca.signCRL(deltaSpec(101, 100,
		revokedEntry(202, ReasonCertificateHold, now0),
		revokedEntry(203, ReasonCertificateHold, now0),
	))
	if err != nil {
		t.Fatal(err)
	}
	delta102, err := ca.signCRL(deltaSpec(102, 100,
		revokedEntry(203, ReasonRemoveFromCRL, now0),
	))
	if err != nil {
		t.Fatal(err)
	}

	c := newCheckerAt(t, ca)
	mustIngest(t, c, base1)
	// 先到 #102 再到 #101，到达顺序与序号顺序相反。
	mustIngest(t, c, delta102)
	// 此时 203 的暂停（#101）还没到达，解除操作找不到目标而暂不生效，
	// 但组件不能报错——增量已按序号就位，等待缺失的前置增量补齐。
	assertStatus(t, c, 203, Good)
	mustIngest(t, c, delta101)

	if c.DeltaCount() != 2 {
		t.Fatalf("expected 2 deltas, got %d", c.DeltaCount())
	}
	assertStatus(t, c, 201, Revoked)
	assertStatus(t, c, 202, Revoked) // #101 暂停，无后续解除
	assertStatus(t, c, 203, Good)    // #101 暂停、#102 解除；乱序到达也按序号语义合并

	// 重放 #101 必须被识别为重号而拒绝。
	assertErrIs(t, c.IngestDER(delta101), ErrStaleCRL)
}

// TestPendingDeltaBeforeBase 核对增量先于基础到达时暂存，基础到达后自动激活。
func TestPendingDeltaBeforeBase(t *testing.T) {
	ca, err := newTestCA("Test Pending CA")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ca.leaf(301); err != nil {
		t.Fatal(err)
	}
	base1, err := ca.signCRL(baseSpec(20))
	if err != nil {
		t.Fatal(err)
	}
	delta21, err := ca.signCRL(deltaSpec(21, 20, revokedEntry(301, ReasonKeyCompromise, now0)))
	if err != nil {
		t.Fatal(err)
	}

	c := newCheckerAt(t, ca)
	assertErrIs(t, c.IngestDER(delta21), ErrNoBaseCRL)
	if c.PendingCount() != 1 {
		t.Fatalf("expected 1 pending delta, got %d", c.PendingCount())
	}
	assertStatus(t, c, 301, Unknown) // 没有基础 CRL，不能给结论

	mustIngest(t, c, base1)
	if c.PendingCount() != 0 || c.DeltaCount() != 1 {
		t.Fatalf("pending=%d deltas=%d, want pending=0 deltas=1", c.PendingCount(), c.DeltaCount())
	}
	assertStatus(t, c, 301, Revoked)
}

// TestStaleDeltaAfterBaseRefresh 核对基础 CRL 日更后，引用旧基础的增量被淘汰。
func TestStaleDeltaAfterBaseRefresh(t *testing.T) {
	ca, err := newTestCA("Test Refresh CA")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []int64{401, 402} {
		if _, err := ca.leaf(s); err != nil {
			t.Fatal(err)
		}
	}

	base10, err := ca.signCRL(baseSpec(10))
	if err != nil {
		t.Fatal(err)
	}
	delta11, err := ca.signCRL(deltaSpec(11, 10, revokedEntry(401, ReasonCertificateHold, now0)))
	if err != nil {
		t.Fatal(err)
	}
	// 新一天的完整 CRL #20 已纳入全部既有吊销；旧增量 #11 的序号被覆盖。
	base20, err := ca.signCRL(baseSpec(20,
		revokedEntry(401, ReasonKeyCompromise, revokedAt),
		revokedEntry(402, ReasonCessationOfOp, revokedAt),
	))
	if err != nil {
		t.Fatal(err)
	}

	c := newCheckerAt(t, ca)
	mustIngest(t, c, base10)
	mustIngest(t, c, delta11)
	if c.DeltaCount() != 1 {
		t.Fatalf("expected 1 delta before refresh, got %d", c.DeltaCount())
	}

	mustIngest(t, c, base20)
	if c.DeltaCount() != 0 {
		t.Fatalf("expected stale delta dropped, got %d", c.DeltaCount())
	}
	assertStatus(t, c, 401, Revoked)
	assertStatus(t, c, 402, Revoked)

	// 基础更新后重放旧增量 #11：序号已不大于当前基础，直接拒绝。
	assertErrIs(t, c.IngestDER(delta11), ErrStaleCRL)
	// 旧基础 #10 重放同样被拒绝。
	assertErrIs(t, c.IngestDER(base10), ErrStaleCRL)
}

// TestFutureBaseReference 核对增量引用的 baseCRLNumber 晚于当前基础时拒绝。
func TestFutureBaseReference(t *testing.T) {
	ca, err := newTestCA("Test Future Base CA")
	if err != nil {
		t.Fatal(err)
	}
	base10, err := ca.signCRL(baseSpec(10))
	if err != nil {
		t.Fatal(err)
	}
	// 增量自称基于尚不存在的基础 #99。
	delta11, err := ca.signCRL(deltaSpec(11, 99))
	if err != nil {
		t.Fatal(err)
	}

	c := newCheckerAt(t, ca)
	mustIngest(t, c, base10)
	assertErrIs(t, c.IngestDER(delta11), ErrFutureBaseReference)
	if c.DeltaCount() != 0 {
		t.Fatalf("rejected delta must not be stored, got %d", c.DeltaCount())
	}
}

// TestExpiryUnknown 核对任一份 CRL 过期都判状态未知。
func TestExpiryUnknown(t *testing.T) {
	ca, err := newTestCA("Test Expiry CA")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ca.leaf(501); err != nil {
		t.Fatal(err)
	}

	t.Run("expired base CRL", func(t *testing.T) {
		spec := baseSpec(10, revokedEntry(501, ReasonKeyCompromise, revokedAt))
		spec.nextUpdate = expiredNext
		der, err := ca.signCRL(spec)
		if err != nil {
			t.Fatal(err)
		}
		c := newCheckerAt(t, ca)
		mustIngest(t, c, der) // 过期 CRL 仍可接入，查询时才判 Unknown
		assertStatus(t, c, 501, Unknown)
	})

	t.Run("expired delta CRL", func(t *testing.T) {
		base10, err := ca.signCRL(baseSpec(10))
		if err != nil {
			t.Fatal(err)
		}
		dspec := deltaSpec(11, 10, revokedEntry(501, ReasonKeyCompromise, now0))
		dspec.nextUpdate = expiredNext
		delta11, err := ca.signCRL(dspec)
		if err != nil {
			t.Fatal(err)
		}
		c := newCheckerAt(t, ca)
		mustIngest(t, c, base10)
		mustIngest(t, c, delta11)
		assertStatus(t, c, 501, Unknown)
	})
}

// TestInvalidRemoveFromCRL 核对 removeFromCRL 只能解除 certificateHold。
func TestInvalidRemoveFromCRL(t *testing.T) {
	ca, err := newTestCA("Test Invalid Remove CA")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ca.leaf(601); err != nil {
		t.Fatal(err)
	}
	// 基础中 601 以 keyCompromise 永久吊销，并非暂停。
	base10, err := ca.signCRL(baseSpec(10, revokedEntry(601, ReasonKeyCompromise, revokedAt)))
	if err != nil {
		t.Fatal(err)
	}
	delta11, err := ca.signCRL(deltaSpec(11, 10, revokedEntry(601, ReasonRemoveFromCRL, now0)))
	if err != nil {
		t.Fatal(err)
	}

	c := newCheckerAt(t, ca)
	mustIngest(t, c, base10)
	assertErrIs(t, c.IngestDER(delta11), ErrInvalidRemoveFromCRL)
	if c.DeltaCount() != 0 {
		t.Fatalf("invalid delta must not be accepted, got %d", c.DeltaCount())
	}
	// 原有视图不受失败接入影响。
	assertStatus(t, c, 601, Revoked)
}

// TestTamperedCRL 核对签名校验：篡改后的 CRL 必须被拒绝。
func TestTamperedCRL(t *testing.T) {
	ca, err := newTestCA("Test Tamper CA")
	if err != nil {
		t.Fatal(err)
	}
	base10, err := ca.signCRL(baseSpec(10))
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), base10...)
	// 翻转签名区附近的一个字节，破坏签名而不破坏 DER 结构的概率足够高；
	// 即使 DER 先损坏，IngestDER 也同样必须返回错误。
	tampered[len(tampered)-3] ^= 0xFF

	c := newCheckerAt(t, ca)
	if err := c.IngestDER(tampered); err == nil {
		t.Fatal("expected tampered CRL to be rejected")
	}
}

// TestScopeAndIssuer 核对分发点范围与签发者一致性校验。
func TestScopeAndIssuer(t *testing.T) {
	ca, err := newTestCA("Test Scope CA")
	if err != nil {
		t.Fatal(err)
	}
	base10, err := ca.signCRL(baseSpec(10))
	if err != nil {
		t.Fatal(err)
	}

	c := newCheckerAt(t, ca)
	mustIngest(t, c, base10)

	t.Run("different distribution point", func(t *testing.T) {
		spec := deltaSpec(11, 10)
		spec.dpURL = "http://ca.example/crl/other.crl"
		der, err := ca.signCRL(spec)
		if err != nil {
			t.Fatal(err)
		}
		assertErrIs(t, c.IngestDER(der), ErrScopeMismatch)
	})

	t.Run("base has IDP but delta has none", func(t *testing.T) {
		spec := deltaSpec(11, 10)
		spec.noIDP = true
		der, err := ca.signCRL(spec)
		if err != nil {
			t.Fatal(err)
		}
		assertErrIs(t, c.IngestDER(der), ErrScopeMismatch)
	})

	t.Run("different issuer DN", func(t *testing.T) {
		// 同一把密钥、另一主体的 CA 证书签出的 CRL：签名可验通，
		// 但 RawIssuer 与受信签发者不一致，必须被拦下。
		alias, err := ca.aliasCert("Rogue Issuer")
		if err != nil {
			t.Fatal(err)
		}
		der, err := ca.signAs(deltaSpec(11, 10), alias, ca.key)
		if err != nil {
			t.Fatal(err)
		}
		assertErrIs(t, c.IngestDER(der), ErrIssuerMismatch)
	})

	t.Run("foreign CA signature", func(t *testing.T) {
		other, err := newTestCA("Other CA")
		if err != nil {
			t.Fatal(err)
		}
		der, err := other.signCRL(deltaSpec(11, 10))
		if err != nil {
			t.Fatal(err)
		}
		assertErrIs(t, c.IngestDER(der), ErrBadSignature)
	})
}
