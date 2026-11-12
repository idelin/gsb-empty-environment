package netsec

import (
	"errors"
	"fmt"
)

// ErrRecordTooLarge 在记录载荷超过共享页容量时返回。
var ErrRecordTooLarge = errors.New("netsec: record larger than page capacity")

// Writer 是与 Reader 配对的 seqlock 写入器。
//
// 它同时是测试中的“受控写入器”：Publish 的每一步都通过页锁串行化，
// 读取器因此可以在写入步骤之间观察到确定的撕裂窗口。
type Writer struct {
	page     *MemPage
	capacity uint32

	gen uint64
	seq uint64

	// ceiling>0 时，seq 到达该偶数后下一次发布回绕到 0 并让 gen+1，
	// 便于测试代次逻辑；为 0 时使用 uint64 自然回绕。
	ceiling uint64
}

// NewWriter 在给定内存页上初始化共享页并返回写入器。
func NewWriter(page *MemPage, capacity uint32) (*Writer, error) {
	return newWriter(page, capacity, 0)
}

// newWriterWithCeiling 仅供测试：用较小的 seq 天花板快速制造回绕。
func newWriterWithCeiling(page *MemPage, capacity uint32, ceiling uint64) (*Writer, error) {
	if ceiling&1 != 0 || ceiling == 0 {
		return nil, fmt.Errorf("netsec: internal: ceiling must be a positive even number, got %d", ceiling)
	}
	return newWriter(page, capacity, ceiling)
}

func newWriter(page *MemPage, capacity uint32, ceiling uint64) (*Writer, error) {
	if capacity == 0 {
		return nil, errors.New("netsec: capacity must be positive")
	}
	if PageSize(capacity) > int(page.Size()) {
		return nil, fmt.Errorf("%w: need %d bytes, page has %d", ErrRecordTooLarge, PageSize(capacity), page.Size())
	}

	w := &Writer{
		page:     page,
		capacity: capacity,
		gen:      0,
		seq:      0,
		ceiling:  ceiling,
	}

	page.Lock()
	defer page.Unlock()
	mem := page.BytesLocked()
	le.PutUint32(mem[offMagic:], magic)
	le.PutUint32(mem[offVersion:], version)
	le.PutUint64(mem[offGen:], 0)
	le.PutUint64(mem[offSeq:], 0)
	le.PutUint32(mem[offCapacity:], capacity)
	le.PutUint32(mem[offLength:], 0)
	// 初始摘要对应空载荷。
	sum := digestPayload(nil)
	copy(mem[offDigest:offDigest+digestSize], sum[:])
	return w, nil
}

// Publish 以 seqlock 协议发布一条记录：
//
//  1. seq 置为奇数（写入进行中）
//  2. 写长度、摘要与载荷
//  3. seq 置为下一个偶数（稳定）；若越过回绕点则 gen+1、seq 归零
//
// 返回发布后的 (gen, seq)。
func (w *Writer) Publish(rec Record) (gen, seq uint64, err error) {
	payload, err := rec.Encoded()
	if err != nil {
		return 0, 0, err
	}
	if uint64(len(payload)) > uint64(w.capacity) {
		return 0, 0, fmt.Errorf("%w: payload %d > capacity %d", ErrRecordTooLarge, len(payload), w.capacity)
	}

	w.page.Lock()
	defer w.page.Unlock()
	mem := w.page.BytesLocked()

	// 步骤 1：标记写入进行中。
	w.seq++
	le.PutUint64(mem[offSeq:], w.seq)

	// 步骤 2：写载荷，再写摘要与长度，最后顺序落头字段。
	recordArea := mem[headerSize:]
	for i := range recordArea {
		recordArea[i] = 0
	}
	copy(recordArea, payload)

	sum := digestPayload(payload)
	copy(mem[offDigest:offDigest+digestSize], sum[:])
	le.PutUint32(mem[offLength:], uint32(len(payload)))

	// 步骤 3：推进到下一个稳定版本，必要时回绕并递增代次。
	switch {
	case w.ceiling != 0 && w.seq+2 >= w.ceiling:
		w.gen++
		w.seq = 0
	default:
		w.seq++
		if w.seq == 0 {
			// uint64 自然回绕：seq 已从奇数最大值 +1 归零（偶数）。
			w.gen++
		}
	}
	le.PutUint64(mem[offGen:], w.gen)
	le.PutUint64(mem[offSeq:], w.seq)
	return w.gen, w.seq, nil
}

// Generation 返回当前写入代次。
func (w *Writer) Generation() uint64 { return w.gen }

// Sequence 返回当前稳定序列号（始终为偶数）。
func (w *Writer) Sequence() uint64 { return w.seq }
