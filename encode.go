package sparse

import "encoding/binary"

// 物理编码布局（例外索引，非 map 包装）：
//
//	run = { bases []uint64, blocks []block }        // 目录 + 块序列
//	block = { base uint64, n uint16,
//	          posEnc []byte,   // uvarint 位置差分（首项相对 base 的差分恒为 0）
//	          valEnc []byte }  // zigzag-varint 绝对值
//
// 每块最多 blockSize 条例外，目录 bases 支持二分定位块。
const blockSize = 128

// bufFlush 是写缓冲（排序平行切片）触发合并进 run 的阈值。
const bufFlush = blockSize

type block struct {
	base   uint64
	n      uint16
	posEnc []byte
	valEnc []byte
}

type runT struct {
	bases  []uint64
	blocks []block
}

func zigzag(x int64) uint64   { return uint64(x<<1) ^ uint64(x>>63) }
func unzigzag(x uint64) int64 { return int64(x>>1) ^ -int64(x&1) }

func buildBlock(pos []uint64, val []int64) block {
	b := block{base: pos[0], n: uint16(len(pos))}
	prev := pos[0]
	b.posEnc = binary.AppendUvarint(b.posEnc, 0)
	for i := 1; i < len(pos); i++ {
		b.posEnc = binary.AppendUvarint(b.posEnc, pos[i]-prev)
		prev = pos[i]
	}
	for _, v := range val {
		b.valEnc = binary.AppendUvarint(b.valEnc, zigzag(v))
	}
	return b
}

// decode 把块内条目追加到 pos/val 并返回。
func (b *block) decode(pos []uint64, val []int64) ([]uint64, []int64) {
	p := b.base
	pos = append(pos, p)
	rest := b.posEnc
	_, k := binary.Uvarint(rest) // 首项差分恒为 0
	rest = rest[k:]
	for i := 1; i < int(b.n); i++ {
		d, k := binary.Uvarint(rest)
		rest = rest[k:]
		p += d
		pos = append(pos, p)
	}
	rest = b.valEnc
	for i := 0; i < int(b.n); i++ {
		u, k := binary.Uvarint(rest)
		rest = rest[k:]
		val = append(val, unzigzag(u))
	}
	return pos, val
}

func buildRun(pos []uint64, val []int64) *runT {
	if len(pos) == 0 {
		return nil
	}
	r := &runT{}
	for i := 0; i < len(pos); i += blockSize {
		j := min(i+blockSize, len(pos))
		r.bases = append(r.bases, pos[i])
		r.blocks = append(r.blocks, buildBlock(pos[i:j], val[i:j]))
	}
	return r
}

// get 在 run 内二分目录定位块并解码查找。
func (r *runT) get(p uint64) (int64, bool) {
	nb := len(r.blocks)
	if nb == 0 || p < r.bases[0] {
		return 0, false
	}
	i := searchU64(r.bases, p+1) - 1 // 最后一个 base <= p 的块
	if i < 0 {
		return 0, false
	}
	var ps [blockSize]uint64
	var vs [blockSize]int64
	pos, val := r.blocks[i].decode(ps[:0], vs[:0])
	k := searchU64(pos, p)
	if k < len(pos) && pos[k] == p {
		return val[k], true
	}
	return 0, false
}

// scanRange 只解码与 [l,r) 相交的块，追加落在范围内的条目。
func (r *runT) scanRange(l, rr uint64, pos []uint64, val []int64) ([]uint64, []int64) {
	nb := len(r.blocks)
	if nb == 0 {
		return pos, val
	}
	i := searchU64(r.bases, l+1) - 1
	if i < 0 {
		i = 0
	}
	var ps [blockSize]uint64
	var vs [blockSize]int64
	for ; i < nb; i++ {
		if r.bases[i] >= rr {
			break
		}
		blk := &r.blocks[i]
		bp, bv := blk.decode(ps[:0], vs[:0])
		for k := 0; k < int(blk.n); k++ {
			if bp[k] >= l && bp[k] < rr {
				pos = append(pos, bp[k])
				val = append(val, bv[k])
			}
		}
	}
	return pos, val
}

func (r *runT) scanAll(pos []uint64, val []int64) ([]uint64, []int64) {
	for i := range r.blocks {
		pos, val = r.blocks[i].decode(pos, val)
	}
	return pos, val
}

// searchU64 返回 a 中第一个 >= x 的下标（a 升序）。
func searchU64(a []uint64, x uint64) int {
	lo, hi := 0, len(a)
	for lo < hi {
		mid := (lo + hi) / 2
		if a[mid] < x {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}
