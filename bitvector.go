package wavelet

import "math/bits"

// superblockStride 表示每 512 个比特（8 个 uint64 字）记录一次前缀 popcount。
const superblockStride = 8

// bitVector 是一个支持 O(1) rank 的位向量。
//
// 真实内存布局（非 map 包装）：
//   - words: 比特按小端位序打包进 []uint64，第 i 个比特位于
//     words[i/64] 的第 i%64 位；
//   - super: 每 superblockStride 个字记录一次"之前共有多少个 1"，
//     rank 时先查 super，再对块内若干字做 popcount。
type bitVector struct {
	n     int      // 比特总数
	words []uint64 // 打包后的比特
	super []uint64 // 每 512 比特的前缀 1 计数
}

func newBitVector(n int) *bitVector {
	return &bitVector{
		n:     n,
		words: make([]uint64, (n+63)/64),
	}
}

func (bv *bitVector) set(i int) {
	bv.words[i/64] |= 1 << (uint(i) % 64)
}

// build 在所有比特设置完成后构建 rank 索引。
func (bv *bitVector) build() {
	numSuper := (len(bv.words) + superblockStride - 1) / superblockStride
	bv.super = make([]uint64, numSuper+1)
	var acc uint64
	for i, w := range bv.words {
		if i%superblockStride == 0 {
			bv.super[i/superblockStride] = acc
		}
		acc += uint64(bits.OnesCount64(w))
	}
	bv.super[numSuper] = acc
}

// rank1 返回区间 [0, i) 中 1 的个数。
func (bv *bitVector) rank1(i int) int {
	if i <= 0 {
		return 0
	}
	if i > bv.n {
		i = bv.n
	}
	full := i / 64
	rem := uint(i) % 64
	sb := full / superblockStride
	acc := bv.super[sb]
	for w := sb * superblockStride; w < full; w++ {
		acc += uint64(bits.OnesCount64(bv.words[w]))
	}
	if rem != 0 {
		acc += uint64(bits.OnesCount64(bv.words[full] & ((1 << rem) - 1)))
	}
	return int(acc)
}

// rank0 返回区间 [0, i) 中 0 的个数。
func (bv *bitVector) rank0(i int) int {
	return i - bv.rank1(i)
}

// bytes 返回该位向量占用的字节数（words + super 两个底层数组）。
func (bv *bitVector) bytes() int {
	return 8*len(bv.words) + 8*len(bv.super)
}
