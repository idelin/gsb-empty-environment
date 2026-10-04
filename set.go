// Package set32 实现 32 位无符号整数集合：
// 按高 16 位分块，每块的低 16 位根据“实际序列化字节数最小”
// 在排序数组、固定位图、互不相邻闭区间 runs 三种容器间选择。
package set32

import (
	"errors"
	"sort"
)

// ErrSelectRange 是 Select 收到非法 k 时返回的错误。
var ErrSelectRange = errors.New("set32: select index out of range")

// block 是一个高 16 位分块。n 为元素数，rc 为 run 数（随增删增量维护，
// 与容器内容始终一致），shared 用于快照的写时复制。
type block struct {
	key    uint16
	c      container
	n      int
	rc     int
	shared bool
}

// Set 是 32 位无符号整数集合。零值即可用。
// 内部只保存按高 16 位排序的块切片，集合运算按块归并，
// 不会把 32 位全域展开成巨大数组。
type Set struct {
	blocks []*block
}

// New 返回空集合。
func New() *Set { return &Set{} }

// find 返回 key 所在块的下标；未找到时 ok=false。
func (s *Set) find(key uint16) (idx int, ok bool) {
	i := sort.Search(len(s.blocks), func(i int) bool { return s.blocks[i].key >= key })
	if i < len(s.blocks) && s.blocks[i].key == key {
		return i, true
	}
	return i, false
}

// writable 返回可安全修改的块（快照共享时先克隆）。
func (s *Set) writable(i int) *block {
	b := s.blocks[i]
	if b.shared {
		nb := &block{key: b.key, c: b.c.clone(), n: b.n, rc: b.rc}
		s.blocks[i] = nb
		return nb
	}
	return b
}

// normalize 在内容变化后把容器转换为当前最优类型。
func (b *block) normalize() {
	want := desiredKind(b.n, b.rc)
	if want != b.c.containerKind() {
		b.c = convert(b.c, want)
	}
}

// Add 插入元素；已存在时无效果。
func (s *Set) Add(x uint32) {
	key, lo := uint16(x>>16), uint16(x)
	i, ok := s.find(key)
	if !ok {
		nb := &block{key: key, c: &arrayC{data: []uint16{lo}}, n: 1, rc: 1}
		s.blocks = append(s.blocks, nil)
		copy(s.blocks[i+1:], s.blocks[i:])
		s.blocks[i] = nb
		return
	}
	b := s.writable(i)
	if b.c.contains(lo) {
		return
	}
	left := lo > 0 && b.c.contains(lo-1)
	right := lo < 0xffff && b.c.contains(lo+1)
	nc, changed := b.c.add(lo)
	if !changed {
		return
	}
	b.c = nc
	b.n++
	b.rc += 1
	if left {
		b.rc--
	}
	if right {
		b.rc--
	}
	b.normalize()
}

// Remove 删除元素；不存在时无效果。块变空时整块移除。
func (s *Set) Remove(x uint32) {
	key, lo := uint16(x>>16), uint16(x)
	i, ok := s.find(key)
	if !ok {
		return
	}
	b := s.writable(i)
	if !b.c.contains(lo) {
		return
	}
	left := lo > 0 && b.c.contains(lo-1)
	right := lo < 0xffff && b.c.contains(lo+1)
	nc, changed := b.c.remove(lo)
	if !changed {
		return
	}
	b.c = nc
	b.n--
	b.rc -= 1
	if left {
		b.rc++
	}
	if right {
		b.rc++
	}
	if b.n == 0 {
		copy(s.blocks[i:], s.blocks[i+1:])
		s.blocks[len(s.blocks)-1] = nil
		s.blocks = s.blocks[:len(s.blocks)-1]
		return
	}
	b.normalize()
}

// Contains 判断元素是否存在。
func (s *Set) Contains(x uint32) bool {
	i, ok := s.find(uint16(x >> 16))
	return ok && s.blocks[i].c.contains(uint16(x))
}

// Len 返回元素总数。
func (s *Set) Len() int {
	n := 0
	for _, b := range s.blocks {
		n += b.n
	}
	return n
}

// Rank 返回集合中 <= x 的元素个数。
func (s *Set) Rank(x uint32) int {
	key, lo := uint16(x>>16), uint16(x)
	n := 0
	for _, b := range s.blocks {
		if b.key < key {
			n += b.n
			continue
		}
		if b.key == key {
			n += b.c.rankLe(lo)
		}
		break
	}
	return n
}

// Select 返回从 0 开始第 k 小的元素；k 越界时报错。
func (s *Set) Select(k int) (uint32, error) {
	if k < 0 {
		return 0, ErrSelectRange
	}
	for _, b := range s.blocks {
		if k < b.n {
			v, ok := b.c.selectK(k)
			if !ok {
				return 0, ErrSelectRange
			}
			return uint32(b.key)<<16 | uint32(v), nil
		}
		k -= b.n
	}
	return 0, ErrSelectRange
}

// Collect 按升序返回全部元素。
func (s *Set) Collect() []uint32 {
	out := make([]uint32, 0, s.Len())
	for _, b := range s.blocks {
		base := uint32(b.key) << 16
		for _, r := range b.c.toRuns() {
			for v := uint32(r.lo); v <= uint32(r.hi); v++ {
				out = append(out, base|v)
			}
		}
	}
	return out
}

// Snapshot 返回快照；之后对任一集合的修改不会污染另一个（写时复制）。
func (s *Set) Snapshot() *Set {
	nb := make([]*block, len(s.blocks))
	copy(nb, s.blocks)
	for _, b := range s.blocks {
		b.shared = true
	}
	return &Set{blocks: nb}
}

// 集合运算的容器级组合：统一经 runs 归并完成，再按最优类型落容器。
func combineContainers(a, b container, op uint8) (container, int, int) {
	ra, rb := a.toRuns(), b.toRuns()
	var rs []run
	switch op {
	case 0:
		rs = runsAnd(ra, rb)
	case 1:
		rs = runsOr(ra, rb)
	default:
		rs = runsXor(ra, rb)
	}
	n := runsCardinality(rs)
	rc := len(rs)
	if n == 0 {
		return nil, 0, 0
	}
	switch desiredKind(n, rc) {
	case kindArray:
		return &arrayC{data: arrayFromRuns(rs)}, n, rc
	case kindBitmap:
		return &bitmapC{words: bitmapFromRuns(rs), n: n}, n, rc
	default:
		return &runsC{runs: rs}, n, rc
	}
}

// setOp 按块 key 归并两个集合。op: 0=AND 1=OR 2=XOR。
func setOp(a, b *Set, op uint8) *Set {
	out := &Set{}
	i, j := 0, 0
	pushBlock := func(bl *block) {
		out.blocks = append(out.blocks, &block{
			key: bl.key, c: bl.c.clone(), n: bl.n, rc: bl.rc,
		})
	}
	for i < len(a.blocks) || j < len(b.blocks) {
		switch {
		case i >= len(a.blocks):
			if op != 0 {
				pushBlock(b.blocks[j])
			}
			j++
		case j >= len(b.blocks):
			if op != 0 {
				pushBlock(a.blocks[i])
			}
			i++
		default:
			ka, kb := a.blocks[i].key, b.blocks[j].key
			switch {
			case ka < kb:
				if op != 0 { // OR / XOR 保留单侧块
					pushBlock(a.blocks[i])
				}
				i++
			case kb < ka:
				if op != 0 {
					pushBlock(b.blocks[j])
				}
				j++
			default:
				c, n, rc := combineContainers(a.blocks[i].c, b.blocks[j].c, op)
				if n > 0 {
					out.blocks = append(out.blocks, &block{key: ka, c: c, n: n, rc: rc})
				}
				i++
				j++
			}
		}
	}
	return out
}

// And 返回交集。
func (s *Set) And(o *Set) *Set { return setOp(s, o, 0) }

// Or 返回并集。
func (s *Set) Or(o *Set) *Set { return setOp(s, o, 1) }

// Xor 返回对称差。
func (s *Set) Xor(o *Set) *Set { return setOp(s, o, 2) }
