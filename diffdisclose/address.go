package diffdisclose

// addr 是稀疏树内部节点的地址：键空间中从最高位开始的公共前缀位串。
// bits 保存高位在前的前缀，长度为 MaxHeight-height；根地址长度为 0。
// 该表示与具体树高解耦，48 位以内的前缀最多 6 字节即可容纳。
type addr struct {
	bits []byte
}

// rootAddr 返回空前缀的根地址。
func rootAddr() addr {
	return addr{bits: []byte{}}
}

// len 返回前缀位数。
func (a addr) len() int {
	return len(a.bits)
}

// child 返回沿 bit（0 为左、1 为右）下降一层后的地址。
// 地址是 MSB 优先的前缀位串：新位作为当前最低有效位追加，
// 因此前缀 [b0,b1,...,bk] 还原出的高位键即为其按序二进制值。
func (a addr) child(bit byte) addr {
	next := make([]byte, len(a.bits)+1)
	copy(next, a.bits)
	next[len(a.bits)] = bit & 1
	return addr{bits: next}
}

// left 与 right 分别返回左、右孩子地址。
func (a addr) left() addr  { return a.child(0) }
func (a addr) right() addr { return a.child(1) }

// keyPrefix 把地址解释为定长键前缀：返回前缀对应的起始键与覆盖宽度。
// 高度为 height 的子树覆盖 1<<height 个连续键。
func (a addr) keyPrefix(height int) (start, span uint64) {
	span = uint64(1) << height
	var key uint64
	// 位串按根到叶顺序存储，位置 i 是键的第 (treeHeight-1-i) 个高位，
	// treeHeight = 前缀位数 + 子树高度。叶子时前缀长度等于树高，
	// 直接还原键；内部节点得到子树起始键，跨度由 height 决定。
	treeHeight := height + len(a.bits)
	var prefixValue uint64
	for i, bit := range a.bits {
		if bit == 1 {
			prefixValue |= uint64(1) << (treeHeight - 1 - i)
		}
	}
	key = prefixValue
	return key, span
}

// covers 判断该地址（给定高度）的子树是否完整覆盖 [lo, hi)。
func (a addr) covers(height int, lo, hi uint64) bool {
	start, span := a.keyPrefix(height)
	return lo >= start && hi <= start+span
}

// contains 判断键是否落在该地址子树内。
func (a addr) contains(height int, key uint64) bool {
	start, span := a.keyPrefix(height)
	return key >= start && key < start+span
}

// bytes 返回前缀的紧凑字节表示与位数，用于摘要绑定。
// 末尾字节未使用的低位补 0；解码侧同样只信任 prefixLen。
func (a addr) bytes() ([]byte, int) {
	n := len(a.bits)
	out := make([]byte, (n+7)/8)
	for i, bit := range a.bits {
		if bit == 1 {
			out[i/8] |= 1 << (7 - uint(i%8))
		}
	}
	return out, n
}

// parseAddr 按位数从紧凑字节表示重建地址。
func parseAddr(packed []byte, bitLen int) (addr, error) {
	if bitLen < 0 || bitLen > MaxHeight || len(packed) != (bitLen+7)/8 {
		return addr{}, errBadAddress
	}
	bits := make([]byte, bitLen)
	for i := 0; i < bitLen; i++ {
		bits[i] = (packed[i/8] >> (7 - uint(i%8))) & 1
	}
	return addr{bits: bits}, nil
}
