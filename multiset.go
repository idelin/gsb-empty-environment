// Package multiset 实现一个按值有序、支持重复元素的压缩多重集合。
//
// 内存布局（三条平行切片，按值升序）：
//
//	values []uint64  // 去重后的元素值，严格递增
//	counts []uint64  // 每个值的重数（>= 1）
//	prefix []uint64  // 累计重数：prefix[i] = counts[0]+...+counts[i]
//
// 磁盘编码（Encode 输出，全部使用 uvarint 变长编码）：
//
//	偏移  内容
//	0     magic "MS01"（4 字节）
//	4     uvarint N（去重后值的个数）
//	...   N 个 uvarint：值的 delta 编码（首值存绝对值，之后存与前值的差）
//	...   N 个 uvarint：每个值的重数
//	...   N 个 uvarint：累计重数的 delta 编码（单调递增，存相邻差）
package multiset

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

var (
	ErrEmpty       = errors.New("multiset: 空集合")
	ErrOutOfRange  = errors.New("multiset: 选择序号越界")
	ErrNegativeCnt = errors.New("multiset: 计数不能为负，整批回滚")
)

var magic = []byte{'M', 'S', '0', '1'}

// Delta 表示一次批量更新中的一项：对 Value 增加 Delta（可为负）个重数。
type Delta struct {
	Value uint64
	Delta int64
}

// Multiset 是按值有序存储计数与累计重数的多重集合。
type Multiset struct {
	values []uint64
	counts []uint64
	prefix []uint64
}

// New 返回一个空多重集合。
func New() *Multiset { return &Multiset{} }

// Len 返回元素总数（含重复），即累计重数。
func (m *Multiset) Len() uint64 {
	if len(m.prefix) == 0 {
		return 0
	}
	return m.prefix[len(m.prefix)-1]
}

// Distinct 返回去重后的值个数。
func (m *Multiset) Distinct() int { return len(m.values) }

// Count 返回某个值的重数。
func (m *Multiset) Count(v uint64) uint64 {
	i := m.search(v)
	if i < len(m.values) && m.values[i] == v {
		return m.counts[i]
	}
	return 0
}

// search 返回 values 中第一个 >= v 的下标。
func (m *Multiset) search(v uint64) int {
	return sort.Search(len(m.values), func(i int) bool { return m.values[i] >= v })
}

// Select 返回第 k 小元素（k 从 1 开始计，重复元素各占一位）。
// 例如集合 [1,1,3] 中 Select(2) == 1。
func (m *Multiset) Select(k uint64) (uint64, error) {
	if len(m.prefix) == 0 {
		return 0, ErrEmpty
	}
	if k == 0 || k > m.Len() {
		return 0, ErrOutOfRange
	}
	// 第一个 prefix[i] >= k 的 i 即为所求。
	i := sort.Search(len(m.prefix), func(i int) bool { return m.prefix[i] >= k })
	return m.values[i], nil
}

// rebuildPrefix 重新计算累计重数。
func (m *Multiset) rebuildPrefix() {
	var sum uint64
	for i, c := range m.counts {
		sum += c
		m.prefix[i] = sum
	}
}

// ApplyBatch 事务化地应用一批增减：先整体校验，任何一项导致计数为负
// 则整批回滚（集合保持不变）并返回 ErrNegativeCnt。
func (m *Multiset) ApplyBatch(deltas []Delta) error {
	// 复制一份工作副本，在校验通过前不触碰原数据。
	vals := append([]uint64(nil), m.values...)
	cnts := append([]uint64(nil), m.counts...)

	for _, d := range deltas {
		i := sort.Search(len(vals), func(i int) bool { return vals[i] >= d.Value })
		switch {
		case i < len(vals) && vals[i] == d.Value:
			next := int64(cnts[i]) + d.Delta
			if next < 0 {
				return fmt.Errorf("%w: 值 %d 的重数将变为 %d", ErrNegativeCnt, d.Value, next)
			}
			if next == 0 {
				vals = append(vals[:i], vals[i+1:]...)
				cnts = append(cnts[:i], cnts[i+1:]...)
			} else {
				cnts[i] = uint64(next)
			}
		default:
			if d.Delta < 0 {
				return fmt.Errorf("%w: 值 %d 不存在却要减 %d", ErrNegativeCnt, d.Value, -d.Delta)
			}
			if d.Delta == 0 {
				continue
			}
			vals = append(vals, 0)
			copy(vals[i+1:], vals[i:])
			vals[i] = d.Value
			cnts = append(cnts, 0)
			copy(cnts[i+1:], cnts[i:])
			cnts[i] = uint64(d.Delta)
		}
	}

	// 校验全部通过，原子切换。
	m.values = vals
	m.counts = cnts
	m.prefix = make([]uint64, len(cnts))
	m.rebuildPrefix()
	return nil
}

// Intersect 返回两个多重集合的交集：每个公共值取两者重数的最小值。
func (m *Multiset) Intersect(other *Multiset) *Multiset {
	out := New()
	i, j := 0, 0
	for i < len(m.values) && j < len(other.values) {
		switch {
		case m.values[i] < other.values[j]:
			i++
		case m.values[i] > other.values[j]:
			j++
		default:
			c := m.counts[i]
			if other.counts[j] < c {
				c = other.counts[j]
			}
			out.values = append(out.values, m.values[i])
			out.counts = append(out.counts, c)
			i++
			j++
		}
	}
	out.prefix = make([]uint64, len(out.counts))
	out.rebuildPrefix()
	return out
}

// Encode 按文件头注释的布局编码为字节流，返回编码结果与总字节数。
func (m *Multiset) Encode() []byte {
	buf := append([]byte(nil), magic...)
	var tmp [binary.MaxVarintLen64]byte
	put := func(x uint64) {
		n := binary.PutUvarint(tmp[:], x)
		buf = append(buf, tmp[:n]...)
	}

	n := uint64(len(m.values))
	put(n)
	// 值：delta 编码。
	var prev uint64
	for i, v := range m.values {
		if i == 0 {
			put(v)
		} else {
			put(v - prev)
		}
		prev = v
	}
	// 重数。
	for _, c := range m.counts {
		put(c)
	}
	// 累计重数：单调递增，delta 编码。
	prev = 0
	for _, p := range m.prefix {
		put(p - prev)
		prev = p
	}
	return buf
}

// EncodedSize 返回 Encode 输出的字节数。
func (m *Multiset) EncodedSize() int { return len(m.Encode()) }

// Decode 从 Encode 的输出还原多重集合。
func Decode(data []byte) (*Multiset, error) {
	if len(data) < 4 || !bytes.Equal(data[:4], magic) {
		return nil, errors.New("multiset: 非法魔数")
	}
	rest := data[4:]
	get := func() (uint64, error) {
		x, n := binary.Uvarint(rest)
		if n <= 0 {
			return 0, errors.New("multiset: 编码损坏")
		}
		rest = rest[n:]
		return x, nil
	}

	n, err := get()
	if err != nil {
		return nil, err
	}
	m := New()
	m.values = make([]uint64, n)
	m.counts = make([]uint64, n)
	m.prefix = make([]uint64, n)

	var prev uint64
	for i := range m.values {
		d, err := get()
		if err != nil {
			return nil, err
		}
		if i == 0 {
			m.values[i] = d
		} else {
			m.values[i] = prev + d
		}
		prev = m.values[i]
	}
	for i := range m.counts {
		c, err := get()
		if err != nil {
			return nil, err
		}
		if c == 0 {
			return nil, errors.New("multiset: 重数不能为 0")
		}
		m.counts[i] = c
	}
	prev = 0
	for i := range m.prefix {
		d, err := get()
		if err != nil {
			return nil, err
		}
		m.prefix[i] = prev + d
		prev = m.prefix[i]
	}
	if len(rest) != 0 {
		return nil, errors.New("multiset: 编码尾部有多余字节")
	}
	// 校验累计重数与计数一致。
	var sum uint64
	for i, c := range m.counts {
		sum += c
		if m.prefix[i] != sum {
			return nil, errors.New("multiset: 累计重数与计数不一致")
		}
	}
	return m, nil
}
