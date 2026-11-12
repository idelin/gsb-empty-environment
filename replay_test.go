package netsec

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
)

// refModel 是按题目原文直接实现的小位宽参考模型，使用独立的朴素整数算术，
// 不共享被测代码的任何实现。小位宽（B<=16）下数学值远小于 2^64，不会溢出。
type refModel struct {
	bits      uint
	lowBits   uint
	span      uint64 // 2^L
	maxSeq    uint64 // 2^B - 1
	width     uint64
	started   bool
	highest   uint64
	delivered uint64 // 以 highest 为偏移 0 的位图，bit k 表示 highest-k 已交付
}

func newRefModel(bits, lowBits uint, width uint64) *refModel {
	return &refModel{
		bits:    bits,
		lowBits: lowBits,
		span:    uint64(1) << lowBits,
		maxSeq:  uint64(1)<<bits - 1,
		width:   width,
	}
}

// refReconstruct 枚举所有与 low 同低位的候选（含越过上限者），
// 取与 next=highest+1 最近者，并列取较小者；选中越界候选即 ErrOverflow。
func (m *refModel) refReconstruct(low uint64) (uint64, error) {
	if !m.started {
		return low, nil
	}
	if m.highest == m.maxSeq {
		return 0, ErrExhausted
	}
	next := m.highest + 1
	best, bestDist := uint64(0), uint64(0)
	bestInRange, first := false, true
	consider := func(cand uint64, inRange bool) {
		d := absDiffU64(cand, next)
		if first || d < bestDist || (d == bestDist && cand < best) {
			best, bestDist, bestInRange = cand, d, inRange
			first = false
		}
	}
	for k := uint64(0); ; k++ {
		cand := k*m.span + low
		inRange := cand <= m.maxSeq
		consider(cand, inRange)
		if !inRange { // 再往后的候选距离只会更大
			break
		}
	}
	if !bestInRange {
		return 0, ErrOverflow
	}
	return best, nil
}

func absDiffU64(a, b uint64) uint64 {
	if a >= b {
		return a - b
	}
	return b - a
}

func (m *refModel) step(low uint64, verifyOK bool) (uint64, error) {
	seq, err := m.refReconstruct(low)
	if err != nil {
		return 0, err
	}
	if m.started {
		edge := uint64(0)
		if m.highest+1 >= m.width {
			edge = m.highest - m.width + 1
		}
		if seq < edge {
			return seq, ErrTooOld
		}
	}
	if m.isDelivered(seq) {
		return seq, ErrReplay
	}
	if !verifyOK {
		return seq, ErrSignature
	}
	if !m.started {
		m.started = true
		m.highest = seq
		m.delivered = 1
		return seq, nil
	}
	if seq > m.highest {
		delta := seq - m.highest
		if delta >= m.width {
			m.delivered = 0
		} else {
			m.delivered <<= delta
			m.delivered &= (uint64(1) << m.width) - 1
		}
		m.highest = seq
	}
	if seq == m.highest {
		m.delivered |= 1 // 新最高号落在偏移 0
	} else {
		m.delivered |= uint64(1) << (m.highest - seq) // 迟到包按偏移置位
	}
	return seq, nil
}

func (m *refModel) isDelivered(seq uint64) bool {
	if !m.started || seq > m.highest {
		return false
	}
	off := m.highest - seq
	if off >= m.width {
		return false
	}
	return m.delivered&(uint64(1)<<off) != 0
}

func (m *refModel) fingerprint() string {
	return fmt.Sprintf("%v|%d|%d", m.started, m.highest, m.delivered)
}

// coreFingerprint 与参考模型同构地描述被测内核状态。
func coreFingerprint[T uint8 | uint16 | uint32 | uint64](c *replayCore[T]) string {
	var bits T
	if c.started {
		for i, word := range c.bitmap {
			bits |= T(word) << (i * int(c.wordBits))
		}
	}
	return fmt.Sprintf("%v|%d|%d", c.started, c.highest, bits)
}

func errClass(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrTooOld):
		return "tooold"
	case errors.Is(err, ErrReplay):
		return "replay"
	case errors.Is(err, ErrSignature):
		return "sig"
	case errors.Is(err, ErrOverflow):
		return "overflow"
	case errors.Is(err, ErrExhausted):
		return "exhausted"
	default:
		return "unknown:" + err.Error()
	}
}

// TestExhaustiveSmallWidths 用小位宽穷举所有可达事件序列（成功/失败验签 ×
// 所有可能低序号），逐条与 big.Int 参考模型比对返回值、错误类别和完整内部状态。
func TestExhaustiveSmallWidths(t *testing.T) {
	type cfg struct {
		name    string
		bits    uint
		lowBits uint
		widths  []uint64
	}
	cases := []cfg{
		{"u8-l2", 8, 2, []uint64{1, 2, 3, 4}},
		{"u8-l3", 8, 3, []uint64{1, 5, 7, 8}},
		{"u16-l3", 16, 3, []uint64{1, 4, 9}},
		{"u16-l4", 16, 4, []uint64{1, 7, 15}},
	}
	const maxDepth = 5
	for _, tc := range cases {
		for _, w := range tc.widths {
			t.Run(fmt.Sprintf("%s-w%d", tc.name, w), func(t *testing.T) {
				switch tc.bits {
				case 8:
					runDFS[uint8](t, tc.bits, tc.lowBits, uint8(w), maxDepth)
				case 16:
					runDFS[uint16](t, tc.bits, tc.lowBits, uint16(w), maxDepth)
				default:
					t.Fatalf("unsupported bits %d", tc.bits)
				}
			})
		}
	}
}

func runDFS[T uint8 | uint16](t *testing.T, bits, lowBits uint, width T, depth int) {
	t.Helper()
	span := T(1) << lowBits
	visited := make(map[string]bool)

	var dfs func(c replayCore[T], r *refModel, d int, path string)
	dfs = func(c replayCore[T], r *refModel, d int, path string) {
		fp := coreFingerprint(&c)
		if fp != r.fingerprint() {
			t.Fatalf("state drift at %s:\ncore=%s\nref =%s", path, fp, r.fingerprint())
		}
		if d == 0 {
			return
		}
		key := fp + fmt.Sprint(d)
		if visited[key] {
			return
		}
		visited[key] = true
		for low := T(0); low < span; low++ {
			for _, ok := range []bool{true, false} {
				c2 := cloneCore(&c)
				r2 := cloneRef(r)
				gotSeq, gotErr := c2.step(low, ok)
				wantSeq, wantErr := r2.step(uint64(low), ok)
				if errClass(gotErr) != errClass(wantErr) {
					t.Fatalf("low=%d ok=%v err mismatch: core=%v ref=%v\nstate=%s",
						low, ok, gotErr, wantErr, fp)
				}
				if gotErr == nil && gotSeq != T(wantSeq) {
					t.Fatalf("low=%d seq mismatch: core=%d ref=%d", low, gotSeq, wantSeq)
				}
				if coreFingerprint(c2) != r2.fingerprint() {
					t.Fatalf("post-step state drift at %s>(%d,%v):\ncore=%s\nref =%s",
						path, low, ok, coreFingerprint(c2), r2.fingerprint())
				}
				dfs(*c2, r2, d-1, fmt.Sprintf("%s>(%d,%v)", path, low, ok))
			}
		}
	}

	dfs(newReplayCore[T](bits, lowBits, width), newRefModel(bits, lowBits, uint64(width)), depth, "root")
}

func cloneCore[T uint8 | uint16 | uint32 | uint64](c *replayCore[T]) *replayCore[T] {
	bm := make([]T, len(c.bitmap))
	copy(bm, c.bitmap)
	cp := *c
	cp.bitmap = bm
	return &cp
}

func cloneRef(r *refModel) *refModel {
	cp := *r
	return &cp
}

// seededFilter 用指定最高序号和已交付偏移构造过滤器，专测 64 位边界。
func seededFilter(t *testing.T, highest uint64, deliveredOffsets []uint64) *ReplayFilter {
	t.Helper()
	const width = 16
	f, err := NewReplayFilter(width, func(seq uint64, _ any) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	c := &f.core
	c.started = true
	c.highest = highest
	for _, off := range deliveredOffsets {
		if off >= width {
			t.Fatalf("offset %d beyond width", off)
		}
		c.setBit(uint64(off))
	}
	return f
}

// TestNamedSmallWidthScenarios 用 B=8、L=3（跨度 8）的小内核显式覆盖题目点名的场景。
func TestNamedSmallWidthScenarios(t *testing.T) {
	t.Run("wrap-around", func(t *testing.T) {
		c := newReplayCore[uint8](8, 3, 4)
		must := func(low uint8, want uint8) {
			t.Helper()
			got, err := c.step(low, true)
			if err != nil || got != want {
				t.Fatalf("low=%d got=(%d,%v), want %d", low, got, err, want)
			}
		}
		must(6, 6)
		must(7, 7)
		must(0, 8) // 跨 8 回绕：低 0 重构为完整号 8
		must(1, 9) // 低 1 重构为完整号 9
	})

	t.Run("window-left-edge", func(t *testing.T) {
		c := newReplayCore[uint8](8, 3, 4)
		c.step(0, true)
		c.step(5, true) // highest=5，左端 = 5-4+1 = 2
		if seq, err := c.step(2, true); err != nil || seq != 2 {
			t.Fatalf("left-edge 2: got=(%d,%v), want accepted 2", seq, err)
		}
		if _, err := c.step(2, true); !errors.Is(err, ErrReplay) {
			t.Fatalf("repeat 2: got %v, want ErrReplay", err)
		}
	})

	t.Run("ancient-packet", func(t *testing.T) {
		c := newReplayCore[uint8](8, 3, 3)
		c.step(0, true)
		c.step(7, true) // highest=7，左端 = 7-3+1 = 5
		// 低 4 的最近候选是旧纪元的 4（新纪元候选 12 更远），4 < 左端 5，判极旧。
		if seq, err := c.step(4, true); !errors.Is(err, ErrTooOld) {
			t.Fatalf("ancient 4: got=(%d,%v), want ErrTooOld", seq, err)
		}
		// 极旧包被拒后窗口不动，左端上的 2 仍可补交付。
		if _, err := c.step(5, true); err != nil {
			t.Fatalf("left-edge 5 after ancient reject: %v", err)
		}
	})

	t.Run("two-candidates-finish-together", func(t *testing.T) {
		// next=4，低 4 的两个候选 4 与 12 到 next 距离分别为 0 与 8，
		// 取近者 4；再用低 0 验证跨回绕候选 8 与极旧候选 0 之间选 8。
		c := newReplayCore[uint8](8, 3, 6)
		c.step(3, true) // highest=3，next=4
		if seq, err := c.step(4, true); err != nil || seq != 4 {
			t.Fatalf("candidate pick: got=(%d,%v), want 4", seq, err)
		}
		c2 := newReplayCore[uint8](8, 3, 6)
		c2.step(7, true) // highest=7，next=8；低 0 的候选为 0（旧）与 8（新）
		if seq, err := c2.step(0, true); err != nil || seq != 8 {
			t.Fatalf("wrap candidate: got=(%d,%v), want 8", seq, err)
		}
	})
}

func TestReconstruct64Edges(t *testing.T) {
	max := ^uint64(0)

	t.Run("wrap-around", func(t *testing.T) {
		// highest=2^32-5；低 2 应重构为 2^32+2（跨 32 位回绕）。
		f := seededFilter(t, span32-5, []uint64{0})
		seq, err := f.Reconstruct(2)
		if err != nil || seq != span32+2 {
			t.Fatalf("got seq=%d err=%v, want %d", seq, err, span32+2)
		}
	})

	t.Run("tie-prefers-smaller", func(t *testing.T) {
		// 让基准 base 恰比 next 旧 2^31，则 base 与上邻 base+2^32 到 next 等距，
		// 按“并列取较小者”应选 base，而不是跳到上邻。
		next := span32 / 2 // 0x80000000
		low := uint32(0)   // base=0，距 next 为 2^31；上邻=2^32，距 next 也是 2^31
		f := seededFilter(t, next-1, nil)
		seq, err := f.Reconstruct(low)
		if err != nil || seq != 0 {
			t.Fatalf("got seq=%#x err=%v, want smaller candidate 0", seq, err)
		}
	})

	t.Run("overflow-nearest-rejected", func(t *testing.T) {
		// next=MaxUint64-255，low=0x100：基准旧 511，越界上邻仅远 1，必须拒绝。
		f := seededFilter(t, max-256, nil)
		if _, err := f.Reconstruct(0x100); !errors.Is(err, ErrOverflow) {
			t.Fatalf("got err=%v, want ErrOverflow", err)
		}
	})

	t.Run("exhausted", func(t *testing.T) {
		f := seededFilter(t, max, []uint64{0})
		if _, err := f.Reconstruct(0); !errors.Is(err, ErrExhausted) {
			t.Fatalf("got err=%v, want ErrExhausted", err)
		}
		if _, err := f.Verify(0, nil); !errors.Is(err, ErrExhausted) {
			t.Fatalf("Verify got err=%v, want ErrExhausted", err)
		}
	})

	t.Run("max-deliverable", func(t *testing.T) {
		// 上限号 MaxUint64 本身仍可交付，之后才耗尽。
		f := seededFilter(t, max-1, []uint64{0})
		seq, err := f.Verify(uint32(max&0xFFFFFFFF), nil)
		if err != nil || seq != max {
			t.Fatalf("got seq=%d err=%v, want MaxUint64", seq, err)
		}
		if _, err := f.Verify(0, nil); !errors.Is(err, ErrExhausted) {
			t.Fatalf("post-max got err=%v, want ErrExhausted", err)
		}
	})
}

func TestMultiwordBitmapWindow(t *testing.T) {
	// 宽度 200 跨越多个 64 位字，验证多字位图的滑动、置位与截断。
	const width = 200
	f, err := NewReplayFilter(width, func(seq uint64, _ any) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	// 先交付 0，再跳到 300：0 已滑出（300-200+1=101 为左端）。
	if _, err := f.Verify(0, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Verify(300, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Verify(0, nil); !errors.Is(err, ErrTooOld) {
		t.Fatalf("ancient seq 0: got %v, want ErrTooOld", err)
	}
	// 左端 101 与窗口内迟到序号 250 都应能补交付，且各自只交付一次。
	for _, seq := range []uint64{101, 250, 300} {
		_, err := f.Verify(uint32(seq), nil)
		if seq == 300 {
			if !errors.Is(err, ErrReplay) {
				t.Fatalf("seq %d repeat: got %v, want ErrReplay", seq, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("late deliver seq %d: %v", seq, err)
		}
	}
	if _, err := f.Verify(250, nil); !errors.Is(err, ErrReplay) {
		t.Fatalf("seq 250 repeat: got %v, want ErrReplay", err)
	}
}

func TestWindowLeftEdgeAndTooOld(t *testing.T) {
	const width = 8
	f, err := NewReplayFilter(width, func(seq uint64, _ any) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	// 先交付 0，再直接跳到 10：窗口左端 = 10-8+1 = 3，序号 1、2 已滑出。
	if _, err := f.Verify(0, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Verify(10, nil); err != nil {
		t.Fatal(err)
	}
	// 左端（含）上的 3 尚未交付，应作为迟到包补交付。
	if seq, err := f.Verify(3, nil); err != nil || seq != 3 {
		t.Fatalf("left-edge seq 3: seq=%d err=%v", seq, err)
	}
	// 同序号再次到达即重放。
	if _, err := f.Verify(3, nil); !errors.Is(err, ErrReplay) {
		t.Fatalf("repeat seq 3: got %v, want ErrReplay", err)
	}
	// 2 在左端之外，是极旧包。
	if _, err := f.Verify(2, nil); !errors.Is(err, ErrTooOld) {
		t.Fatalf("ancient seq 2: got %v, want ErrTooOld", err)
	}
}

func TestSignatureFailureDoesNotMoveWindow(t *testing.T) {
	const width = 4
	var reject atomic.Bool
	f, err := NewReplayFilter(width, func(seq uint64, _ any) error {
		if reject.Load() {
			return errors.New("bad signature")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Verify(10, nil); err != nil {
		t.Fatalf("first deliver failed: %v", err)
	}
	reject.Store(true)
	// 一个更靠前的新序号验签失败：窗口不得推进。
	if _, err := f.Verify(11, nil); !errors.Is(err, ErrSignature) {
		t.Fatalf("got %v, want ErrSignature", err)
	}
	reject.Store(false)
	// 失败后同序号仍可重新验签并成功，且只交付一次。
	if seq, err := f.Verify(11, nil); err != nil || seq != 11 {
		t.Fatalf("retry seq 11: seq=%d err=%v", seq, err)
	}
	if _, err := f.Verify(11, nil); !errors.Is(err, ErrReplay) {
		t.Fatalf("duplicate after success: got %v, want ErrReplay", err)
	}
	// 窗口未因那次失败而移动：10 仍记录在位图中，重复到达判重放。
	if _, err := f.Verify(10, nil); !errors.Is(err, ErrReplay) {
		t.Fatalf("seq 10 not retained: got %v, want ErrReplay", err)
	}
}
