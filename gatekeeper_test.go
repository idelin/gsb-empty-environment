package netsec

import (
	"net/netip"
	"testing"
	"time"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func newTestGatekeeper(t *testing.T, clock *fakeClock) *Gatekeeper {
	t.Helper()
	g, err := NewGatekeeper(Config{Now: clock.Now})
	if err != nil {
		t.Fatalf("NewGatekeeper: %v", err)
	}
	if g.threshold != DefaultHalfOpenThreshold {
		t.Fatalf("threshold = %d, want %d", g.threshold, DefaultHalfOpenThreshold)
	}
	return g
}

func fillHalfOpen(g *Gatekeeper, n int) {
	for i := 0; i < n; i++ {
		g.HalfOpenBegin()
	}
}

func testRequest() Request {
	return Request{
		SourceAddr: netip.MustParseAddr("203.0.113.7"),
		SPIi:       [8]byte{1, 2, 3, 4, 5, 6, 7, 8},
		Ni:         []byte("initiator-nonce-0123456789abcdef"),
	}
}

// 无 Cookie：未达阈值直接处理；达到阈值 200 后只回 COOKIE，不产生半开状态。
func TestAdmitWithoutCookie(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	g := newTestGatekeeper(t, clock)
	req := testRequest()

	if d := g.Admit(req); d.Action != Process {
		t.Fatalf("below threshold: action = %v, want Process", d.Action)
	}

	fillHalfOpen(g, DefaultHalfOpenThreshold)
	before := g.HalfOpenCount()

	d := g.Admit(req)
	if d.Action != SendCookie {
		t.Fatalf("at threshold: action = %v, want SendCookie", d.Action)
	}
	if len(d.Cookie) != cookieSize {
		t.Fatalf("cookie length = %d, want %d", len(d.Cookie), cookieSize)
	}
	if got := g.HalfOpenCount(); got != before {
		t.Fatalf("half-open count changed to %d, want %d (no state, no DH)", got, before)
	}
}

// 正常流程：质询后重发携带匹配 COOKIE 的请求（Ni、源地址、SPIi 不变）被放行。
func TestNormalCookieFlow(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	g := newTestGatekeeper(t, clock)
	fillHalfOpen(g, DefaultHalfOpenThreshold)
	req := testRequest()

	challenge := g.Admit(req)
	if challenge.Action != SendCookie {
		t.Fatalf("action = %v, want SendCookie", challenge.Action)
	}

	retry := req
	retry.Cookie = challenge.Cookie
	if d := g.Admit(retry); d.Action != Process {
		t.Fatalf("retry with valid cookie: action = %v, want Process", d.Action)
	}
}

// 换 Ni：COOKIE 是用原 Ni 计算的，改 Ni 后必须丢弃。
func TestCookieRejectedOnNonceChange(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	g := newTestGatekeeper(t, clock)
	fillHalfOpen(g, DefaultHalfOpenThreshold)
	req := testRequest()

	cookie := g.Admit(req).Cookie

	tampered := req
	tampered.Ni = []byte("attacker-changed-nonce-000000000")
	tampered.Cookie = cookie
	if d := g.Admit(tampered); d.Action != Drop {
		t.Fatalf("changed Ni: action = %v, want Drop", d.Action)
	}
}

// 换源地址：COOKIE 绑定源地址，伪造其他地址重放必须丢弃。
func TestCookieRejectedOnSourceChange(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	g := newTestGatekeeper(t, clock)
	fillHalfOpen(g, DefaultHalfOpenThreshold)
	req := testRequest()

	cookie := g.Admit(req).Cookie

	spoofed := req
	spoofed.SourceAddr = netip.MustParseAddr("198.51.100.99")
	spoofed.Cookie = cookie
	if d := g.Admit(spoofed); d.Action != Drop {
		t.Fatalf("changed source addr: action = %v, want Drop", d.Action)
	}
}

// 秘密轮换：上一把秘密在一个周期内仍可验证旧 COOKIE，再次轮换后失效。
func TestSecretRotationGracePeriod(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	g := newTestGatekeeper(t, clock)
	fillHalfOpen(g, DefaultHalfOpenThreshold)
	req := testRequest()

	cookie := g.Admit(req).Cookie
	withCookie := req
	withCookie.Cookie = cookie

	// 第一次轮换：旧秘密进入宽限期，旧 COOKIE 仍可用。
	clock.Advance(DefaultRotationInterval + time.Second)
	if d := g.Admit(withCookie); d.Action != Process {
		t.Fatalf("after first rotation: action = %v, want Process (previous secret grace)", d.Action)
	}

	// 第二次轮换：旧秘密被丢弃，旧 COOKIE 失效。
	clock.Advance(DefaultRotationInterval + time.Second)
	if d := g.Admit(withCookie); d.Action != Drop {
		t.Fatalf("after second rotation: action = %v, want Drop (secret expired)", d.Action)
	}
}

// 轮换后新请求应使用新秘密签发 COOKIE。
func TestCookieIssuedWithRotatedSecret(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	g := newTestGatekeeper(t, clock)
	fillHalfOpen(g, DefaultHalfOpenThreshold)
	req := testRequest()

	oldCookie := g.Admit(req).Cookie
	clock.Advance(DefaultRotationInterval + time.Second)
	newCookie := g.Admit(req).Cookie

	if string(oldCookie) == string(newCookie) {
		t.Fatal("cookie unchanged after secret rotation")
	}
	retry := req
	retry.Cookie = newCookie
	if d := g.Admit(retry); d.Action != Process {
		t.Fatalf("retry with new cookie: action = %v, want Process", d.Action)
	}
}
