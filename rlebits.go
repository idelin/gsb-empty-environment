// Package rlebits 实现游程编码（RLE）的可变位序列。
//
// 内存布局：
//
//	start byte     首段的 bit 值（0/1），之后各段值按奇偶交替推出
//	runs []uint64  各段长度，相邻段值必不同，不存在零长段
//	pos  []uint64  长度索引：位位置前缀和，pos[i] = sum(runs[:i])
//	ones []uint64  长度索引：1 的个数前缀和，用于 rank/select
//
// 磁盘编码（Encode 输出）：
//
//	偏移 0: magic   4 字节 "RLB1"
//	偏移 4: flags   1 字节，bit0 = 首段值
//	偏移 5: numRuns uvarint
//	其后:  totalLen uvarint（总位数，用于校验）
//	其后:  runs     uvarint × numRuns（各段长度）
package rlebits

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
)

var magic = []byte{'R', 'L', 'B', '1'}

// Bits 是游程压缩的位序列。零值不可用，请用 New / FromBits / FromString / Decode 构造。
type Bits struct {
	start byte     // 首段值
	runs  []uint64 // 段长度，交替表示 start, 1-start, start, ...
	pos   []uint64 // 位位置前缀和，长度 len(runs)+1
	ones  []uint64 // 1 的个数前缀和，长度 len(runs)+1
}

// New 构造空序列。
func New() *Bits { return &Bits{} }

// FromBits 由 0/1 切片构造。
func FromBits(bits []byte) *Bits {
	b := &Bits{}
	if len(bits) == 0 {
		b.rebuild()
		return b
	}
	b.start = bits[0] & 1
	cur := b.start
	var n uint64
	for _, v := range bits {
		v &= 1
		if v == cur {
			n++
		} else {
			b.runs = append(b.runs, n)
			cur = v
			n = 1
		}
	}
	b.runs = append(b.runs, n)
	b.rebuild()
	return b
}

// FromString 由 "01001..." 构造。
func FromString(s string) *Bits {
	bits := make([]byte, 0, len(s))
	for _, c := range s {
		switch c {
		case '0':
			bits = append(bits, 0)
		case '1':
			bits = append(bits, 1)
		default:
			panic(fmt.Sprintf("rlebits: bad char %q", c))
		}
	}
	return FromBits(bits)
}

func (b *Bits) runValue(i int) byte {
	if i%2 == 0 {
		return b.start
	}
	return 1 - b.start
}

// rebuild 重建长度索引（pos / ones 前缀和）。
func (b *Bits) rebuild() {
	n := len(b.runs) + 1
	if cap(b.pos) < n {
		b.pos = make([]uint64, n)
		b.ones = make([]uint64, n)
	} else {
		b.pos = b.pos[:n]
		b.ones = b.ones[:n]
	}
	b.pos[0], b.ones[0] = 0, 0
	for i, l := range b.runs {
		b.pos[i+1] = b.pos[i] + l
		b.ones[i+1] = b.ones[i]
		if b.runValue(i) == 1 {
			b.ones[i+1] += l
		}
	}
}

// runIndexAt 返回覆盖位位置 p 的段下标；p >= Len 时返回 len(runs)。
func (b *Bits) runIndexAt(p uint64) int {
	return sort.Search(len(b.runs), func(i int) bool { return b.pos[i+1] > p })
}

// splitAt 在位置 p 处切分段，使 p 成为段边界；p 已在边界或越界时不动。
// 返回 true 表示真的切开了一个段（会暂时产生相邻同值段，
// 调用方必须据此决定后续的合并行为）。
func (b *Bits) splitAt(p uint64) bool {
	if p == 0 || p >= b.Len() {
		return false
	}
	i := b.runIndexAt(p)
	if b.pos[i] == p {
		return false
	}
	off := p - b.pos[i]
	l := b.runs[i]
	b.runs[i] = off
	b.runs = append(b.runs, 0)
	copy(b.runs[i+2:], b.runs[i+1:])
	b.runs[i+1] = l - off
	b.rebuild()
	return true
}

// Len 返回总位数。
func (b *Bits) Len() uint64 {
	if len(b.pos) == 0 {
		return 0
	}
	return b.pos[len(b.pos)-1]
}

// NumRuns 返回段数。
func (b *Bits) NumRuns() int { return len(b.runs) }

// At 返回位置 p 的 bit（0/1）。
func (b *Bits) At(p uint64) byte {
	if p >= b.Len() {
		panic("rlebits: index out of range")
	}
	return b.runValue(b.runIndexAt(p))
}

// Expand 展开为 0/1 切片。
func (b *Bits) Expand() []byte {
	out := make([]byte, 0, b.Len())
	for i := range b.runs {
		v := b.runValue(i)
		for k := uint64(0); k < b.runs[i]; k++ {
			out = append(out, v)
		}
	}
	return out
}

// String 返回 "01001..." 形式。
func (b *Bits) String() string {
	var sb strings.Builder
	sb.Grow(int(b.Len()))
	for _, v := range b.Expand() {
		sb.WriteByte('0' + v)
	}
	return sb.String()
}

// Flip 翻转区间 [l, r) 内的所有 bit。
//
// 原理：在 l、r 处切分段后，区间内各段值取反仍保持交替。
// 若端点原本是天然段边界（未发生切分），取反后的首/尾段与外侧邻居
// 同值，需要合并；若端点是切分产生的（切分制造了一对同值段），取反后
// 二者恰好错开，无需合并。由此自动维护“相邻同值段合并”的不变量。
func (b *Bits) Flip(l, r uint64) {
	n := b.Len()
	if l >= n || l >= r {
		return
	}
	if r > n {
		r = n
	}
	splitL := b.splitAt(l)
	splitR := b.splitAt(r)
	lo := b.runIndexAt(l)
	hi := b.runIndexAt(r) // 段 [lo, hi) 被翻转
	if lo == 0 {
		b.start ^= 1
	} else if !splitL {
		b.runs[lo-1] += b.runs[lo]
		b.runs = append(b.runs[:lo], b.runs[lo+1:]...)
		hi--
	}
	if hi > 0 && hi < len(b.runs) && !splitR {
		b.runs[hi-1] += b.runs[hi]
		b.runs = append(b.runs[:hi], b.runs[hi+1:]...)
	}
	b.rebuild()
}

// Insert 在位置 p 前插入 count 个值为 value 的 bit。
// p 在段边界时直接延长相邻同值段，不会产生重复位置或零长段。
func (b *Bits) Insert(p uint64, value byte, count uint64) {
	if count == 0 {
		return
	}
	value &= 1
	n := b.Len()
	if p > n {
		p = n
	}
	if len(b.runs) == 0 {
		b.start = value
		b.runs = []uint64{count}
		b.rebuild()
		return
	}
	if p == n { // 尾部追加
		last := len(b.runs) - 1
		if b.runValue(last) == value {
			b.runs[last] += count
		} else {
			b.runs = append(b.runs, count)
		}
		b.rebuild()
		return
	}
	i := b.runIndexAt(p)
	if b.pos[i] == p { // 段边界
		if i > 0 && b.runValue(i-1) == value {
			b.runs[i-1] += count
		} else if b.runValue(i) == value {
			b.runs[i] += count
		} else { // i == 0 且与首段异值：头部插入新段
			b.runs = append([]uint64{count}, b.runs...)
			b.start = value
		}
		b.rebuild()
		return
	}
	// 段内部
	if b.runValue(i) == value {
		b.runs[i] += count
	} else {
		off := p - b.pos[i]
		l := b.runs[i]
		b.runs[i] = off
		b.runs = append(b.runs, 0, 0)
		copy(b.runs[i+3:], b.runs[i+1:])
		b.runs[i+1] = count
		b.runs[i+2] = l - off
	}
	b.rebuild()
}

// Delete 删除区间 [l, r)。跨多段删除后合并交界同值段，不留零长段。
func (b *Bits) Delete(l, r uint64) {
	n := b.Len()
	if l >= n || l >= r {
		return
	}
	if r > n {
		r = n
	}
	splitL := b.splitAt(l)
	splitR := b.splitAt(r)
	lo := b.runIndexAt(l)
	hi := b.runIndexAt(r) // 段 [lo, hi) 被删除
	removed := hi - lo
	b.runs = append(b.runs[:lo], b.runs[hi:]...)
	// 从头部删除时新首段的值 = 旧首段值 ^ (removed+splitR 的奇偶)
	// （splitR 切分出的后半段与前任一段同值，要多算一次）
	if lo == 0 && (int(removed%2)+b2i(splitR))%2 == 1 {
		b.start ^= 1
	}
	// 交界处左右两段是否同值，取决于删除段数与两端点是否发生切分：
	// removed+splitL+splitR 为奇数时同值，必须合并（不留同值相邻段）。
	merge := int(removed%2) + b2i(splitL) + b2i(splitR)
	if merge%2 == 1 && lo > 0 && lo < len(b.runs) {
		b.runs[lo-1] += b.runs[lo]
		b.runs = append(b.runs[:lo], b.runs[lo+1:]...)
	}
	b.rebuild()
}

func b2i(v bool) int {
	if v {
		return 1
	}
	return 0
}

// Rank1 返回 [0, p) 内 1 的个数。
func (b *Bits) Rank1(p uint64) uint64 {
	if p == 0 || len(b.runs) == 0 {
		return 0
	}
	if p >= b.Len() {
		return b.ones[len(b.ones)-1]
	}
	i := b.runIndexAt(p)
	c := b.ones[i]
	if b.runValue(i) == 1 {
		c += p - b.pos[i]
	}
	return c
}

// Rank0 返回 [0, p) 内 0 的个数。
func (b *Bits) Rank0(p uint64) uint64 {
	if p > b.Len() {
		p = b.Len()
	}
	return p - b.Rank1(p)
}

// Select1 返回第 k 个 1（从 0 计）的位置；不存在时 ok=false。
func (b *Bits) Select1(k uint64) (pos uint64, ok bool) {
	if len(b.runs) == 0 || k >= b.ones[len(b.ones)-1] {
		return 0, false
	}
	j := sort.Search(len(b.runs), func(i int) bool { return b.ones[i+1] > k })
	return b.pos[j] + (k - b.ones[j]), true
}

// Select0 返回第 k 个 0（从 0 计）的位置；不存在时 ok=false。
func (b *Bits) Select0(k uint64) (pos uint64, ok bool) {
	if len(b.runs) == 0 {
		return 0, false
	}
	total0 := b.pos[len(b.pos)-1] - b.ones[len(b.ones)-1]
	if k >= total0 {
		return 0, false
	}
	j := sort.Search(len(b.runs), func(i int) bool {
		return b.pos[i+1]-b.ones[i+1] > k
	})
	return b.pos[j] + (k - (b.pos[j] - b.ones[j])), true
}

// Encode 按文件头注释的布局编码为字节流。
func (b *Bits) Encode() []byte {
	var flags byte
	if b.start == 1 {
		flags |= 1
	}
	out := append([]byte{}, magic...)
	out = append(out, flags)
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], uint64(len(b.runs)))
	out = append(out, tmp[:n]...)
	n = binary.PutUvarint(tmp[:], b.Len())
	out = append(out, tmp[:n]...)
	for _, l := range b.runs {
		n := binary.PutUvarint(tmp[:], l)
		out = append(out, tmp[:n]...)
	}
	return out
}

// Decode 解码 Encode 的输出。
func Decode(data []byte) (*Bits, error) {
	if len(data) < 5 || string(data[:4]) != string(magic) {
		return nil, errors.New("rlebits: bad magic or truncated header")
	}
	flags := data[4]
	rest := data[5:]
	readU := func() (uint64, error) {
		v, n := binary.Uvarint(rest)
		if n <= 0 {
			return 0, errors.New("rlebits: corrupt varint")
		}
		rest = rest[n:]
		return v, nil
	}
	numRuns, err := readU()
	if err != nil {
		return nil, err
	}
	total, err := readU()
	if err != nil {
		return nil, err
	}
	b := &Bits{start: flags & 1}
	var sum uint64
	for i := uint64(0); i < numRuns; i++ {
		l, err := readU()
		if err != nil {
			return nil, err
		}
		if l == 0 {
			return nil, errors.New("rlebits: zero-length run in stream")
		}
		b.runs = append(b.runs, l)
		sum += l
	}
	if sum != total {
		return nil, errors.New("rlebits: run lengths do not sum to total length")
	}
	if len(rest) != 0 {
		return nil, errors.New("rlebits: trailing bytes")
	}
	b.rebuild()
	return b, nil
}

// EncodedSize 返回 Encode 输出的字节数。
func (b *Bits) EncodedSize() int { return len(b.Encode()) }

// RawSize 返回未压缩位图所需字节数。
func (b *Bits) RawSize() uint64 { return (b.Len() + 7) / 8 }

// IndexBytes 返回内存中长度索引（pos + ones 前缀和）占用的字节数。
func (b *Bits) IndexBytes() int { return 2 * (len(b.runs) + 1) * 8 }
