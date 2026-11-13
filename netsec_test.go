package netsec

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestNormalizeUserCodeIgnoresHyphenAndCase(t *testing.T) {
	cases := map[string]string{
		"BCDFGHJK":        "BCDFGHJK",
		"bcdfghjk":        "BCDFGHJK",
		"BCDF-GHJK":       "BCDFGHJK",
		"bcdf-ghjk":       "BCDFGHJK",
		"BcDf-GhJk":       "BCDFGHJK",
		"B-C-D-F-G-H-J-K": "BCDFGHJK",
	}
	for input, want := range cases {
		got, err := NormalizeUserCode(input)
		if err != nil {
			t.Fatalf("NormalizeUserCode(%q) error: %v", input, err)
		}
		if got != want {
			t.Errorf("NormalizeUserCode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeUserCodeRejectsMalformed(t *testing.T) {
	for _, input := range []string{
		"",          // 空
		"BCDFGHJ",   // 太短
		"BCDFGHJKL", // 太长
		"BCDF-GHJ",  // 去掉连字符后太短
		"BCDFGHJA",  // 含元音 A，不在字母表内
		"BCDFGHJ1",  // 含数字
		"BCDF GHJK", // 含空格
		"BCDFGHJ-",  // 去掉连字符后太短
	} {
		if _, err := NormalizeUserCode(input); !errors.Is(err, ErrMalformedUserCode) {
			t.Errorf("NormalizeUserCode(%q) err = %v, want ErrMalformedUserCode", input, err)
		}
	}
}

func TestFormatUserCode(t *testing.T) {
	if got := FormatUserCode("BCDFGHJK"); got != "BCDF-GHJK" {
		t.Errorf("FormatUserCode = %q, want BCDF-GHJK", got)
	}
}

func TestGenerateUserCode(t *testing.T) {
	for i := 0; i < 100; i++ {
		code, err := GenerateUserCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != UserCodeLength {
			t.Fatalf("len(%q) = %d, want %d", code, len(code), UserCodeLength)
		}
		for _, r := range code {
			if !strings.ContainsRune(UserCodeAlphabet, r) {
				t.Fatalf("code %q contains %q outside alphabet", code, r)
			}
		}
		display := FormatUserCode(code)
		if len(display) != 9 || display[4] != '-' {
			t.Fatalf("display form %q is not XXXX-XXXX", display)
		}
		if back, err := NormalizeUserCode(display); err != nil || back != code {
			t.Fatalf("round-trip failed: %q -> %q, %v", display, back, err)
		}
	}
}

func TestVerifyAcceptsHyphenAndLowercase(t *testing.T) {
	v := NewVerifier()
	code, err := GenerateUserCode()
	if err != nil {
		t.Fatal(err)
	}
	v.RegisterUserCode(code)
	mixed := strings.ToLower(FormatUserCode(code))
	if err := v.Verify("203.0.113.1", mixed); err != nil {
		t.Fatalf("Verify(%q) = %v, want nil", mixed, err)
	}
}

func TestVerifyUnknownCode(t *testing.T) {
	v := NewVerifier()
	if err := v.Verify("203.0.113.1", "BCDF-GHJK"); !errors.Is(err, ErrInvalidUserCode) {
		t.Fatalf("Verify unknown code = %v, want ErrInvalidUserCode", err)
	}
}

func TestIPLockBoundary(t *testing.T) {
	clock := newFakeClock()
	v := NewVerifier(WithClock(clock.now))
	ip := "198.51.100.7"

	// 窗口内前 5 次错误仅返回无效，不锁定。
	for i := 0; i < DefaultMaxIPErrors; i++ {
		if err := v.Verify(ip, fmt.Sprintf("BCDF-GHJ%c", "KLMNP"[i])); !errors.Is(err, ErrInvalidUserCode) {
			t.Fatalf("attempt %d = %v, want ErrInvalidUserCode", i+1, err)
		}
	}
	// 第 6 次错误触发锁定。
	if err := v.Verify(ip, "BCDF-GHJQ"); !errors.Is(err, ErrSourceLocked) {
		t.Fatalf("6th error = %v, want ErrSourceLocked", err)
	}
	// 锁定对所有 user_code 一视同仁：即使 code 真实有效也被拒绝。
	valid, _ := GenerateUserCode()
	v.RegisterUserCode(valid)
	if err := v.Verify(ip, valid); !errors.Is(err, ErrSourceLocked) {
		t.Fatalf("locked source with valid code = %v, want ErrSourceLocked", err)
	}
	// 锁定边界：15 分钟差 1 秒仍锁定。
	clock.advance(DefaultLockDuration - time.Second)
	if err := v.Verify(ip, valid); !errors.Is(err, ErrSourceLocked) {
		t.Fatalf("1s before unlock = %v, want ErrSourceLocked", err)
	}
	// 到达 15 分钟整即解锁。
	clock.advance(time.Second)
	if err := v.Verify(ip, valid); err != nil {
		t.Fatalf("after lock expiry = %v, want nil", err)
	}
}

func TestIPWindowSlides(t *testing.T) {
	clock := newFakeClock()
	v := NewVerifier(WithClock(clock.now))
	ip := "198.51.100.8"

	for i := 0; i < DefaultMaxIPErrors; i++ {
		_ = v.Verify(ip, "BCDF-GHJK")
	}
	// 旧错误滑出 5 分钟窗口后重新计数，再来 5 次仍不锁定。
	clock.advance(DefaultIPWindow + time.Second)
	for i := 0; i < DefaultMaxIPErrors; i++ {
		if err := v.Verify(ip, "BCDF-GHJK"); !errors.Is(err, ErrInvalidUserCode) {
			t.Fatalf("attempt %d after window slide = %v, want ErrInvalidUserCode", i+1, err)
		}
	}
	if err := v.Verify(ip, "BCDF-GHJK"); !errors.Is(err, ErrSourceLocked) {
		t.Fatalf("6th error in new window = %v, want ErrSourceLocked", err)
	}
}

func TestUserCodeBurnedAfterFiveFailures(t *testing.T) {
	clock := newFakeClock()
	v := NewVerifier(WithClock(clock.now))
	code := "BCDFGHJK"

	// 用不同来源 IP 各试一次，避免触发来源锁定。
	for i := 0; i < DefaultMaxCodeFailures-1; i++ {
		ip := fmt.Sprintf("192.0.2.%d", i+1)
		if err := v.Verify(ip, code); !errors.Is(err, ErrInvalidUserCode) {
			t.Fatalf("failure %d = %v, want ErrInvalidUserCode", i+1, err)
		}
	}
	// 4 次错误后 code 尚未作废：登记后即可正常验证。
	v.RegisterUserCode(code)
	if err := v.Verify("192.0.2.100", code); err != nil {
		t.Fatalf("after 4 failures, registered code = %v, want nil", err)
	}

	// 换一个未登记的 code 累计 5 次错误即作废。
	victim := "QTJDWSZX"
	for i := 0; i < DefaultMaxCodeFailures; i++ {
		ip := fmt.Sprintf("192.0.2.%d", i+10)
		_ = v.Verify(ip, victim)
	}
	// 作废后即使登记也无法使用。
	v.RegisterUserCode(victim)
	if err := v.Verify("192.0.2.200", victim); !errors.Is(err, ErrInvalidUserCode) {
		t.Fatalf("burned code = %v, want ErrInvalidUserCode", err)
	}
}

func TestPollSlowDown(t *testing.T) {
	clock := newFakeClock()
	m := NewTokenManager(WithTokenClock(clock.now))
	dc := "device-code-1"
	m.RegisterDevice(dc)

	if iv, _ := m.Interval(dc); iv != DefaultPollInterval {
		t.Fatalf("initial interval = %v, want %v", iv, DefaultPollInterval)
	}
	// 首次轮询：待授权。
	if _, err := m.Poll(dc); !errors.Is(err, ErrAuthorizationPending) {
		t.Fatalf("first poll = %v, want ErrAuthorizationPending", err)
	}
	// 早于 5 秒间隔再次轮询：slow_down，间隔升到 10 秒。
	clock.advance(2 * time.Second)
	if _, err := m.Poll(dc); !errors.Is(err, ErrSlowDown) {
		t.Fatalf("early poll = %v, want ErrSlowDown", err)
	}
	if iv, _ := m.Interval(dc); iv != 10*time.Second {
		t.Fatalf("interval after slow_down = %v, want 10s", iv)
	}
	// 新的 10 秒间隔从这次违规请求起算。
	clock.advance(9 * time.Second)
	if _, err := m.Poll(dc); !errors.Is(err, ErrSlowDown) {
		t.Fatalf("poll inside raised interval = %v, want ErrSlowDown", err)
	}
	if iv, _ := m.Interval(dc); iv != 15*time.Second {
		t.Fatalf("interval after second slow_down = %v, want 15s", iv)
	}
	// 遵守间隔后恢复正常的 pending 响应。
	clock.advance(15 * time.Second)
	if _, err := m.Poll(dc); !errors.Is(err, ErrAuthorizationPending) {
		t.Fatalf("poll after respecting interval = %v, want ErrAuthorizationPending", err)
	}
}

func TestPollRedeemThenReplay(t *testing.T) {
	clock := newFakeClock()
	m := NewTokenManager(WithTokenClock(clock.now))
	dc := "device-code-2"
	m.RegisterDevice(dc)
	m.Approve(dc)

	token, err := m.Poll(dc)
	if err != nil {
		t.Fatalf("approved poll = %v, want token", err)
	}
	if token.AccessToken == "" || token.TokenType != "Bearer" {
		t.Fatalf("bad token: %+v", token)
	}
	// device_code 换出令牌后立即失效：重放返回 invalid_grant。
	if _, err := m.Poll(dc); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("replay after redeem = %v, want ErrInvalidGrant", err)
	}
	// 未知 device_code 同样是 invalid_grant。
	if _, err := m.Poll("no-such-code"); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("unknown device code = %v, want ErrInvalidGrant", err)
	}
}

func TestPollAccessDenied(t *testing.T) {
	clock := newFakeClock()
	m := NewTokenManager(WithTokenClock(clock.now))
	dc := "device-code-3"
	m.RegisterDevice(dc)
	m.Deny(dc)

	if _, err := m.Poll(dc); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("denied poll = %v, want ErrAccessDenied", err)
	}
	// 拒绝是终态，之后轮询仍然 access_denied。
	clock.advance(time.Minute)
	if _, err := m.Poll(dc); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("later denied poll = %v, want ErrAccessDenied", err)
	}
}
