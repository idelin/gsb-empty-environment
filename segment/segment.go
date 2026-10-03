package segment

import (
	"encoding/binary"
	"errors"
	"sort"
)

// Segment 是只读倒排索引段，直接在序列化字节上零拷贝检索。
type Segment struct {
	postings  []byte // 倒排区
	dictBody  []byte // 词典块体（块索引之后）
	blockIdx  []dictBlock
	termCount int
}

type dictBlock struct {
	head string
	off  int
}

// Open 打开 Build 产出的段字节流。
func Open(data []byte) (*Segment, error) {
	if len(data) < 4 || string(data[:4]) != magic {
		return nil, errors.New("segment: bad magic")
	}
	pos := 4
	termCount, n := binary.Uvarint(data[pos:])
	if n <= 0 {
		return nil, errors.New("segment: corrupt header")
	}
	pos += n
	dictLen, n := binary.Uvarint(data[pos:])
	if n <= 0 {
		return nil, errors.New("segment: corrupt header")
	}
	pos += n
	dict := data[pos : pos+int(dictLen)]
	postings := data[pos+int(dictLen):]

	// 解析词典块索引。
	dpos := 0
	nblocks, n := binary.Uvarint(dict[dpos:])
	if n <= 0 {
		return nil, errors.New("segment: corrupt dictionary")
	}
	dpos += n
	idx := make([]dictBlock, 0, nblocks)
	for i := 0; i < int(nblocks); i++ {
		l, n := binary.Uvarint(dict[dpos:])
		dpos += n
		head := string(dict[dpos : dpos+int(l)])
		dpos += int(l)
		off, n := binary.Uvarint(dict[dpos:])
		dpos += n
		idx = append(idx, dictBlock{head, int(off)})
	}
	return &Segment{
		postings:  postings,
		dictBody:  dict[dpos:],
		blockIdx:  idx,
		termCount: int(termCount),
	}, nil
}

// TermCount 返回段内词项总数。
func (s *Segment) TermCount() int { return s.termCount }

// lookup 在词典中查找词，返回其倒排列表位置。
func (s *Segment) lookup(term string) (postingRef, bool) {
	// 二分块索引：找到最后一个块首词 <= term 的块。
	i := sort.Search(len(s.blockIdx), func(i int) bool {
		return s.blockIdx[i].head > term
	}) - 1
	if i < 0 {
		return postingRef{}, false
	}
	pos := s.blockIdx[i].off
	end := len(s.dictBody)
	if i+1 < len(s.blockIdx) {
		end = s.blockIdx[i+1].off
	}
	cur := ""
	for pos < end {
		p, n := binary.Uvarint(s.dictBody[pos:])
		pos += n
		l, n := binary.Uvarint(s.dictBody[pos:])
		pos += n
		cur = cur[:p] + string(s.dictBody[pos:pos+int(l)])
		pos += int(l)
		off, n := binary.Uvarint(s.dictBody[pos:])
		pos += n
		length, n := binary.Uvarint(s.dictBody[pos:])
		pos += n
		if cur == term {
			return postingRef{int(off), int(length)}, true
		}
		if cur > term {
			return postingRef{}, false
		}
	}
	return postingRef{}, false
}

// Has 报告词是否存在于词典中。
func (s *Segment) Has(term string) bool {
	_, ok := s.lookup(term)
	return ok
}

// Postings 返回词的倒排列表迭代器；词不存在时返回 nil。
func (s *Segment) Postings(term string) *Iterator {
	r, ok := s.lookup(term)
	if !ok {
		return nil
	}
	return newIterator(s.postings[r.off : r.off+r.length])
}

// Iterator 是倒排列表的只读游标，支持 Next 顺序解码与
// Advance 借助跳跃指针快速前进。
type Iterator struct {
	data   []byte // delta/tf 数据区
	skips  []skipEntry
	count  int
	pos    int // data 内的字节偏移
	idx    int // 已解码的 posting 数
	prev   uint32
	curDoc uint32
	curTF  uint32
	done   bool

	// 统计信息，用于验证跳跃指针的加速效果。
	Decoded   int // 实际解码的 posting 条数
	SkipsUsed int // 跳跃指针命中次数
}

type skipEntry struct {
	base uint32 // 跳跃目标前一条 posting 的绝对 docID
	off  int    // 跳跃目标在数据区的字节偏移
}

func newIterator(buf []byte) *Iterator {
	pos := 0
	count, n := binary.Uvarint(buf)
	pos += n
	nskips, n := binary.Uvarint(buf[pos:])
	pos += n
	skips := make([]skipEntry, 0, nskips)
	for i := 0; i < int(nskips); i++ {
		base, n := binary.Uvarint(buf[pos:])
		pos += n
		off, n := binary.Uvarint(buf[pos:])
		pos += n
		skips = append(skips, skipEntry{uint32(base), int(off)})
	}
	return &Iterator{data: buf[pos:], skips: skips, count: int(count)}
}

// Len 返回倒排列表长度（文档数）。
func (it *Iterator) Len() int { return it.count }

// Doc 返回当前 posting 的文档 ID（需先成功调用 Next/Advance）。
func (it *Iterator) Doc() uint32 { return it.curDoc }

// TF 返回当前 posting 的词频。
func (it *Iterator) TF() uint32 { return it.curTF }

// Next 解码下一条 posting，耗尽时返回 false。
func (it *Iterator) Next() bool {
	if it.done || it.idx >= it.count {
		it.done = true
		return false
	}
	delta, n := binary.Uvarint(it.data[it.pos:])
	it.pos += n
	tf, n := binary.Uvarint(it.data[it.pos:])
	it.pos += n
	it.prev += uint32(delta)
	it.curDoc = it.prev
	it.curTF = uint32(tf)
	it.idx++
	it.Decoded++
	return true
}

// Advance 前进到第一个 docID >= target 的 posting。
// 先对跳跃指针表二分，直接跳过整段无用区间，再在块内解码。
func (it *Iterator) Advance(target uint32) bool {
	if it.done {
		return false
	}
	if it.idx > 0 && it.curDoc >= target {
		return true
	}
	// 最后一个 base < target 的跳跃指针：其之前的 posting 全部 < target。
	k := sort.Search(len(it.skips), func(i int) bool {
		return it.skips[i].base >= target
	}) - 1
	if k >= 0 {
		targetIdx := (k + 1) * skipInterval
		if targetIdx > it.idx {
			it.prev = it.skips[k].base
			it.pos = it.skips[k].off
			it.idx = targetIdx
			it.SkipsUsed++
		}
	}
	for it.Next() {
		if it.curDoc >= target {
			return true
		}
	}
	return false
}
