package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"
)

// NumRegs 为帧模型中通用寄存器数量。
const NumRegs = 16

// Digest 是帧内容的 SHA-256 摘要，用于父子帧链式绑定。
type Digest [sha256.Size]byte

// Frame 是用户态提交的信号恢复帧。
type Frame struct {
	ThreadID uint64
	Seq      uint64 // 每线程单调递增序号，耗尽即拒绝再入，不回绕
	Regs     [NumRegs]uint64
	PC       uint64
	SP       uint64
	Flags    uint64
	Parent   Digest // 父帧摘要；最外层帧为零值
}

// FrameOptions 由宿主在进入信号时给定，约束该帧允许的恢复范围。
type FrameOptions struct {
	StackBase   uint64 // 登记栈区基址
	StackLen    uint64 // 栈区长度，半开区间 [base, base+len)
	MutableRegs []int  // 允许修改的通用寄存器索引
	UserMask    uint64 // 允许被用户改动的 flags 位掩码
}

// 恢复失败的哨兵错误。
var (
	ErrUnknownThread   = errors.New("netsec: unknown thread")
	ErrNoActiveFrame   = errors.New("netsec: no active frame for thread")
	ErrSeqExhausted    = errors.New("netsec: frame sequence exhausted")
	ErrCrossThread     = errors.New("netsec: frame belongs to another thread")
	ErrSeqMismatch     = errors.New("netsec: frame is not the innermost active frame (replay or skip)")
	ErrParentMismatch  = errors.New("netsec: parent digest mismatch")
	ErrRegMismatch     = errors.New("netsec: read-only register modified")
	ErrBadPC           = errors.New("netsec: pc outside registered executable ranges or misaligned")
	ErrBadSP           = errors.New("netsec: sp outside frame stack region or misaligned")
	ErrFlagsEscalation = errors.New("netsec: flags modify bits outside user mask")
	ErrRangeOverflow   = errors.New("netsec: address range addition overflow")
	ErrBadRegIndex     = errors.New("netsec: register index out of range")
)

// shadowFrame 是宿主进入信号时保存的不可修改影子记录。
type shadowFrame struct {
	snapshot Frame // 进入信号时的寄存器现场，恢复时逐位比对
	digest   Digest
	parent   Digest
	stackLo  uint64
	stackHi  uint64 // 半开区间 [stackLo, stackHi)
	mutable  [NumRegs]bool
	userMask uint64
}

type threadState struct {
	nextSeq uint64
	frames  []*shadowFrame // 影子栈，栈顶为最内层帧
	live    Frame          // 当前已生效的寄存器状态
	hasLive bool
}

// Host 是信号分派宿主，负责登记可执行区间、保存影子记录并验证恢复帧。
type Host struct {
	mu      sync.Mutex
	threads map[uint64]*threadState
	execLo  []uint64
	execHi  []uint64 // 与 execLo 配对，半开区间
}

// NewHost 创建一个空的恢复宿主。
func NewHost() *Host {
	return &Host{threads: make(map[uint64]*threadState)}
}

// RegisterExecRange 登记一段可执行区间 [base, base+length)，半开区间。
// length 为 0 或地址加法溢出时拒绝。
func (h *Host) RegisterExecRange(base, length uint64) error {
	hi, err := checkedEnd(base, length)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.execLo = append(h.execLo, base)
	h.execHi = append(h.execHi, hi)
	return nil
}

func checkedEnd(base, length uint64) (uint64, error) {
	if length == 0 || base > math.MaxUint64-length {
		return 0, ErrRangeOverflow
	}
	return base + length, nil
}

// digestFrame 计算帧内容摘要，把父摘要纳入以形成链式绑定。
func digestFrame(f *Frame) Digest {
	h := sha256.New()
	var buf [8]byte
	put := func(v uint64) {
		binary.LittleEndian.PutUint64(buf[:], v)
		h.Write(buf[:])
	}
	put(f.ThreadID)
	put(f.Seq)
	for _, r := range f.Regs {
		put(r)
	}
	put(f.PC)
	put(f.SP)
	put(f.Flags)
	h.Write(f.Parent[:])
	var d Digest
	copy(d[:], h.Sum(nil))
	return d
}

// EnterSignal 在进入信号时由宿主调用：保存不可修改的影子记录并压栈，
// 返回该帧摘要。序号耗尽（达到 math.MaxUint64）时拒绝再入，不回绕。
func (h *Host) EnterSignal(cur Frame, opts FrameOptions) (Digest, error) {
	stackHi, err := checkedEnd(opts.StackBase, opts.StackLen)
	if err != nil {
		return Digest{}, err
	}
	sf := &shadowFrame{
		stackLo:  opts.StackBase,
		stackHi:  stackHi,
		userMask: opts.UserMask,
	}
	for _, idx := range opts.MutableRegs {
		if idx < 0 || idx >= NumRegs {
			return Digest{}, fmt.Errorf("%w: %d", ErrBadRegIndex, idx)
		}
		sf.mutable[idx] = true
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	ts := h.threads[cur.ThreadID]
	if ts == nil {
		ts = &threadState{}
		h.threads[cur.ThreadID] = ts
	}
	if ts.nextSeq == math.MaxUint64 {
		return Digest{}, ErrSeqExhausted
	}
	if n := len(ts.frames); n > 0 {
		sf.parent = ts.frames[n-1].digest
	}
	cur.Seq = ts.nextSeq
	cur.Parent = sf.parent
	ts.nextSeq++
	sf.snapshot = cur
	sf.digest = digestFrame(&cur)
	ts.frames = append(ts.frames, sf)
	if !ts.hasLive {
		ts.live = cur
		ts.hasLive = true
	}
	return sf.digest, nil
}

// Restore 验证用户提交的恢复帧。全部检查通过后才一次性恢复并消费
// 最内层影子帧；任何一步失败都不消费帧、不修改寄存器状态。
func (h *Host) Restore(tid uint64, f *Frame) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	ts := h.threads[tid]
	if ts == nil {
		return ErrUnknownThread
	}
	if len(ts.frames) == 0 {
		return ErrNoActiveFrame
	}
	inner := ts.frames[len(ts.frames)-1]

	// 跨线程、重放旧序号、越过内层帧：统一表现为与栈顶影子记录不符。
	if f.ThreadID != tid || f.ThreadID != inner.snapshot.ThreadID {
		return ErrCrossThread
	}
	if f.Seq != inner.snapshot.Seq {
		return ErrSeqMismatch
	}
	if f.Parent != inner.parent {
		return ErrParentMismatch
	}
	for i := 0; i < NumRegs; i++ {
		if !inner.mutable[i] && f.Regs[i] != inner.snapshot.Regs[i] {
			return fmt.Errorf("%w: reg %d", ErrRegMismatch, i)
		}
	}
	if f.PC%4 != 0 || !h.inExec(f.PC) {
		return ErrBadPC
	}
	if f.SP%16 != 0 || f.SP < inner.stackLo || f.SP >= inner.stackHi {
		return ErrBadSP
	}
	if (f.Flags^inner.snapshot.Flags)&^inner.userMask != 0 {
		return ErrFlagsEscalation
	}

	// 全部验证通过：一次性提交并消费帧。
	ts.live = *f
	ts.frames = ts.frames[:len(ts.frames)-1]
	return nil
}

func (h *Host) inExec(pc uint64) bool {
	for i := range h.execLo {
		if pc >= h.execLo[i] && pc < h.execHi[i] {
			return true
		}
	}
	return false
}

// LiveState 返回线程当前已生效的寄存器状态，供宿主/测试观察。
func (h *Host) LiveState(tid uint64) (Frame, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ts := h.threads[tid]
	if ts == nil || !ts.hasLive {
		return Frame{}, false
	}
	return ts.live, true
}

// ActiveDepth 返回线程当前未消费的影子帧数量。
func (h *Host) ActiveDepth(tid uint64) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ts := h.threads[tid]; ts != nil {
		return len(ts.frames)
	}
	return 0
}
