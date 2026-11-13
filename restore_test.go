package netsec

import (
	"errors"
	"math"
	"testing"
)

const (
	tid      = uint64(7)
	execBase = uint64(0x400000)
	execLen  = uint64(0x1000)
	stackLo  = uint64(0x800000)
	stackLen = uint64(0x2000)
	userMask = uint64(0x0F) // 仅低 4 位允许用户改动，高位为保留权限位
)

func newHost(t *testing.T) *Host {
	t.Helper()
	h := NewHost()
	if err := h.RegisterExecRange(execBase, execLen); err != nil {
		t.Fatalf("RegisterExecRange: %v", err)
	}
	return h
}

func defaultOpts() FrameOptions {
	return FrameOptions{
		StackBase:   stackLo,
		StackLen:    stackLen,
		MutableRegs: []int{0, 1},
		UserMask:    userMask,
	}
}

func baseFrame() Frame {
	var f Frame
	f.ThreadID = tid
	for i := range f.Regs {
		f.Regs[i] = uint64(0x100 + i)
	}
	f.PC = execBase + 0x40
	f.SP = stackLo + 0x100
	f.Flags = 0xA0 // 含保留权限位
	return f
}

// seqTracker 按进入顺序维护宿主赋予的单调序号。
type seqTracker struct{ next uint64 }

func (s *seqTracker) alloc() uint64 { v := s.next; s.next++; return v }

func TestNestedSignalsRestoreInnerThenOuter(t *testing.T) {
	h := newHost(t)
	var trk seqTracker

	outer := baseFrame()
	outer.Seq = trk.alloc()
	outerDigest, err := h.EnterSignal(outer, defaultOpts())
	if err != nil {
		t.Fatalf("enter outer: %v", err)
	}

	inner := baseFrame()
	inner.Seq = trk.alloc()
	inner.Parent = outerDigest
	inner.Regs[0] = 0xFFFF // 进入内层信号前的现场
	if _, err := h.EnterSignal(inner, defaultOpts()); err != nil {
		t.Fatalf("enter inner: %v", err)
	}
	if got := h.ActiveDepth(tid); got != 2 {
		t.Fatalf("depth = %d, want 2", got)
	}

	// 恢复内层帧：修改允许寄存器 0、1，其余保持影子值。
	ri := inner
	ri.Regs[0] = 0xDEAD
	ri.Regs[1] = 0xBEEF
	ri.Flags = inner.Flags ^ 0x05 // 仅翻动用户位
	if err := h.Restore(tid, &ri); err != nil {
		t.Fatalf("restore inner: %v", err)
	}
	if got := h.ActiveDepth(tid); got != 1 {
		t.Fatalf("depth after inner restore = %d, want 1", got)
	}
	live, _ := h.LiveState(tid)
	if live.Regs[0] != 0xDEAD || live.Regs[1] != 0xBEEF {
		t.Fatalf("mutable regs not committed: %x %x", live.Regs[0], live.Regs[1])
	}
	if live.Flags != inner.Flags^0x05 {
		t.Fatalf("flags not committed: %x", live.Flags)
	}

	// 恢复外层帧，影子链回到空。
	ro := outer
	if err := h.Restore(tid, &ro); err != nil {
		t.Fatalf("restore outer: %v", err)
	}
	if got := h.ActiveDepth(tid); got != 0 {
		t.Fatalf("depth after outer restore = %d, want 0", got)
	}
	if err := h.Restore(tid, &ro); !errors.Is(err, ErrNoActiveFrame) {
		t.Fatalf("restore on empty stack = %v, want ErrNoActiveFrame", err)
	}
}

func TestReplayOldSeqRejected(t *testing.T) {
	h := newHost(t)
	var trk seqTracker

	f1 := baseFrame()
	f1.Seq = trk.alloc()
	d1, err := h.EnterSignal(f1, defaultOpts())
	if err != nil {
		t.Fatalf("enter f1: %v", err)
	}
	f2 := baseFrame()
	f2.Seq = trk.alloc()
	f2.Parent = d1
	if _, err := h.EnterSignal(f2, defaultOpts()); err != nil {
		t.Fatalf("enter f2: %v", err)
	}

	// 内层帧仍活跃时重放外层旧序号：越过内层帧，拒绝。
	stale := f1
	if err := h.Restore(tid, &stale); !errors.Is(err, ErrSeqMismatch) {
		t.Fatalf("stale outer restore = %v, want ErrSeqMismatch", err)
	}
	// 消费内层后再重放已消费帧：重放攻击，拒绝。
	if err := h.Restore(tid, &f2); err != nil {
		t.Fatalf("restore inner: %v", err)
	}
	replay := f2
	if err := h.Restore(tid, &replay); !errors.Is(err, ErrSeqMismatch) {
		t.Fatalf("replayed inner restore = %v, want ErrSeqMismatch", err)
	}
	if got := h.ActiveDepth(tid); got != 1 {
		t.Fatalf("depth = %d, want 1 (failed restores must not consume)", got)
	}
	after, _ := h.LiveState(tid)
	if after != f2 {
		t.Fatalf("live state drifted: got %+v want %+v", after, f2)
	}
}

func TestPrivilegeBitElevationRejected(t *testing.T) {
	h := newHost(t)
	var trk seqTracker

	f := baseFrame()
	f.Seq = trk.alloc()
	if _, err := h.EnterSignal(f, defaultOpts()); err != nil {
		t.Fatalf("enter: %v", err)
	}
	before, _ := h.LiveState(tid)

	// 抬高保留权限位（bit 4 不在用户掩码内）。
	esc := f
	esc.Flags = f.Flags | 0x10
	if err := h.Restore(tid, &esc); !errors.Is(err, ErrFlagsEscalation) {
		t.Fatalf("escalation restore = %v, want ErrFlagsEscalation", err)
	}
	if got := h.ActiveDepth(tid); got != 1 {
		t.Fatalf("frame consumed on failure, depth = %d", got)
	}
	after, _ := h.LiveState(tid)
	if after != before {
		t.Fatalf("live state modified by failed restore: %+v -> %+v", before, after)
	}

	// 掩码内的位可以翻动。
	ok := f
	ok.Flags = f.Flags ^ 0x07
	if err := h.Restore(tid, &ok); err != nil {
		t.Fatalf("in-mask flags flip rejected: %v", err)
	}
}

func TestMidValidationFailureNoPartialState(t *testing.T) {
	h := newHost(t)
	var trk seqTracker

	outer := baseFrame()
	outer.Seq = trk.alloc()
	d1, err := h.EnterSignal(outer, defaultOpts())
	if err != nil {
		t.Fatalf("enter outer: %v", err)
	}
	inner := baseFrame()
	inner.Seq = trk.alloc()
	inner.Parent = d1
	if _, err := h.EnterSignal(inner, defaultOpts()); err != nil {
		t.Fatalf("enter inner: %v", err)
	}
	before, _ := h.LiveState(tid)
	depthBefore := h.ActiveDepth(tid)

	// 前半段检查全部通过（序号、父摘要、只读寄存器、flags 均合法），
	// 但 PC 落在未登记区间：验证中途失败。
	bad := inner
	bad.Regs[0] = 0x1234 // 允许修改，不应被部分提交
	bad.PC = 0xDEAD0000  // 未登记且 4 字节对齐
	if err := h.Restore(tid, &bad); !errors.Is(err, ErrBadPC) {
		t.Fatalf("bad pc restore = %v, want ErrBadPC", err)
	}
	if got := h.ActiveDepth(tid); got != depthBefore {
		t.Fatalf("shadow chain consumed on failure: depth %d -> %d", depthBefore, got)
	}
	after, _ := h.LiveState(tid)
	if after != before {
		t.Fatalf("register state partially modified: %+v -> %+v", before, after)
	}

	// 影子链未受损：合法恢复仍可完成。
	good := inner
	good.Regs[0] = 0x1234
	if err := h.Restore(tid, &good); err != nil {
		t.Fatalf("restore after failed attempt: %v", err)
	}
	live, _ := h.LiveState(tid)
	if live.Regs[0] != 0x1234 {
		t.Fatalf("mutable reg not committed: %x", live.Regs[0])
	}
}

func TestCrossThreadRejected(t *testing.T) {
	h := newHost(t)
	var trk seqTracker

	f := baseFrame()
	f.Seq = trk.alloc()
	if _, err := h.EnterSignal(f, defaultOpts()); err != nil {
		t.Fatalf("enter: %v", err)
	}
	forged := f
	forged.ThreadID = tid + 1
	if err := h.Restore(tid, &forged); !errors.Is(err, ErrCrossThread) {
		t.Fatalf("cross-thread restore = %v, want ErrCrossThread", err)
	}
	if err := h.Restore(tid+1, &f); !errors.Is(err, ErrUnknownThread) {
		t.Fatalf("restore on foreign tid = %v, want ErrUnknownThread", err)
	}
}

func TestReadOnlyRegisterBitExact(t *testing.T) {
	h := newHost(t)
	var trk seqTracker

	f := baseFrame()
	f.Seq = trk.alloc()
	if _, err := h.EnterSignal(f, defaultOpts()); err != nil {
		t.Fatalf("enter: %v", err)
	}
	before, _ := h.LiveState(tid)
	tampered := f
	tampered.Regs[5] ^= 1 // reg 5 未获允许，哪怕 1 位也拒绝
	if err := h.Restore(tid, &tampered); !errors.Is(err, ErrRegMismatch) {
		t.Fatalf("tampered reg restore = %v, want ErrRegMismatch", err)
	}
	after, _ := h.LiveState(tid)
	if after != before {
		t.Fatalf("live state modified by failed restore")
	}
}

func TestPCAndSPAlignmentAndHalfOpenRanges(t *testing.T) {
	h := newHost(t)
	var trk seqTracker

	mk := func() Frame {
		f := baseFrame()
		f.Seq = trk.alloc()
		return f
	}

	cases := []struct {
		name    string
		mutate  func(*Frame)
		wantErr error
	}{
		{"pc misaligned", func(f *Frame) { f.PC = execBase + 2 }, ErrBadPC},
		{"pc at half-open end", func(f *Frame) { f.PC = execBase + execLen }, ErrBadPC},
		{"sp misaligned", func(f *Frame) { f.SP = stackLo + 8 }, ErrBadSP},
		{"sp at half-open end", func(f *Frame) { f.SP = stackLo + stackLen }, ErrBadSP},
		{"sp below stack", func(f *Frame) { f.SP = stackLo - 16 }, ErrBadSP},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := mk()
			if _, err := h.EnterSignal(f, defaultOpts()); err != nil {
				t.Fatalf("enter: %v", err)
			}
			before, _ := h.LiveState(tid)
			tc.mutate(&f)
			if err := h.Restore(tid, &f); !errors.Is(err, tc.wantErr) {
				t.Fatalf("restore = %v, want %v", err, tc.wantErr)
			}
			after, _ := h.LiveState(tid)
			if after != before {
				t.Fatalf("live state modified on failure")
			}
			// 边界合法值应通过：PC 末条 4 字节入口、SP 末个 16 字节槽。
			f.PC = execBase + execLen - 4
			f.SP = stackLo + stackLen - 16
			if err := h.Restore(tid, &f); err != nil {
				t.Fatalf("boundary restore: %v", err)
			}
		})
	}
}

func TestParentDigestMismatch(t *testing.T) {
	h := newHost(t)
	var trk seqTracker

	outer := baseFrame()
	outer.Seq = trk.alloc()
	if _, err := h.EnterSignal(outer, defaultOpts()); err != nil {
		t.Fatalf("enter outer: %v", err)
	}
	inner := baseFrame()
	inner.Seq = trk.alloc()
	d2, err := h.EnterSignal(inner, defaultOpts())
	if err != nil {
		t.Fatalf("enter inner: %v", err)
	}
	forged := inner
	forged.Parent = d2 // 自指摘要，与影子记录的父摘要不符
	if err := h.Restore(tid, &forged); !errors.Is(err, ErrParentMismatch) {
		t.Fatalf("forged parent restore = %v, want ErrParentMismatch", err)
	}
	if got := h.ActiveDepth(tid); got != 2 {
		t.Fatalf("depth = %d, want 2", got)
	}
}

func TestRangeOverflowRejected(t *testing.T) {
	h := NewHost()
	if err := h.RegisterExecRange(math.MaxUint64-4, 8); !errors.Is(err, ErrRangeOverflow) {
		t.Fatalf("overflowing exec range = %v, want ErrRangeOverflow", err)
	}
	if err := h.RegisterExecRange(0x1000, 0); !errors.Is(err, ErrRangeOverflow) {
		t.Fatalf("zero-length range = %v, want ErrRangeOverflow", err)
	}
	f := baseFrame()
	opts := defaultOpts()
	opts.StackBase = math.MaxUint64 - 8
	opts.StackLen = 0x100
	if _, err := h.EnterSignal(f, opts); !errors.Is(err, ErrRangeOverflow) {
		t.Fatalf("overflowing stack range = %v, want ErrRangeOverflow", err)
	}
}

func TestSeqExhaustionNoWrap(t *testing.T) {
	h := newHost(t)
	f := baseFrame()
	if _, err := h.EnterSignal(f, defaultOpts()); err != nil {
		t.Fatalf("enter: %v", err)
	}
	// 把序号推进到耗尽边界。
	h.mu.Lock()
	h.threads[tid].nextSeq = math.MaxUint64
	h.mu.Unlock()
	if _, err := h.EnterSignal(f, defaultOpts()); !errors.Is(err, ErrSeqExhausted) {
		t.Fatalf("enter at exhaustion = %v, want ErrSeqExhausted", err)
	}
	// 序号未回绕：再次进入仍拒绝，而不是回绕到 0 后成功。
	if _, err := h.EnterSignal(f, defaultOpts()); !errors.Is(err, ErrSeqExhausted) {
		t.Fatalf("enter after exhaustion = %v, want ErrSeqExhausted", err)
	}
}
