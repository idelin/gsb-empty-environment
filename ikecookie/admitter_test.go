package ikecookie

import (
	"bytes"
	"net/netip"
	"sync"
	"testing"
	"time"
)

func newTestAdmitter(t *testing.T, opts ...Option) *Admitter {
	t.Helper()
	defaults := []Option{WithRotationInterval(0)}
	a, err := NewAdmitter(append(defaults, opts...)...)
	if err != nil {
		t.Fatalf("NewAdmitter: %v", err)
	}
	t.Cleanup(a.Close)
	return a
}

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	addr, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return addr
}

func spiiAt(b byte) SPIi {
	var s SPIi
	for i := range s {
		s[i] = b
	}
	return s
}

func newReq(ip netip.Addr, spii SPIi, ni, cookie []byte) InitRequest {
	return InitRequest{SourceIP: ip, SPIi: spii, Ni: ni, Cookie: cookie}
}

// fillTable reserves limit distinct half-open entries without releasing them.
func fillTable(t *testing.T, a *Admitter, limit int) {
	t.Helper()
	ni := []byte("nonce-bulk")
	for i := 0; i < limit; i++ {
		res := a.Admit(newReq(
			netip.AddrFrom4([4]byte{10, 0, byte(i >> 8), byte(i)}),
			spiiAt(byte(i)), ni, nil))
		if res.Decision != DecisionAllow {
			t.Fatalf("entry %d: want Allow, got %v", i, res.Decision)
		}
	}
	if got := a.HalfOpen(); got != limit {
		t.Fatalf("HalfOpen: want %d, got %d", limit, got)
	}
}

// Below the threshold, cookieless requests are processed and reserve a slot.
func TestCookielessBelowThreshold(t *testing.T) {
	a := newTestAdmitter(t)
	req := newReq(mustAddr(t, "198.51.100.7"), spiiAt(1), []byte("ni-1"), nil)

	res := a.Admit(req)
	if res.Decision != DecisionAllow {
		t.Fatalf("want Allow below threshold, got %v", res.Decision)
	}
	if res.Release == nil {
		t.Fatal("Allow result must carry a Release func")
	}
	if res.Cookie != nil {
		t.Fatalf("no cookie expected below threshold, got %x", res.Cookie)
	}
	if a.HalfOpen() != 1 {
		t.Fatalf("HalfOpen: want 1, got %d", a.HalfOpen())
	}

	res.Release()
	if a.HalfOpen() != 0 {
		t.Fatalf("HalfOpen after release: want 0, got %d", a.HalfOpen())
	}
	res.Release()
	if a.HalfOpen() != 0 {
		t.Fatalf("HalfOpen after second release: want 0, got %d", a.HalfOpen())
	}
}

// At the threshold, a cookieless request gets only a COOKIE: no DH, no slot.
func TestCookielessAtThresholdReturnsCookie(t *testing.T) {
	a := newTestAdmitter(t)
	fillTable(t, a, DefaultHalfOpenLimit)

	req := newReq(mustAddr(t, "203.0.113.9"), spiiAt(0xAB), []byte("flood-ni"), nil)
	before := a.HalfOpen()
	res := a.Admit(req)

	if res.Decision != DecisionCookie {
		t.Fatalf("want Cookie at threshold, got %v", res.Decision)
	}
	if len(res.Cookie) == 0 {
		t.Fatal("Cookie decision must return a cookie value")
	}
	if res.Release != nil {
		t.Fatal("a challenge must not reserve a half-open slot")
	}
	if a.HalfOpen() != before {
		t.Fatalf("challenge changed half-open count: before=%d after=%d", before, a.HalfOpen())
	}

	res2 := a.Admit(req)
	if !bytes.Equal(res.Cookie, res2.Cookie) {
		t.Fatal("identical request must yield an identical cookie")
	}
}

// Normal flow under load: challenge then a retransmission carrying the cookie
// is processed, and Ni must match the value used when the cookie was issued.
func TestCookieRoundTrip(t *testing.T) {
	a := newTestAdmitter(t)
	fillTable(t, a, DefaultHalfOpenLimit)

	ip := mustAddr(t, "192.0.2.50")
	spii := spiiAt(0x33)
	ni := []byte("initiator-nonce")

	challenge := a.Admit(newReq(ip, spii, ni, nil))
	if challenge.Decision != DecisionCookie {
		t.Fatalf("want challenge, got %v", challenge.Decision)
	}

	ok := a.Admit(newReq(ip, spii, ni, challenge.Cookie))
	if ok.Decision != DecisionAllow {
		t.Fatalf("retransmission with valid cookie: want Allow, got %v", ok.Decision)
	}
	if ok.Release == nil {
		t.Fatal("Allow result must carry a Release func")
	}
	defer ok.Release()

	if a.HalfOpen() != DefaultHalfOpenLimit+1 {
		t.Fatalf("HalfOpen: want %d, got %d", DefaultHalfOpenLimit+1, a.HalfOpen())
	}
}

// Changing Ni after the cookie was issued must invalidate it.
func TestCookieRejectsChangedNi(t *testing.T) {
	a := newTestAdmitter(t)
	fillTable(t, a, DefaultHalfOpenLimit)

	ip := mustAddr(t, "192.0.2.51")
	spii := spiiAt(0x44)
	ni := []byte("original-ni")

	challenge := a.Admit(newReq(ip, spii, ni, nil))
	if challenge.Decision != DecisionCookie {
		t.Fatalf("want challenge, got %v", challenge.Decision)
	}

	tampered := a.Admit(newReq(ip, spii, []byte("changed-ni"), challenge.Cookie))
	if tampered.Decision != DecisionDrop {
		t.Fatalf("changed Ni: want Drop, got %v", tampered.Decision)
	}
	if tampered.Release != nil {
		t.Fatal("dropped request must not reserve a slot")
	}

	forged := a.Admit(newReq(ip, spii, ni, bytes.Repeat([]byte{0x01}, len(challenge.Cookie))))
	if forged.Decision != DecisionDrop {
		t.Fatalf("forged cookie: want Drop, got %v", forged.Decision)
	}
}

// Changing the source address after the cookie was issued must invalidate it.
func TestCookieRejectsChangedSource(t *testing.T) {
	a := newTestAdmitter(t)
	fillTable(t, a, DefaultHalfOpenLimit)

	original := mustAddr(t, "198.51.100.200")
	spoofer := mustAddr(t, "198.51.100.201")
	spii := spiiAt(0x55)
	ni := []byte("bound-ni")

	challenge := a.Admit(newReq(original, spii, ni, nil))
	if challenge.Decision != DecisionCookie {
		t.Fatalf("want challenge, got %v", challenge.Decision)
	}

	replay := a.Admit(newReq(spoofer, spii, ni, challenge.Cookie))
	if replay.Decision != DecisionDrop {
		t.Fatalf("changed source: want Drop, got %v", replay.Decision)
	}

	ok := a.Admit(newReq(original, spii, ni, challenge.Cookie))
	if ok.Decision != DecisionAllow {
		t.Fatalf("original tuple: want Allow, got %v", ok.Decision)
	}
	ok.Release()
}

// Changing SPIi after the cookie was issued must invalidate it.
func TestCookieRejectsChangedSPIi(t *testing.T) {
	a := newTestAdmitter(t)
	fillTable(t, a, DefaultHalfOpenLimit)

	ip := mustAddr(t, "192.0.2.66")
	ni := []byte("spii-bound-ni")

	challenge := a.Admit(newReq(ip, spiiAt(0x66), ni, nil))
	if challenge.Decision != DecisionCookie {
		t.Fatalf("want challenge, got %v", challenge.Decision)
	}

	replay := a.Admit(newReq(ip, spiiAt(0x67), ni, challenge.Cookie))
	if replay.Decision != DecisionDrop {
		t.Fatalf("changed SPIi: want Drop, got %v", replay.Decision)
	}
}

// After one rotation the previous secret still validates; after two it does not.
func TestRotationKeepsPreviousSecretOnePeriod(t *testing.T) {
	a := newTestAdmitter(t)
	fillTable(t, a, DefaultHalfOpenLimit)

	ip := mustAddr(t, "203.0.113.77")
	spii := spiiAt(0x77)
	ni := []byte("rotation-ni")

	challenge := a.Admit(newReq(ip, spii, ni, nil))
	if challenge.Decision != DecisionCookie {
		t.Fatalf("want challenge, got %v", challenge.Decision)
	}

	if err := a.RotateSecret(); err != nil {
		t.Fatalf("RotateSecret: %v", err)
	}
	newChallenge := a.Admit(newReq(ip, spii, ni, nil))
	if newChallenge.Decision != DecisionCookie {
		t.Fatalf("want challenge, got %v", newChallenge.Decision)
	}
	if bytes.Equal(challenge.Cookie, newChallenge.Cookie) {
		t.Fatal("cookie must change after secret rotation")
	}

	oldStillGood := a.Admit(newReq(ip, spii, ni, challenge.Cookie))
	if oldStillGood.Decision != DecisionAllow {
		t.Fatalf("old cookie after one rotation: want Allow, got %v", oldStillGood.Decision)
	}
	oldStillGood.Release()

	if err := a.RotateSecret(); err != nil {
		t.Fatalf("RotateSecret: %v", err)
	}
	expired := a.Admit(newReq(ip, spii, ni, challenge.Cookie))
	if expired.Decision != DecisionDrop {
		t.Fatalf("old cookie after two rotations: want Drop, got %v", expired.Decision)
	}

	previousGood := a.Admit(newReq(ip, spii, ni, newChallenge.Cookie))
	if previousGood.Decision != DecisionAllow {
		t.Fatalf("previous-secret cookie after two rotations: want Allow, got %v", previousGood.Decision)
	}
	previousGood.Release()
}

// Automatic rotation via the janitor keeps the previous secret valid.
func TestAutomaticRotation(t *testing.T) {
	a := newTestAdmitter(t,
		WithRotationInterval(20*time.Millisecond),
		WithSweepInterval(time.Hour),
	)
	fillTable(t, a, DefaultHalfOpenLimit)

	ip := mustAddr(t, "203.0.113.88")
	ni := []byte("auto-ni")
	req := newReq(ip, spiiAt(0x88), ni, nil)
	first := a.Admit(req)
	if first.Decision != DecisionCookie {
		t.Fatalf("want challenge, got %v", first.Decision)
	}

	deadline := time.Now().Add(2 * time.Second)
	rotated := false
	for time.Now().Before(deadline) {
		if !bytes.Equal(a.Admit(req).Cookie, first.Cookie) {
			rotated = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !rotated {
		t.Fatal("secret did not rotate automatically")
	}

	res := a.Admit(newReq(ip, spiiAt(0x88), ni, first.Cookie))
	if res.Decision != DecisionAllow {
		t.Fatalf("pre-rotation cookie after auto rotation: want Allow, got %v", res.Decision)
	}
	res.Release()
}

// Half-open entries are reaped after their TTL, freeing threshold capacity.
func TestHalfOpenTTLReclaim(t *testing.T) {
	a := newTestAdmitter(t,
		WithHalfOpenTTL(30*time.Millisecond),
		WithSweepInterval(15*time.Millisecond),
	)

	req := newReq(mustAddr(t, "198.51.100.9"), spiiAt(0x09), []byte("ttl-ni"), nil)
	if res := a.Admit(req); res.Decision != DecisionAllow {
		t.Fatalf("want Allow, got %v", res.Decision)
	}
	if a.HalfOpen() != 1 {
		t.Fatalf("HalfOpen: want 1, got %d", a.HalfOpen())
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && a.HalfOpen() != 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if a.HalfOpen() != 0 {
		t.Fatalf("expired entry was not reaped, HalfOpen=%d", a.HalfOpen())
	}

	if res := a.Admit(req); res.Decision != DecisionAllow {
		t.Fatalf("want Allow after reclaim, got %v", res.Decision)
	}
}

// A missing/invalid source address is rejected rather than crashing.
func TestInvalidSource(t *testing.T) {
	a := newTestAdmitter(t)
	res := a.Admit(newReq(netip.Addr{}, spiiAt(1), []byte("ni"), nil))
	if res.Decision != DecisionInvalid {
		t.Fatalf("want Invalid, got %v", res.Decision)
	}
}

// Concurrent admissions must stay race-free and respect the counter.
func TestConcurrentAdmit(t *testing.T) {
	a := newTestAdmitter(t, WithHalfOpenLimit(50))

	const goroutines = 8
	const perGoroutine = 25
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				ip := netip.AddrFrom4([4]byte{
					172, 16, byte(g), byte(i),
				})
				res := a.Admit(newReq(ip, spiiAt(byte(g)), []byte("c-ni"), nil))
				switch res.Decision {
				case DecisionAllow:
					res.Release()
				case DecisionCookie:
				default:
					t.Errorf("unexpected decision %v", res.Decision)
				}
			}
		}(g)
	}
	wg.Wait()

	if got := a.HalfOpen(); got != 0 {
		t.Fatalf("HalfOpen after all releases: want 0, got %d", got)
	}
}

// The cookie is a deterministic HMAC over the framed tuple and is IP-version
// aware; an IPv6 source binds the cookie to that 16-byte address.
func TestCookieDeterminismAndIPv6Binding(t *testing.T) {
	a := newTestAdmitter(t)
	fillTable(t, a, DefaultHalfOpenLimit)

	ip4 := mustAddr(t, "192.0.2.10")
	ip6 := mustAddr(t, "2001:db8::10")
	spii := spiiAt(0x10)
	ni := []byte("framed-nonce")

	req4 := newReq(ip4, spii, ni, nil)
	req6 := newReq(ip6, spii, ni, nil)

	c4 := a.Admit(req4)
	c4Again := a.Admit(req4)
	c6 := a.Admit(req6)

	if !bytes.Equal(c4.Cookie, c4Again.Cookie) {
		t.Fatal("IPv4 cookie must be deterministic")
	}
	if bytes.Equal(c4.Cookie, c6.Cookie) {
		t.Fatal("IPv4 and IPv6 cookies for the same tuple must differ")
	}

	if res := a.Admit(newReq(mustAddr(t, "2001:db8::11"), spii, ni, c6.Cookie)); res.Decision != DecisionDrop {
		t.Fatalf("IPv6 changed source: want Drop, got %v", res.Decision)
	}
	if res := a.Admit(newReq(ip6, spii, ni, c6.Cookie)); res.Decision != DecisionAllow {
		t.Fatalf("IPv6 original tuple: want Allow, got %v", res.Decision)
	} else {
		res.Release()
	}
}
