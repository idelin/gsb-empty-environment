package grid

import (
	"math"
	"math/rand"
	"testing"
)

// 验收坐标：北京。
var beijing = LatLng{Lat: 39.9042, Lng: 116.4074}

func TestEisensteinExpandCollapseRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 20000; i++ {
		a := axial{rng.Int63n(2_000_000) - 1_000_000, rng.Int63n(2_000_000) - 1_000_000}
		if got := collapse(expand(a)); got != a {
			t.Fatalf("collapse(expand(%v)) = %v", a, got)
		}
	}
}

func TestEisensteinCollapseExpandDigits(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 20000; i++ {
		n := rng.Intn(15) + 1
		digits := make([]int, n)
		digits[0] = rng.Intn(6) + 1 // 首位非零，保证规范化
		for j := 1; j < n; j++ {
			digits[j] = rng.Intn(7)
		}
		got := expand(collapse(digits))
		if len(got) != len(digits) {
			t.Fatalf("expand(collapse(%v)) = %v", digits, got)
		}
		for j := range digits {
			if got[j] != digits[j] {
				t.Fatalf("expand(collapse(%v)) = %v", digits, got)
			}
		}
	}
}

func TestFromLatLngResolution8(t *testing.T) {
	h, err := FromLatLng(beijing, 8)
	if err != nil {
		t.Fatal(err)
	}
	if h.Res() != 8 {
		t.Fatalf("res = %d, want 8", h.Res())
	}
	if !h.IsValid() {
		t.Fatal("index not valid")
	}
	s := h.String()
	for _, c := range s {
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
		if !isHex {
			t.Fatalf("non-hex char in %q", s)
		}
	}
	back, err := Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	if back != h {
		t.Fatalf("parse round trip: %x != %x", uint64(back), uint64(h))
	}
}

func TestCenterRoundTripRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	// res 8 单元中心间距约 5.3km，中心到顶点约 3.1km，留足余量。
	const maxErrM = 8000
	for i := 0; i < 2000; i++ {
		ll := LatLng{Lat: rng.Float64()*176 - 88, Lng: rng.Float64()*360 - 180}
		h, err := FromLatLng(ll, 8)
		if err != nil {
			t.Fatal(err)
		}
		if d := HaversineM(ll, Center(h)); d > maxErrM {
			t.Fatalf("round trip error %.0fm for %v", d, ll)
		}
	}
}

func TestParentChildHierarchy(t *testing.T) {
	h, err := FromLatLng(beijing, 10)
	if err != nil {
		t.Fatal(err)
	}
	// 父链：digit 前缀一致，分辨率递减。
	for r := 0; r <= 10; r++ {
		p, err := Parent(h, r)
		if err != nil {
			t.Fatal(err)
		}
		if p.Res() != r || p.BaseCell() != h.BaseCell() {
			t.Fatalf("parent res/base mismatch at %d", r)
		}
		for i := 1; i <= r; i++ {
			if p.Digit(i) != h.Digit(i) {
				t.Fatalf("digit %d mismatch at res %d", i, r)
			}
		}
	}
	if _, err := Parent(h, 11); err == nil {
		t.Fatal("expected error for res > current")
	}
	// 子单元：parent(child) == 自身。
	p5, _ := Parent(h, 5)
	c, err := Child(p5, h.Digit(6))
	if err != nil {
		t.Fatal(err)
	}
	if back, _ := Parent(c, 5); back != p5 {
		t.Fatal("parent(child) != self")
	}
	children, err := Children(p5)
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 7 {
		t.Fatalf("children = %d, want 7", len(children))
	}
	seen := map[Index]bool{}
	for _, ch := range children {
		if seen[ch] {
			t.Fatal("duplicate child")
		}
		seen[ch] = true
		if back, _ := Parent(ch, 5); back != p5 {
			t.Fatal("child parent mismatch")
		}
	}
	// 子单元中心必须落在父单元附近（层级地理包含性）：
	// 偏移上界为一个子级格距（v_d 为单位向量）。
	maxChildOffset := resScale(6) * EarthRadiusM * 1.2
	if d := HaversineM(Center(c), Center(p5)); d > maxChildOffset {
		t.Fatalf("child too far from parent: %.0fm > %.0fm", d, maxChildOffset)
	}
}

func TestKRingSizes(t *testing.T) {
	h, err := FromLatLng(beijing, 8)
	if err != nil {
		t.Fatal(err)
	}
	want := []int{1, 7, 19, 37, 61, 91}
	for k, w := range want {
		if got := len(KRing(h, k)); got != w {
			t.Fatalf("KRing(k=%d) = %d, want %d", k, got, w)
		}
		if got := KRingSize(k); got != w {
			t.Fatalf("KRingSize(%d) = %d, want %d", k, got, w)
		}
		if k > 0 {
			if got := len(Ring(h, k)); got != 6*k {
				t.Fatalf("Ring(k=%d) = %d, want %d", k, got, 6*k)
			}
		}
	}
}

func TestKRingNoDuplicates(t *testing.T) {
	h, _ := FromLatLng(beijing, 8)
	seen := map[Index]bool{}
	for _, c := range KRing(h, 4) {
		if seen[c] {
			t.Fatalf("duplicate cell %s", c)
		}
		seen[c] = true
	}
}

func TestNeighborsSymmetric(t *testing.T) {
	h, _ := FromLatLng(beijing, 8)
	for _, nb := range Neighbors(h) {
		found := false
		for _, back := range Neighbors(nb) {
			if back == h {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("neighbor relation not symmetric for %s", nb)
		}
	}
}

func TestGridDistance(t *testing.T) {
	h, _ := FromLatLng(beijing, 8)
	if d := GridDistance(h, h); d != 0 {
		t.Fatalf("self distance = %d", d)
	}
	for _, nb := range Neighbors(h) {
		if d := GridDistance(h, nb); d != 1 {
			t.Fatalf("neighbor distance = %d", d)
		}
	}
	// 环 k 上的单元距离恰为 k。
	for k := 1; k <= 3; k++ {
		for _, c := range Ring(h, k) {
			if d := GridDistance(h, c); d != k {
				t.Fatalf("ring %d cell distance = %d", k, d)
			}
		}
	}
}

// 验收：GPS -> res8 十六进制编码，半径 3 环覆盖连续闭合无空洞。
func TestAcceptanceGPSKRingCoverage(t *testing.T) {
	h, err := FromLatLng(beijing, 8)
	if err != nil {
		t.Fatal(err)
	}
	hexStr := h.String()
	parsed, err := Parse(hexStr)
	if err != nil || parsed != h {
		t.Fatalf("hex string round trip failed: %q", hexStr)
	}
	cells := KRing(h, 3)
	if len(cells) != 37 {
		t.Fatalf("k-ring 3 size = %d, want 37", len(cells))
	}
	// 全部同分辨率、同基单元、无重复。
	seen := map[Index]bool{}
	for _, c := range cells {
		if c.Res() != 8 {
			t.Fatalf("cell res = %d", c.Res())
		}
		if seen[c] {
			t.Fatal("duplicate cell")
		}
		seen[c] = true
	}
	// 拓扑闭合校验：边界边构成单一闭合环，边数 = 12*(3+1)-6 = 42。
	n, ok := VerifyContiguousClosed(cells)
	if !ok {
		t.Fatal("coverage not contiguous/closed")
	}
	if n != 42 {
		t.Fatalf("boundary edges = %d, want 42", n)
	}
	// 每个非中心单元至少有一个邻居在集合内（无孤立单元）。
	inSet := seen
	for _, c := range cells {
		if c == h {
			continue
		}
		connected := false
		for _, nb := range Neighbors(c) {
			if inSet[nb] {
				connected = true
				break
			}
		}
		if !connected {
			t.Fatalf("isolated cell %s", c)
		}
	}
}

// 空洞检测：挖掉一个内部单元后，校验必须失败（产生内环）。
func TestCoverageDetectsHole(t *testing.T) {
	h, _ := FromLatLng(beijing, 8)
	cells := KRing(h, 3)
	// 挖掉中心 -> 产生空洞。
	var holed []Index
	for _, c := range cells {
		if c != h {
			holed = append(holed, c)
		}
	}
	if _, ok := VerifyContiguousClosed(holed); ok {
		t.Fatal("hole not detected")
	}
	// 挖掉一个 1 环邻居 -> 空洞。
	nb := Neighbors(h)[0]
	var holed2 []Index
	for _, c := range cells {
		if c != nb {
			holed2 = append(holed2, c)
		}
	}
	if _, ok := VerifyContiguousClosed(holed2); ok {
		t.Fatal("hole not detected (ring-1)")
	}
	// 重复单元 -> 失败。
	dup := append(append([]Index{}, cells...), cells[5])
	if _, ok := VerifyContiguousClosed(dup); ok {
		t.Fatal("duplicate not detected")
	}
}

// 全球扫描：任意点的 digit 展开不得超过分辨率位数（验证 baseCellScale 足够，
// 编码永不触发截断兜底）。
func TestEncodeSweepNeverTruncates(t *testing.T) {
	maxLen := 0
	for lat := -88.0; lat <= 88; lat += 1 {
		for lng := -180.0; lng < 180; lng += 1 {
			_, a, err := latLngToAxial(LatLng{Lat: lat, Lng: lng}, MaxRes)
			if err != nil {
				t.Fatal(err)
			}
			if n := len(expand(a)); n > MaxRes {
				t.Fatalf("expansion len %d > %d at (%v,%v)", n, MaxRes, lat, lng)
			} else if n > maxLen {
				maxLen = n
			}
		}
	}
	t.Logf("max expansion length at res %d: %d", MaxRes, maxLen)
}

// 索引编码/解码互逆：随机单元 decode->encode 恒等。
func TestIndexRoundTripRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	for i := 0; i < 5000; i++ {
		res := rng.Intn(MaxRes + 1)
		ll := LatLng{Lat: rng.Float64()*176 - 88, Lng: rng.Float64()*360 - 180}
		h, err := FromLatLng(ll, res)
		if err != nil {
			t.Fatal(err)
		}
		// 中心点重新编码：同面必须回到同一单元；
		// 跨面（面边缘单元，瓦片超出面边界）时断言地理邻近。
		h2, err := FromLatLng(Center(h), res)
		if err != nil {
			t.Fatal(err)
		}
		if h2.BaseCell() == h.BaseCell() {
			if h2 != h {
				t.Fatalf("re-encode mismatch: %s vs %s", h, h2)
			}
		} else {
			maxD := resScale(res) * EarthRadiusM * 1.5
			if d := HaversineM(Center(h), Center(h2)); d > maxD {
				t.Fatalf("cross-face re-encode too far: %.0fm > %.0fm", d, maxD)
			}
		}
	}
}

func TestInvalidInputs(t *testing.T) {
	if _, err := FromLatLng(LatLng{Lat: 91}, 8); err == nil {
		t.Fatal("expected error for lat > 90")
	}
	if _, err := FromLatLng(LatLng{Lat: math.NaN()}, 8); err == nil {
		t.Fatal("expected error for NaN")
	}
	if _, err := FromLatLng(beijing, 16); err == nil {
		t.Fatal("expected error for res > 15")
	}
	if _, err := Parse("zzzz"); err == nil {
		t.Fatal("expected parse error")
	}
	if _, err := Parse("ffffffffffffffff"); err == nil {
		t.Fatal("expected invalid index error")
	}
}
