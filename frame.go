package acx

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
)

// 帧布局（全部多字节整数按小端或 uvarint，详见 FORMAT.md）：
//
//	magic      4 字节  "ACX1"
//	count      uvarint 事件（位）总数 N，解码器据此知道何时停止，不靠填充猜长度
//	numCtx     uvarint 上下文个数，1..256
//	payloadLen uvarint 算术编码载荷的字节数
//	labels     N 字节  每个事件的上下文标签（原始字节，必须 < numCtx）
//	crc32      4 字节  IEEE CRC-32，覆盖每个事件的 (label, bit) 两字节序列
//	payload    payloadLen 字节  算术编码输出
const (
	// MaxEvents 是单帧允许的最大事件数，超过即拒绝（长度超限）。
	MaxEvents = 1 << 26
	// MaxContexts 是上下文标签个数上限（标签为单字节）。
	MaxContexts = 256
)

var magic = []byte("ACX1")

var crcTable = crc32.MakeTable(crc32.IEEE)

var (
	ErrBadMagic     = errors.New("acx: bad magic")
	ErrNumContexts  = errors.New("acx: context count out of range [1,256]")
	ErrTooMany      = errors.New("acx: event count exceeds MaxEvents")
	ErrBadLabel     = errors.New("acx: context label out of range")
	ErrBadBit       = errors.New("acx: bit value must be 0 or 1")
	ErrTruncated    = errors.New("acx: truncated frame")
	ErrTrailing     = errors.New("acx: trailing garbage after payload")
	ErrChecksum     = errors.New("acx: crc32 mismatch")
	ErrFinalized    = errors.New("acx: encoder already finalized")
	ErrBadSnapshot  = errors.New("acx: corrupt snapshot")
	ErrContextLimit = errors.New("acx: frame exceeds decoder context limit")
)

// Event 是一个带上下文标签的二进制事件。
type Event struct {
	Label uint8
	Bit   uint8
}

// Encoder 把事件流编码成一帧。可增量调用 Put，也可 Snapshot/Resume 暂停恢复。
type Encoder struct {
	numCtx int
	count  uint64
	crc    uint32
	labels []byte
	ctxs   []context
	core   coreEncoder
	done   bool
}

// NewEncoder 创建带 numContexts 个上下文（1..256）的编码器。
func NewEncoder(numContexts int) (*Encoder, error) {
	if numContexts < 1 || numContexts > MaxContexts {
		return nil, ErrNumContexts
	}
	e := &Encoder{
		numCtx: numContexts,
		ctxs:   make([]context, numContexts),
		core:   newCoreEncoder(),
	}
	for i := range e.ctxs {
		e.ctxs[i] = newContext()
	}
	return e, nil
}

// Put 编码一个事件。只更新 label 对应的上下文。
func (e *Encoder) Put(label uint8, bit uint8) error {
	if e.done {
		return ErrFinalized
	}
	if int(label) >= e.numCtx {
		return ErrBadLabel
	}
	if bit > 1 {
		return ErrBadBit
	}
	if e.count >= MaxEvents {
		return ErrTooMany
	}
	c := &e.ctxs[label]
	e.core.encodeBit(c.zero, c.total(), bit)
	c.update(bit)
	e.labels = append(e.labels, label)
	e.crc = crc32.Update(e.crc, crcTable, []byte{label, bit})
	e.count++
	return nil
}

// Frame 终止编码并返回完整帧。可重复调用，结果不变。
func (e *Encoder) Frame() []byte {
	if !e.done {
		e.core.finish()
		e.done = true
	}
	var buf bytes.Buffer
	buf.Write(magic)
	buf.Write(uvarint(e.count))
	buf.Write(uvarint(uint64(e.numCtx)))
	buf.Write(uvarint(uint64(len(e.core.w.out))))
	buf.Write(e.labels)
	var crcB [4]byte
	binary.LittleEndian.PutUint32(crcB[:], e.crc)
	buf.Write(crcB[:])
	buf.Write(e.core.w.out)
	return buf.Bytes()
}

func uvarint(v uint64) []byte {
	var b [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(b[:], v)
	return b[:n]
}

// Decode 解码一帧。maxContexts 是调用方允许的上下文个数上限（1..256）。
// 解码器按帧头的 count 精确停止，并校验标签合法性、长度与 CRC。
func Decode(frame []byte, maxContexts int) ([]Event, error) {
	if maxContexts < 1 || maxContexts > MaxContexts {
		return nil, ErrNumContexts
	}
	if len(frame) < len(magic) || !bytes.Equal(frame[:len(magic)], magic) {
		return nil, ErrBadMagic
	}
	rest := frame[len(magic):]

	count, rest, err := readUvarint(rest)
	if err != nil {
		return nil, err
	}
	if count > MaxEvents {
		return nil, ErrTooMany
	}
	numCtx, rest, err := readUvarint(rest)
	if err != nil {
		return nil, err
	}
	if numCtx < 1 || numCtx > MaxContexts {
		return nil, ErrNumContexts
	}
	if int(numCtx) > maxContexts {
		return nil, ErrContextLimit
	}
	payloadLen, rest, err := readUvarint(rest)
	if err != nil {
		return nil, err
	}
	if uint64(len(rest)) < count {
		return nil, ErrTruncated
	}
	labels := rest[:count]
	rest = rest[count:]
	for _, lb := range labels {
		if uint64(lb) >= numCtx {
			return nil, ErrBadLabel
		}
	}
	if len(rest) < 4 {
		return nil, ErrTruncated
	}
	wantCRC := binary.LittleEndian.Uint32(rest[:4])
	rest = rest[4:]
	if uint64(len(rest)) != payloadLen {
		if uint64(len(rest)) < payloadLen {
			return nil, ErrTruncated
		}
		return nil, ErrTrailing
	}
	payload := rest

	ctxs := make([]context, numCtx)
	for i := range ctxs {
		ctxs[i] = newContext()
	}
	d := newCoreDecoder(payload)
	events := make([]Event, 0, count)
	var crc uint32
	var crcIn [2]byte
	for i := uint64(0); i < count; i++ {
		lb := labels[i]
		c := &ctxs[lb]
		bit := d.decodeBit(c.zero, c.total())
		c.update(bit) // 与编码器完全相同的位置更新模型
		events = append(events, Event{Label: lb, Bit: bit})
		crcIn[0], crcIn[1] = lb, bit
		crc = crc32.Update(crc, crcTable, crcIn[:])
	}
	if crc != wantCRC {
		return nil, ErrChecksum
	}
	return events, nil
}

func readUvarint(b []byte) (uint64, []byte, error) {
	v, n := binary.Uvarint(b)
	if n == 0 {
		return 0, nil, ErrTruncated
	}
	if n < 0 {
		return 0, nil, ErrTooMany
	}
	return v, b[n:], nil
}

// ---- 暂停 / 恢复 ----
//
// 快照包含编码器的全部状态（含已产出的字节与半字节缓冲），
// 恢复后继续编码与一次性编码逐字节一致。快照布局见 FORMAT.md。

var snapMagic = []byte("ACXS")

// Snapshot 序列化当前编码器状态。已调用 Frame 的编码器不能再快照。
func (e *Encoder) Snapshot() ([]byte, error) {
	if e.done {
		return nil, ErrFinalized
	}
	var buf bytes.Buffer
	buf.Write(snapMagic)
	buf.Write(uvarint(uint64(e.numCtx)))
	buf.Write(uvarint(e.count))
	var fixed [8]byte
	binary.LittleEndian.PutUint32(fixed[:4], e.crc)
	buf.Write(fixed[:4])
	binary.LittleEndian.PutUint64(fixed[:8], e.core.low)
	buf.Write(fixed[:8])
	binary.LittleEndian.PutUint64(fixed[:8], e.core.high)
	buf.Write(fixed[:8])
	binary.LittleEndian.PutUint64(fixed[:8], e.core.follow)
	buf.Write(fixed[:8])
	buf.WriteByte(e.core.w.cur)
	buf.WriteByte(e.core.w.nbits)
	buf.Write(uvarint(uint64(len(e.labels))))
	buf.Write(e.labels)
	buf.Write(uvarint(uint64(len(e.core.w.out))))
	buf.Write(e.core.w.out)
	for _, c := range e.ctxs {
		binary.LittleEndian.PutUint32(fixed[:4], c.zero)
		buf.Write(fixed[:4])
		binary.LittleEndian.PutUint32(fixed[:4], c.one)
		buf.Write(fixed[:4])
	}
	return buf.Bytes(), nil
}

// Resume 从快照恢复编码器，之后可继续 Put / Frame。
func Resume(snap []byte) (*Encoder, error) {
	r := &snapReader{b: snap}
	magic4 := r.bytes(4)
	if r.err || !bytes.Equal(magic4, snapMagic) {
		return nil, ErrBadSnapshot
	}
	numCtx := r.uvarint()
	count := r.uvarint()
	crc := r.uint32()
	low := r.uint64()
	high := r.uint64()
	follow := r.uint64()
	cur := r.byte()
	nbits := r.byte()
	labels := r.bytes(int(r.uvarint()))
	out := r.bytes(int(r.uvarint()))
	if r.err || numCtx < 1 || numCtx > MaxContexts || count > MaxEvents ||
		high > topValue || low > high || nbits > 7 {
		return nil, ErrBadSnapshot
	}
	ctxs := make([]context, numCtx)
	for i := range ctxs {
		ctxs[i].zero = r.uint32()
		ctxs[i].one = r.uint32()
	}
	if r.err || len(r.b) != 0 {
		return nil, ErrBadSnapshot
	}
	for _, c := range ctxs {
		if c.zero < 1 || c.one < 1 || c.zero+c.one > maxTotal {
			return nil, ErrBadSnapshot
		}
	}
	return &Encoder{
		numCtx: int(numCtx),
		count:  count,
		crc:    crc,
		labels: append([]byte(nil), labels...),
		ctxs:   ctxs,
		core: coreEncoder{
			low:    low,
			high:   high,
			follow: follow,
			w:      bitWriter{out: append([]byte(nil), out...), cur: cur, nbits: nbits},
		},
	}, nil
}

type snapReader struct {
	b   []byte
	err bool
}

func (r *snapReader) bytes(n int) []byte {
	if n < 0 || len(r.b) < n {
		r.err = true
		return nil
	}
	v := r.b[:n]
	r.b = r.b[n:]
	return v
}

func (r *snapReader) byte() uint8 {
	v := r.bytes(1)
	if v == nil {
		return 0
	}
	return v[0]
}

func (r *snapReader) uint32() uint32 {
	v := r.bytes(4)
	if v == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(v)
}

func (r *snapReader) uint64() uint64 {
	v := r.bytes(8)
	if v == nil {
		return 0
	}
	return binary.LittleEndian.Uint64(v)
}

func (r *snapReader) uvarint() uint64 {
	v, n := binary.Uvarint(r.b)
	if n <= 0 {
		r.err = true
		return 0
	}
	r.b = r.b[n:]
	return v
}

// hexDump 供测试与文档调试使用。
func hexDump(b []byte) string { return hex.EncodeToString(b) }
