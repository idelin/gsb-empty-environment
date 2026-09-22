package crcseg

import "fmt"

// BlockKind 描述一个输入块的可用性。
type BlockKind int

const (
	// BlockOK 表示块数据完整可读。
	BlockOK BlockKind = iota
	// BlockMissing 表示块缺失（读不到任何字节）。
	BlockMissing
	// BlockBad 表示块能读到但内容损坏。
	BlockBad
)

// Block 是解析后的一个分块。Kind 不为 BlockOK 时 Data 为 nil。
type Block struct {
	Kind BlockKind
	Data []byte
}

// Frame 是最小的“线路格式”：1 字节标签 + 载荷。
//
//	标签 0x00：完整块，载荷即块字节；
//	标签 0x01：缺数据（传输层丢失），载荷必须为空；
//	标签 0x02：坏数据（块校验失败），载荷必须为空；
//	其它标签：格式错误，按坏数据处理。
const (
	TagOK      byte = 0x00
	TagMissing byte = 0x01
	TagBad     byte = 0x02
)

// ParseFrame 解析一帧。
//
// 缺数据返回 BlockMissing 与 ErrMissingData；坏数据（标签 0x02、
// 未知标签、或缺失/坏帧携带了非空载荷）返回 BlockBad 与 ErrBadData。
// 解析阶段不做任何 CRC 计算，也不按内容做大块复制之外的分配。
func ParseFrame(frame []byte) (Block, error) {
	if len(frame) == 0 {
		return Block{Kind: BlockBad}, fmt.Errorf("%w: empty frame", ErrBadData)
	}
	tag, payload := frame[0], frame[1:]
	switch tag {
	case TagOK:
		// payload 可能为空：空块是合法块（等价于恒等分段）。
		return Block{Kind: BlockOK, Data: payload}, nil
	case TagMissing:
		if len(payload) != 0 {
			return Block{Kind: BlockBad}, fmt.Errorf("%w: missing frame carries payload", ErrBadData)
		}
		return Block{Kind: BlockMissing}, fmt.Errorf("%w: block absent", ErrMissingData)
	case TagBad:
		if len(payload) != 0 {
			return Block{Kind: BlockBad}, fmt.Errorf("%w: bad frame carries payload", ErrBadData)
		}
		return Block{Kind: BlockBad}, fmt.Errorf("%w: block corrupted", ErrBadData)
	default:
		return Block{Kind: BlockBad}, fmt.Errorf("%w: unknown frame tag 0x%02x", ErrBadData, tag)
	}
}

// Collector 逐块累积 CRC，直到块全部收齐后取出整个消息的摘要。
// 它接受任意顺序之外的任意分块方式，且永不为“完整消息”分配缓冲——
// 块字节只流经 CRC 寄存器。
type Collector struct {
	h     *Hasher
	quota int64 // 调用方声明的总长度上限；<0 表示不限。
}

// NewCollector 创建收集器。quota 为允许的总字节数上限，
// 传 -1 表示不设上限。
func NewCollector(p Params, quota int64) (*Collector, error) {
	h, err := NewHasher(p)
	if err != nil {
		return nil, err
	}
	if quota < -1 {
		return nil, fmt.Errorf("crcseg: negative quota %d", quota)
	}
	return &Collector{h: h, quota: quota}, nil
}

// Reserve 在写入前检查“再接收 n 字节”是否超出额度。
// 检查发生在任何与 n 相关的分配之前。
func (c *Collector) Reserve(n int64) error {
	if n < 0 {
		return fmt.Errorf("crcseg: negative reservation %d", n)
	}
	if c.quota >= 0 && c.h.size > c.quota-n {
		return fmt.Errorf("%w: quota=%d used=%d want=%d",
			ErrQuota, c.quota, c.h.size, n)
	}
	return nil
}

// Write 直接吸收一块已知完好的字节，吸收前先做额度检查。
func (c *Collector) Write(data []byte) (int, error) {
	if err := c.Reserve(int64(len(data))); err != nil {
		return 0, err
	}
	return c.h.Write(data)
}

// IngestBlock 消费一个解析后的块：缺数据/坏数据原样返回对应错误，
// 完整块先做额度检查再吸收。出错时收集器状态保持不变，调用方可在
// 补齐该块后重试。
func (c *Collector) IngestBlock(b Block) error {
	switch b.Kind {
	case BlockMissing:
		return fmt.Errorf("%w: cannot ingest absent block", ErrMissingData)
	case BlockBad:
		return fmt.Errorf("%w: cannot ingest corrupted block", ErrBadData)
	case BlockOK:
		_, err := c.Write(b.Data)
		return err
	default:
		return fmt.Errorf("%w: unknown block kind %d", ErrBadData, b.Kind)
	}
}

// IngestFrame 是 ParseFrame + IngestBlock 的便捷组合。
// 注意：额度检查在 ParseFrame 之后、CRC 吸收之前进行；坏帧/缺帧
// 不会消耗任何额度。
func (c *Collector) IngestFrame(frame []byte) error {
	b, perr := ParseFrame(frame)
	if perr != nil {
		return perr
	}
	return c.IngestBlock(b)
}

// Segment 返回当前已收数据的摘要。
func (c *Collector) Segment() Segment { return c.h.Segment() }

// Len 返回已吸收字节数。
func (c *Collector) Len() int64 { return c.h.size }

// Reset 清空收集器（额度设置保持不变）。
func (c *Collector) Reset() { c.h.Reset() }
