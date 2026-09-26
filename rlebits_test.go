package rlebits

import (
	"math/rand"
	"testing"
)

// ---------- 朴素模型 ----------

type model struct{ b []byte }

func (m *model) flip(l, r int) {
	for i := l; i < r; i++ {
		m.b[i] ^= 1
	}
}
func (m *model) insert(pos int, v byte, n int) {
	tail := append([]byte{}, m.b[pos:]...)
	m.b = m.b[:pos]
	for i := 0; i < n; i++ {
		m.b = append(m.b, v)
	}
	m.b = append(m.b, tail...)
}
func (m *model) delete(l, r int) { m.b = append(m.b[:l], m.b[r:]...) }
func (m *model) rank1(p int) int {
	c := 0
	for i := 0; i < p; i++ {
		c += int(m.b[i])
	}
	return c
}

// ---------- 不变量校验 ----------

func checkInvariants(t *testing.T, b *Bits) {
	t.Helper()
	for i, l := range b.runs {
		if l == 0 {
			t.Fatalf("zero-length run at index %d", i)
		}
	}
	if len(b.pos) != len(b.runs)+1 || len(b.ones) != len(b.runs)+1 {
		t.Fatalf("index length mismatch: runs=%d pos=%d ones=%d",
			len(b.runs), len(b.pos), len(b.ones))
	}
	var posSum, onesSum uint64
	for i, l := range b.runs {
		if b.pos[i] != posSum || b.ones[i] != onesSum {
			t.Fatalf("prefix index broken at run %d", i)
		}
		posSum += l
		if b.runValue(i) == 1 {
			onesSum += l
		}
	}
	if b.pos[len(b.runs)] != posSum || b.ones[len(b.runs)] != onesSum {
		t.Fatal("prefix index tail mismatch")
	}
}

func checkEqual(t *testing.T, b *Bits, m *model) {
	t.Helper()
	checkInvariants(t, b)
	got := b.Expand()
	if len(got) != len(m.b) {
		t.Fatalf("length mismatch: got %d want %d", len(got), len(m.b))
	}
	for i := range got {
		if got[i] != m.b[i] {
			t.Fatalf("bit %d: got %d want %d\ngot:  %v\nwant: %v", i, got[i], m.b[i], got, m.b)
		}
	}
	for p := 0; p <= len(m.b); p++ {
		if uint64(m.rank1(p)) != b.Rank1(uint64(p)) {
			t.Fatalf("Rank1(%d) mismatch", p)
		}
		if uint64(p-m.rank1(p)) != b.Rank0(uint64(p)) {
			t.Fatalf("Rank0(%d) mismatch", p)
		}
	}
	ones := m.rank1(len(m.b))
	for k := 0; k < ones; k++ {
		pos, ok := b.Select1(uint64(k))
		if !ok || m.b[pos] != 1 || m.rank1(int(pos)) != k {
			t.Fatalf("Select1(%d)=%d,%v wrong", k, pos, ok)
		}
	}
	for k := 0; k < len(m.b)-ones; k++ {
		pos, ok := b.Select0(uint64(k))
		if !ok || m.b[pos] != 0 || int(pos)-m.rank1(int(pos)) != k {
			t.Fatalf("Select0(%d)=%d,%v wrong", k, pos, ok)
		}
	}
}

// ---------- 指定场景 ----------

func TestFlipTwiceRestores(t *testing.T) {
	src := "0001110000111100001111"
	orig := FromString(src)
	b := FromString(src)
	b.Flip(2, 19)
	b.Flip(2, 19)
	if b.String() != src {
		t.Fatalf("flip twice not identity: got %s want %s", b.String(), src)
	}
	if b.NumRuns() != orig.NumRuns() {
		t.Fatalf("run count changed: got %d want %d", b.NumRuns(), orig.NumRuns())
	}
	// 全区间翻转两次
	b.Flip(0, b.Len())
	b.Flip(0, b.Len())
	if b.String() != src {
		t.Fatalf("full flip twice not identity: %s", b.String())
	}
}

func TestDeleteAcrossRunsNoZeroRuns(t *testing.T) {
	// 6 段，删除横跨中间 4 段
	b := FromString("00011001010111")
	m := &model{b: b.Expand()}
	b.Delete(2, 12)
	m.delete(2, 12)
	checkEqual(t, b, m)
	// 删除奇数段触发交界合并：111|000|111 -> 删 000 后两段 1 合并
	b2 := FromString("111000111")
	m2 := &model{b: b2.Expand()}
	b2.Delete(3, 6)
	m2.delete(3, 6)
	checkEqual(t, b2, m2)
	if b2.NumRuns() != 1 {
		t.Fatalf("expected single merged run, got %d", b2.NumRuns())
	}
	// 删空
	b3 := FromString("010101")
	m3 := &model{b: b3.Expand()}
	b3.Delete(0, 6)
	m3.delete(0, 6)
	checkEqual(t, b3, m3)
}

func TestInsertAtBoundaryNoDuplicate(t *testing.T) {
	// 在 0/1 段边界处插入 0：应延长左段而非新建段
	b := FromString("000111")
	b.Insert(3, 0, 2)
	if b.String() != "00000111" {
		t.Fatalf("got %s", b.String())
	}
	if b.NumRuns() != 2 {
		t.Fatalf("expected 2 runs, got %d", b.NumRuns())
	}
	// 同一边界插入 1：应延长右段
	b = FromString("000111")
	b.Insert(3, 1, 2)
	if b.String() != "00011111" || b.NumRuns() != 2 {
		t.Fatalf("got %s runs=%d", b.String(), b.NumRuns())
	}
	// 头部边界插入异值：新段且 start 翻转
	b = FromString("111")
	b.Insert(0, 0, 2)
	if b.String() != "00111" || b.NumRuns() != 2 {
		t.Fatalf("got %s runs=%d", b.String(), b.NumRuns())
	}
	// 段内部插入异值：一分为三
	b = FromString("0000")
	b.Insert(2, 1, 3)
	if b.String() != "0011100" || b.NumRuns() != 3 {
		t.Fatalf("got %s runs=%d", b.String(), b.NumRuns())
	}
	// 空序列插入
	b = New()
	b.Insert(0, 1, 4)
	if b.String() != "1111" {
		t.Fatalf("got %s", b.String())
	}
}

func TestRankSelect(t *testing.T) {
	b := FromString("1001110000110101")
	m := &model{b: b.Expand()}
	checkEqual(t, b, m)
	if _, ok := b.Select1(100); ok {
		t.Fatal("Select1 out of range should fail")
	}
	if _, ok := b.Select0(100); ok {
		t.Fatal("Select0 out of range should fail")
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	for _, s := range []string{"", "0", "1", "000111000", "10", "0001110000111100001111"} {
		b := FromString(s)
		d, err := Decode(b.Encode())
		if err != nil {
			t.Fatalf("Decode(%q): %v", s, err)
		}
		if d.String() != b.String() || d.NumRuns() != b.NumRuns() {
			t.Fatalf("round trip mismatch for %q", s)
		}
	}
}

func TestEncodedSizeBeatsRaw(t *testing.T) {
	// 10000 个 0 + 10000 个 1：原始 2500 字节，RLE 应远小于它
	b := New()
	b.Insert(0, 0, 10000)
	b.Insert(10000, 1, 10000)
	if b.EncodedSize() >= int(b.RawSize()) {
		t.Fatalf("encoded %d bytes >= raw %d bytes", b.EncodedSize(), b.RawSize())
	}
	t.Logf("len=%d runs=%d encoded=%dB raw=%dB index=%dB",
		b.Len(), b.NumRuns(), b.EncodedSize(), b.RawSize(), b.IndexBytes())
}

// ---------- 随机操作对照 ----------

func TestRandomOpsAgainstModel(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 50; trial++ {
		n := 1 + rng.Intn(120)
		bits := make([]byte, n)
		for i := range bits {
			bits[i] = byte(rng.Intn(2))
		}
		b := FromBits(bits)
		m := &model{b: append([]byte{}, bits...)}
		for op := 0; op < 200; op++ {
			L := len(m.b)
			switch rng.Intn(4) {
			case 0:
				if L == 0 {
					continue
				}
				l := rng.Intn(L)
				r := l + rng.Intn(L-l) + 1
				b.Flip(uint64(l), uint64(r))
				m.flip(l, r)
			case 1:
				pos := rng.Intn(L + 1)
				v := byte(rng.Intn(2))
				c := 1 + rng.Intn(8)
				b.Insert(uint64(pos), v, uint64(c))
				m.insert(pos, v, c)
			case 2:
				if L == 0 {
					continue
				}
				l := rng.Intn(L)
				r := l + rng.Intn(L-l) + 1
				b.Delete(uint64(l), uint64(r))
				m.delete(l, r)
			case 3: // 翻转两次应还原
				if L == 0 {
					continue
				}
				l := rng.Intn(L)
				r := l + rng.Intn(L-l) + 1
				snap := b.String()
				b.Flip(uint64(l), uint64(r))
				b.Flip(uint64(l), uint64(r))
				if b.String() != snap {
					t.Fatalf("trial %d op %d: flip twice not identity", trial, op)
				}
			}
			checkEqual(t, b, m)
		}
	}
}
