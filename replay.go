package netsec

import (
	"errors"
	"sync"
	"unsafe"
)

// 预定义的判定错误，宿主可用 errors.Is 区分拒绝原因。
var (
	// ErrReplay 表示序号已交付，或已有同序号验签在途，属于重复包。
	ErrReplay = errors.New("netsec: replay of already accepted sequence number")
	// ErrTooOld 表示完整序号落在抗重放窗口左端之外，是极旧的迟到包。
	ErrTooOld = errors.New("netsec: sequence number below replay window")
	// ErrExhausted 表示 64 位序号空间已用尽，最高已认证号已是 MaxUint64。
	ErrExhausted = errors.New("netsec: 64-bit sequence space exhausted")
	// ErrOverflow 表示按候选规则选中的完整序号越过 64 位上限（MaxUint64 之后）。
	ErrOverflow = errors.New("netsec: sequence number would exceed 64-bit limit")
	// ErrSignature 表示宿主验签函数返回错误；此时窗口与位图不会发生任何移动。
	ErrSignature = errors.New("netsec: signature verification failed")
)

// span32 是低 32 位序号的完整跨度，即 2^32。
const span32 uint64 = 1 << 32

// ReplayFilter 是供隧道接收宿主调用的抗重放过滤器。
//
// 线上只传输 64 位序号的低 32 位，窗口宽度必须小于 2^31。完整序号取与
// “最高已认证号 + 1”距离最近的 64 位候选，距离并列时取较小者；选中的候选
// 越过 64 位上限（> MaxUint64）时拒绝。只有宿主验签成功后才原子提交位图并
// 推进窗口，验签失败不会移动窗口。多个相同序号的并行验签只会交付一次。
type ReplayFilter struct {
	mu       sync.Mutex
	core     replayCore[uint64]
	verify   func(seq uint64, payload any) error
	inflight map[uint64]*inflightCall
}

// inflightCall 记录一个在途验签：发起者负责验签，其余同序号并行调用在 done 上等待。
type inflightCall struct {
	done chan struct{}
	err  error
}

// NewReplayFilter 创建抗重放过滤器。
//
// windowWidth 是滑动窗口可接纳的不同序号数量，必须满足 1 <= windowWidth < 2^31。
// verify 在互斥锁之外执行，返回非 nil 时视为验签失败，窗口保持不动。
func NewReplayFilter(windowWidth int, verify func(seq uint64, payload any) error) (*ReplayFilter, error) {
	if verify == nil {
		return nil, errors.New("netsec: nil verify function")
	}
	if windowWidth < 1 || uint64(windowWidth) >= span32/2 {
		return nil, errors.New("netsec: window width must be in [1, 2^31)")
	}
	return &ReplayFilter{
		core:     newReplayCore[uint64](64, 32, uint64(windowWidth)),
		verify:   verify,
		inflight: make(map[uint64]*inflightCall),
	}, nil
}

// Reconstruct 仅按低 32 位推算完整 64 位序号，不改变任何状态。
//
// 候选取与“最高已认证号 + 1”距离最近的 64 位值，并列取较小者；选中的候选
// 越过 64 位上限时返回 ErrOverflow，序号空间已用尽时返回 ErrExhausted。
func (f *ReplayFilter) Reconstruct(low32 uint32) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.core.reconstruct(uint64(low32))
}

// Verify 是接收宿主的入口：推算完整序号、抗重放判定、调用验签并在成功后提交。
//
// 同序号的并行调用只会有一个真正执行 verify，其余调用共享其结果；无论并发
// 数量多少，一个序号最多交付（verify 成功并提交）一次。
func (f *ReplayFilter) Verify(low32 uint32, payload any) (seq uint64, err error) {
	f.mu.Lock()
	if f.core.exhausted() {
		f.mu.Unlock()
		return 0, ErrExhausted
	}
	seq, err = f.core.reconstruct(uint64(low32))
	if err != nil {
		f.mu.Unlock()
		return 0, err
	}
	// 极旧包：落在窗口左端之外。未初始化（首个包）时跳过该判定。
	if f.core.started && seq < f.core.edge() {
		f.mu.Unlock()
		return 0, ErrTooOld
	}
	// 已提交或已在途的同序号都是重放。
	if f.core.bitGet(seq) {
		f.mu.Unlock()
		return seq, ErrReplay
	}
	if call, ok := f.inflight[seq]; ok {
		f.mu.Unlock()
		<-call.done
		// 发起者失败则未交付，等待者收到相同验签失败结论，允许之后重试；
		// 发起者成功则该序号已提交，等待者属于重放，绝不能再次算作交付。
		if call.err != nil {
			return seq, call.err
		}
		return seq, ErrReplay
	}
	call := &inflightCall{done: make(chan struct{})}
	f.inflight[seq] = call
	f.mu.Unlock()

	// 验签在锁外执行，允许不同序号并行验签。
	verr := f.safeVerify(seq, payload)

	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.inflight, seq)
	if verr != nil {
		// 验签失败：只清理在途标记，窗口与位图原样不动。
		call.err = errors.Join(ErrSignature, verr)
		close(call.done)
		return seq, call.err
	}
	// 提交前在同一临界区内重新判定，保证提交原子且不会重复交付。
	if f.core.exhausted() {
		call.err = ErrExhausted
		close(call.done)
		return 0, call.err
	}
	if f.core.started {
		if seq < f.core.edge() {
			call.err = ErrTooOld
			close(call.done)
			return seq, call.err
		}
		if f.core.bitGet(seq) {
			call.err = ErrReplay
			close(call.done)
			return seq, call.err
		}
	}
	f.core.commit(seq)
	close(call.done)
	return seq, nil
}

// safeVerify 包住宿主验签，发起者 panic 时转化为错误并保证等待者不会永久阻塞。
func (f *ReplayFilter) safeVerify(seq uint64, payload any) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.Join(ErrSignature, panicError{value: r})
		}
	}()
	return f.verify(seq, payload)
}

type panicError struct{ value any }

func (e panicError) Error() string { return "netsec: verify panicked" }

// replayCore 是与位宽参数无关的纯算术内核：线上使用 [64,32] 实例，
// 测试中用小位宽实例做穷举交叉验证。
type replayCore[T uint8 | uint16 | uint32 | uint64] struct {
	bits     uint // 完整序号位宽 B
	lowBits  uint // 线上传输的低位宽 L，L < B
	wordBits uint // 存储字 T 的位宽
	span     T    // 2^L
	maxSeq   T    // 2^B - 1
	width    T    // 窗口宽度 W，W < 2^(B-1)
	started  bool
	highest  T   // 最高已认证完整序号
	bitmap   []T // 位图：偏移 0（bit 0）是 highest，偏移 k 表示 highest-k，越大越旧
}

func newReplayCore[T uint8 | uint16 | uint32 | uint64](bits, lowBits uint, width T) replayCore[T] {
	var zero T
	wordBits := uint(unsafe.Sizeof(zero)) * 8
	return replayCore[T]{
		bits:     bits,
		lowBits:  lowBits,
		wordBits: wordBits,
		span:     T(1) << lowBits,
		maxSeq:   T(1)<<bits - 1,
		width:    width,
		bitmap:   make([]T, (width+T(wordBits)-1)/T(wordBits)),
	}
}

// exhausted 表示最高已认证号已达 B 位上限，后续任何包都必须拒绝。
func (c *replayCore[T]) exhausted() bool {
	return c.started && c.highest == c.maxSeq
}

// reconstruct 实现候选规则：枚举所有落在 B 位上限内、与 low 同低位的候选，
// 取与 next=highest+1 距离最近者，并列取较小者。
func (c *replayCore[T]) reconstruct(low T) (T, error) {
	if !c.started {
		// 首个包：最高已认证号为 -1，next=0，最近候选就是 low 本身。
		return low, nil
	}
	if c.highest == c.maxSeq {
		return 0, ErrExhausted
	}
	next := c.highest + 1 // 未达 maxSeq，这里不会回绕

	// 基准候选：把 next 的高 L 位与 low 拼接。
	base := (next &^ (c.span - 1)) | low
	best := base
	bestDist := uabsdiff(base, next)

	// 上邻候选 base+span：不越过 B 位上限时参与比较；越界且严格更近时拒绝。
	if base <= c.maxSeq-c.span {
		up := base + c.span
		if d := uabsdiff(up, next); d < bestDist || (d == bestDist && up < best) {
			best, bestDist = up, d
		}
	} else {
		// 越界候选的数学距离 = span - 基准距离；等距时按“并列取较小者”保留基准。
		if d := c.span - uabsdiff(base, next); d < bestDist {
			return 0, ErrOverflow
		}
	}

	// 下邻候选 base-span：仅当不回绕（base >= span）时有效。
	if base >= c.span {
		down := base - c.span
		if d := uabsdiff(down, next); d < bestDist || (d == bestDist && down < best) {
			best, bestDist = down, d
		}
	}
	return best, nil
}

// edge 返回窗口左端（含）：highest-W+1，仅在 started 后有意义。
func (c *replayCore[T]) edge() T {
	if c.highest+1 >= c.width {
		return c.highest - c.width + 1
	}
	return 0
}

// bitGet 判断 seq 是否已在窗口位图中（即已成功交付）。
func (c *replayCore[T]) bitGet(seq T) bool {
	if !c.started || seq > c.highest {
		return false
	}
	offset := c.highest - seq
	if offset >= c.width {
		return false
	}
	word := offset / T(c.wordBits)
	bit := offset % T(c.wordBits)
	return c.bitmap[word]&(T(1)<<bit) != 0
}

// commit 在调用方持锁的前提下置位并按需推进窗口，仅在验签成功后调用。
func (c *replayCore[T]) commit(seq T) {
	if !c.started {
		c.started = true
		c.highest = seq
		c.setBit(0) // 偏移 0 即最高已认证号
		return
	}
	switch {
	case seq <= c.highest:
		// 窗口内的迟到包：只需置位，窗口不动。
		c.setBit(c.highest - seq)
	case seq-c.highest >= c.width:
		// 跳跃超过窗口宽度：旧位图全部滑出，清空后只留新右端。
		for i := range c.bitmap {
			c.bitmap[i] = 0
		}
		c.highest = seq
		c.setBit(0)
	default:
		delta := seq - c.highest
		// 旧序号相对新最高号变老 delta，对应位向更旧偏移方向移动。
		c.shiftOlder(delta)
		c.highest = seq
		c.setBit(0)
	}
}

// step 是 Verify 的顺序内核：完成重构、窗口判定，验签成功才提交。
// 并行/在途去重由 ReplayFilter 在外层负责，测试用它与穷举参考逐条对齐。
func (c *replayCore[T]) step(low T, verifyOK bool) (seq T, err error) {
	if c.exhausted() {
		return 0, ErrExhausted
	}
	seq, err = c.reconstruct(low)
	if err != nil {
		return 0, err
	}
	if c.started && seq < c.edge() {
		return seq, ErrTooOld
	}
	if c.bitGet(seq) {
		return seq, ErrReplay
	}
	if !verifyOK {
		// 验签失败：直接返回，位图与窗口保持原样。
		return seq, ErrSignature
	}
	c.commit(seq)
	return seq, nil
}

func (c *replayCore[T]) setBit(offset T) {
	word := offset / T(c.wordBits)
	bit := offset % T(c.wordBits)
	c.bitmap[word] |= T(1) << bit
}

// shiftOlder 把位图向“更旧偏移”方向移动 delta 位：偏移 0 是最高号，
// 序号变老 delta 意味着其偏移增加 delta，即位图整体左移，超宽部分由 maskWidth 截掉。
func (c *replayCore[T]) shiftOlder(delta T) {
	wordShift := delta / T(c.wordBits)
	bitShift := delta % T(c.wordBits)
	for i := len(c.bitmap) - 1; i >= 0; i-- {
		var v T
		src := i - int(wordShift)
		if src >= 0 {
			v = c.bitmap[src] << bitShift
			if bitShift != 0 && src-1 >= 0 {
				v |= c.bitmap[src-1] >> (T(c.wordBits) - bitShift)
			}
		}
		c.bitmap[i] = v
	}
	c.maskWidth()
}

// maskWidth 清掉逻辑宽度之外的位：偏移 >= width 的槽位不属于窗口。
func (c *replayCore[T]) maskWidth() {
	last := len(c.bitmap) - 1
	for i := range c.bitmap {
		if i < last {
			continue // 完整字全部属于窗口
		}
		rem := c.width % T(c.wordBits)
		if rem == 0 {
			continue // 宽度恰为整字倍数，最后一个字也全部有效
		}
		c.bitmap[i] &= (T(1) << rem) - 1
	}
}

// uabsdiff 返回无符号差值的绝对值，避免任何加减法溢出。
func uabsdiff[T uint8 | uint16 | uint32 | uint64](a, b T) T {
	if a >= b {
		return a - b
	}
	return b - a
}
