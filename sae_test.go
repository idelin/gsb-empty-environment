package netsec

import (
	"crypto/elliptic"
	"crypto/rand"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"
)

var testMACs = []net.HardwareAddr{
	{0x02, 0x00, 0x00, 0x00, 0x00, 0x01},
	{0x02, 0x00, 0x00, 0x00, 0x00, 0x02},
	{0x02, 0x00, 0x00, 0x00, 0x00, 0x03},
}

// testClock 是可手动推进的时钟。
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestFilter(t *testing.T, maxPending int, clk *testClock) *Filter {
	t.Helper()
	cfg := Config{MaxPending: maxPending, TokenTTL: time.Minute}
	if clk != nil {
		cfg.Now = clk.Now
	}
	f, err := NewFilter(cfg, []byte("0123456789abcdef-test-secret"))
	if err != nil {
		t.Fatalf("NewFilter: %v", err)
	}
	return f
}

// makeCommit 生成一对合法的 (scalar, element)。
func makeCommit(t *testing.T, curve elliptic.Curve) (scalar, element []byte) {
	t.Helper()
	n := curve.Params().N
	l := byteLen(curve)
	// s 落在 [2, n-1]
	s, err := rand.Int(rand.Reader, new(big.Int).Sub(n, big.NewInt(3)))
	if err != nil {
		t.Fatal(err)
	}
	s.Add(s, big.NewInt(2))
	x, y := curve.ScalarBaseMult(s.Bytes())
	scalar = s.FillBytes(make([]byte, l))
	element = append(x.FillBytes(make([]byte, l)), y.FillBytes(make([]byte, l))...)
	return scalar, element
}

func scalarBytes(t *testing.T, curve elliptic.Curve, v *big.Int) []byte {
	t.Helper()
	return v.FillBytes(make([]byte, byteLen(curve)))
}

// TestFloodCommits 洪泛：超过阈值后无 token 的 commit 一律只回 token，
// 且过滤器自身不做任何曲线点乘。
func TestFloodCommits(t *testing.T) {
	f := newTestFilter(t, 2, nil)
	curve := elliptic.P256()

	// 两个合法 STA 占满未完成事务名额。
	for i := 0; i < 2; i++ {
		scalar, element := makeCommit(t, curve)
		d, _ := f.CheckCommit(testMACs[i], GroupP256, scalar, element, nil)
		if d != DecisionAccept {
			t.Fatalf("sta %d: got %v, want accept", i, d)
		}
		_, own := makeCommit(t, curve)
		f.TrackStart(testMACs[i], GroupP256, own)
	}

	// 洪泛：大量无 token 的 commit 全部只得到 anti-clogging token。
	for i := 0; i < 1000; i++ {
		scalar, element := makeCommit(t, curve)
		d, tok := f.CheckCommit(testMACs[2], GroupP256, scalar, element, nil)
		if d != DecisionAntiClogging {
			t.Fatalf("flood iter %d: got %v, want anti-clogging", i, d)
		}
		if len(tok) != 32 {
			t.Fatalf("flood iter %d: token len %d, want 32", i, len(tok))
		}
	}
	if f.Pending() != 2 {
		t.Fatalf("pending = %d, want 2 (flood must not create transactions)", f.Pending())
	}
}

// TestLegitimateHandshake 合法握手：阈值内直接通过；超阈值时先取 token，
// 携带 token 重发即通过，confirm 完成后释放名额。
func TestLegitimateHandshake(t *testing.T) {
	clk := &testClock{now: time.Now()}
	f := newTestFilter(t, 1, clk)
	curve := elliptic.P256()

	// STA1 在阈值内直接完成握手。
	s1, e1 := makeCommit(t, curve)
	if d, _ := f.CheckCommit(testMACs[0], GroupP256, s1, e1, nil); d != DecisionAccept {
		t.Fatalf("sta1 commit: got %v, want accept", d)
	}
	_, own1 := makeCommit(t, curve)
	f.TrackStart(testMACs[0], GroupP256, own1)
	f.TrackEnd(testMACs[0]) // confirm 完成

	// STA2 占住唯一名额后，STA3 需走 anti-clogging 流程。
	s2, e2 := makeCommit(t, curve)
	if d, _ := f.CheckCommit(testMACs[1], GroupP256, s2, e2, nil); d != DecisionAccept {
		t.Fatalf("sta2 commit: got %v, want accept", d)
	}
	_, own2 := makeCommit(t, curve)
	f.TrackStart(testMACs[1], GroupP256, own2)

	s3, e3 := makeCommit(t, curve)
	d, tok := f.CheckCommit(testMACs[2], GroupP256, s3, e3, nil)
	if d != DecisionAntiClogging {
		t.Fatalf("sta3 first commit: got %v, want anti-clogging", d)
	}
	// STA3 携带 token 重发同一 commit，应被接受。
	if d, _ := f.CheckCommit(testMACs[2], GroupP256, s3, e3, tok); d != DecisionAccept {
		t.Fatalf("sta3 commit with token: got %v, want accept", d)
	}
	_, own3 := makeCommit(t, curve)
	f.TrackStart(testMACs[2], GroupP256, own3)

	// 密钥轮换后（下一窗口）旧 token 仍在宽限期内可用。
	clk.Advance(time.Minute)
	s4, e4 := makeCommit(t, curve)
	if d, _ := f.CheckCommit(testMACs[2], GroupP256, s4, e4, tok); d != DecisionAccept {
		t.Fatalf("sta3 token in grace window: got %v, want accept", d)
	}
}

// TestTokenReplay 重放：token 绑定 (MAC, group) 与密钥窗口，
// 换站点、换群、跨两个窗口重放均须失败。
func TestTokenReplay(t *testing.T) {
	clk := &testClock{now: time.Now()}
	f := newTestFilter(t, 1, clk)
	curve := elliptic.P256()

	// 占满阈值，拿到发给 STA2 的 token。
	s1, e1 := makeCommit(t, curve)
	if d, _ := f.CheckCommit(testMACs[0], GroupP256, s1, e1, nil); d != DecisionAccept {
		t.Fatalf("setup commit: got %v", d)
	}
	_, own1 := makeCommit(t, curve)
	f.TrackStart(testMACs[0], GroupP256, own1)

	s2, e2 := makeCommit(t, curve)
	d, tok := f.CheckCommit(testMACs[1], GroupP256, s2, e2, nil)
	if d != DecisionAntiClogging {
		t.Fatalf("sta2 first commit: got %v, want anti-clogging", d)
	}

	// 重放给另一站点：拒绝（只回 token，不接受）。
	s3, e3 := makeCommit(t, curve)
	if d, _ := f.CheckCommit(testMACs[2], GroupP256, s3, e3, tok); d != DecisionAntiClogging {
		t.Fatalf("replay to other sta: got %v, want anti-clogging", d)
	}
	// 重放到另一群：拒绝。
	s4, e4 := makeCommit(t, elliptic.P384())
	if d, _ := f.CheckCommit(testMACs[1], GroupP384, s4, e4, tok); d != DecisionAntiClogging {
		t.Fatalf("replay to other group: got %v, want anti-clogging", d)
	}
	// 原站点在原窗口内重放：接受（这是正常的 token 使用）。
	if d, _ := f.CheckCommit(testMACs[1], GroupP256, s2, e2, tok); d != DecisionAccept {
		t.Fatalf("legit token use: got %v, want accept", d)
	}
	// 越过两个密钥窗口后重放：密钥已轮换，拒绝。
	clk.Advance(2 * time.Minute)
	s5, e5 := makeCommit(t, curve)
	if d, _ := f.CheckCommit(testMACs[1], GroupP256, s5, e5, tok); d != DecisionAntiClogging {
		t.Fatalf("replay after key rotation: got %v, want anti-clogging", d)
	}
}

// TestReflection 反射：攻击者把 AP 自己的 commit 回投，即使阈值未满也拒绝。
func TestReflection(t *testing.T) {
	f := newTestFilter(t, 8, nil)
	curve := elliptic.P256()

	s1, e1 := makeCommit(t, curve)
	if d, _ := f.CheckCommit(testMACs[0], GroupP256, s1, e1, nil); d != DecisionAccept {
		t.Fatalf("sta1 commit: got %v", d)
	}
	ownScalar, ownElement := makeCommit(t, curve)
	f.TrackStart(testMACs[0], GroupP256, ownElement)

	// 反射 AP 自己的 commit（scalar 与 element 都是 AP 发出的值）。
	if d, _ := f.CheckCommit(testMACs[0], GroupP256, ownScalar, ownElement, nil); d != DecisionReject {
		t.Fatalf("reflected commit: got %v, want reject", d)
	}
	// 同一 element 换到别的站点名下不算该站点的反射（各事务独立）。
	s2, _ := makeCommit(t, curve)
	if d, _ := f.CheckCommit(testMACs[1], GroupP256, s2, ownElement, nil); d != DecisionAccept {
		t.Fatalf("other sta same element: got %v, want accept", d)
	}
}

// TestScalarBounds scalar 必须落在 [2, r-1]。
func TestScalarBounds(t *testing.T) {
	f := newTestFilter(t, 8, nil)
	curve := elliptic.P256()
	n := curve.Params().N
	_, element := makeCommit(t, curve)

	cases := []struct {
		name   string
		scalar *big.Int
		want   Decision
	}{
		{"zero", big.NewInt(0), DecisionReject},
		{"one", big.NewInt(1), DecisionReject},
		{"two", big.NewInt(2), DecisionAccept},
		{"r-1", new(big.Int).Sub(n, big.NewInt(1)), DecisionAccept},
		{"r", n, DecisionReject},
		{"r+1", new(big.Int).Add(n, big.NewInt(1)), DecisionReject},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := f.CheckCommit(testMACs[0], GroupP256, scalarBytes(t, curve, tc.scalar), element, nil)
			if d != tc.want {
				t.Fatalf("scalar %s: got %v, want %v", tc.name, d, tc.want)
			}
		})
	}
	// 长度不符直接拒绝。
	if d, _ := f.CheckCommit(testMACs[0], GroupP256, []byte{0x02}, element, nil); d != DecisionReject {
		t.Fatalf("short scalar: got %v, want reject", d)
	}
}

// TestElementValidation element 必须是曲线上非无穷远点。
func TestElementValidation(t *testing.T) {
	f := newTestFilter(t, 8, nil)
	curve := elliptic.P256()
	l := byteLen(curve)
	scalar, element := makeCommit(t, curve)

	// 无穷远点 (0, 0)。
	inf := make([]byte, 2*l)
	if d, _ := f.CheckCommit(testMACs[0], GroupP256, scalar, inf, nil); d != DecisionReject {
		t.Fatalf("infinity element: got %v, want reject", d)
	}
	// 不在曲线上的点：合法 x 配非法 y。
	bad := make([]byte, 2*l)
	copy(bad, element)
	bad[2*l-1] ^= 0x01
	if curve.IsOnCurve(new(big.Int).SetBytes(bad[:l]), new(big.Int).SetBytes(bad[l:])) {
		t.Skip("flipped point accidentally on curve")
	}
	if d, _ := f.CheckCommit(testMACs[0], GroupP256, scalar, bad, nil); d != DecisionReject {
		t.Fatalf("off-curve element: got %v, want reject", d)
	}
	// 长度不符。
	if d, _ := f.CheckCommit(testMACs[0], GroupP256, scalar, element[:2*l-1], nil); d != DecisionReject {
		t.Fatalf("truncated element: got %v, want reject", d)
	}
	// 不支持的群。
	if d, _ := f.CheckCommit(testMACs[0], 99, scalar, element, nil); d != DecisionReject {
		t.Fatalf("unknown group: got %v, want reject", d)
	}
	// 合法 element 通过。
	if d, _ := f.CheckCommit(testMACs[0], GroupP256, scalar, element, nil); d != DecisionAccept {
		t.Fatalf("valid element: got %v, want accept", d)
	}
}

// TestAllGroups 三个 SAE 椭圆曲线群都能完成合法 commit 校验。
func TestAllGroups(t *testing.T) {
	f := newTestFilter(t, 8, nil)
	for _, g := range []struct {
		id    uint16
		curve elliptic.Curve
	}{
		{GroupP256, elliptic.P256()},
		{GroupP384, elliptic.P384()},
		{GroupP521, elliptic.P521()},
	} {
		scalar, element := makeCommit(t, g.curve)
		if d, _ := f.CheckCommit(testMACs[0], g.id, scalar, element, nil); d != DecisionAccept {
			t.Fatalf("group %d: got %v, want accept", g.id, d)
		}
	}
}
