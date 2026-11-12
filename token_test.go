package netsec

import (
	"errors"
	"testing"
	"time"
)

const idleTimeout = 30 * time.Second

// newTestManager 返回一个使用可控时钟的 TokenManager 及时钟前进函数。
func newTestManager(t *testing.T) (*TokenManager, func(d time.Duration)) {
	t.Helper()
	base := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	current := base
	m := NewTokenManager(idleTimeout)
	m.now = func() time.Time { return current }
	return m, func(d time.Duration) { current = current.Add(d) }
}

func TestIssueBindingRules(t *testing.T) {
	m, _ := newTestManager(t)

	if _, err := m.Issue(PermMC, ""); !errors.Is(err, ErrRpIDRequired) {
		t.Fatalf("mc without rpId: got %v, want ErrRpIDRequired", err)
	}
	if _, err := m.Issue(PermGA, ""); !errors.Is(err, ErrRpIDRequired) {
		t.Fatalf("ga without rpId: got %v, want ErrRpIDRequired", err)
	}
	if _, err := m.Issue(PermCM, "example.com"); !errors.Is(err, ErrRpIDForbidden) {
		t.Fatalf("cm with rpId: got %v, want ErrRpIDForbidden", err)
	}
	if _, err := m.Issue(PermMC|PermGA, "example.com"); err != nil {
		t.Fatalf("mc|ga with rpId should succeed: %v", err)
	}
	if _, err := m.Issue(PermCM|PermBE|PermLBW|PermACFG, ""); err != nil {
		t.Fatalf("cm|be|lbw|acfg without rpId should succeed: %v", err)
	}
}

func TestCrossRpAssertionRejected(t *testing.T) {
	m, _ := newTestManager(t)

	tok, err := m.Issue(PermGA, "good.example")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	// 同一 rpId 的断言请求允许。
	if err := m.Validate(tok.ID, PermGA, "good.example"); err != nil {
		t.Fatalf("same-rp getAssertion should pass: %v", err)
	}
	// 恶意页面把令牌拿去别的 RP 签断言，必须拒绝。
	if err := m.Validate(tok.ID, PermGA, "evil.example"); !errors.Is(err, ErrRpIDMismatch) {
		t.Fatalf("cross-rp getAssertion: got %v, want ErrRpIDMismatch", err)
	}
}

func TestCmTokenCannotMakeCredential(t *testing.T) {
	m, _ := newTestManager(t)

	tok, err := m.Issue(PermCM, "")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	// cm 令牌做凭据管理自身合法。
	if err := m.Validate(tok.ID, PermCM, ""); err != nil {
		t.Fatalf("cm validate should pass: %v", err)
	}
	// 但缺少 mc 权限位，用于 makeCredential 必须拒绝。
	if err := m.Validate(tok.ID, PermMC, "evil.example"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("cm token for mc: got %v, want ErrPermissionDenied", err)
	}
	if err := m.ConsumeMC(tok.ID, "evil.example"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("cm token ConsumeMC: got %v, want ErrPermissionDenied", err)
	}
}

func TestMcRevokedAfterSuccessfulMakeCredential(t *testing.T) {
	m, _ := newTestManager(t)

	tok, err := m.Issue(PermMC|PermGA, "app.example")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if err := m.ConsumeMC(tok.ID, "app.example"); err != nil {
		t.Fatalf("first makeCredential should succeed: %v", err)
	}
	// mc 权限已收回：再次分发 makeCredential 被拒绝。
	if err := m.Validate(tok.ID, PermMC, "app.example"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("mc after consume: got %v, want ErrPermissionDenied", err)
	}
	if err := m.ConsumeMC(tok.ID, "app.example"); !errors.Is(err, ErrPermissionConsumed) {
		t.Fatalf("second ConsumeMC: got %v, want ErrPermissionConsumed", err)
	}
	// 其余权限（ga）不受影响。
	if err := m.Validate(tok.ID, PermGA, "app.example"); err != nil {
		t.Fatalf("ga should still pass: %v", err)
	}
}

func TestIdleTimeoutBoundary(t *testing.T) {
	m, advance := newTestManager(t)

	tok, err := m.Issue(PermGA, "app.example")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	// 恰好到达无操作期限边界：仍有效。
	advance(idleTimeout)
	if err := m.Validate(tok.ID, PermGA, "app.example"); err != nil {
		t.Fatalf("at boundary should still be valid: %v", err)
	}
	// 超过期限一纳秒：失效。
	advance(idleTimeout + time.Nanosecond)
	if err := m.Validate(tok.ID, PermGA, "app.example"); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("past boundary: got %v, want ErrTokenExpired", err)
	}
	// 过期后令牌被清除。
	if err := m.Validate(tok.ID, PermGA, "app.example"); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("after expiry: got %v, want ErrTokenNotFound", err)
	}
}

func TestValidateRefreshesActivity(t *testing.T) {
	m, advance := newTestManager(t)

	tok, err := m.Issue(PermGA, "app.example")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	// 每次在期限内使用都会刷新活跃时间，连续使用不会过期。
	for i := 0; i < 5; i++ {
		advance(idleTimeout - time.Second)
		if err := m.Validate(tok.ID, PermGA, "app.example"); err != nil {
			t.Fatalf("iteration %d: token should remain valid: %v", i, err)
		}
	}
}

func TestPowerCycleInvalidatesTokens(t *testing.T) {
	m, _ := newTestManager(t)

	gaTok, err := m.Issue(PermGA, "app.example")
	if err != nil {
		t.Fatalf("issue ga: %v", err)
	}
	cmTok, err := m.Issue(PermCM, "")
	if err != nil {
		t.Fatalf("issue cm: %v", err)
	}

	m.PowerCycle() // 认证器重新上电

	if err := m.Validate(gaTok.ID, PermGA, "app.example"); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("ga token after power cycle: got %v, want ErrTokenNotFound", err)
	}
	if err := m.Validate(cmTok.ID, PermCM, ""); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("cm token after power cycle: got %v, want ErrTokenNotFound", err)
	}
	// 上电后可正常签发新令牌。
	if _, err := m.Issue(PermGA, "app.example"); err != nil {
		t.Fatalf("issue after power cycle: %v", err)
	}
}
