package netsec

// bitset 是按输入组合展开的位并行真值表：
// 第 x 位（x 为输入向量整数值）表示该输入下节点的取值。
// 仅低 space 位有效，高位恒为 0。
type bitset []uint64

func wordsOf(space int) int { return (space + 63) / 64 }

// validMask 返回低 space 位为 1 的掩码。
func validMask(space int) bitset {
	m := make(bitset, wordsOf(space))
	for i := range m {
		m[i] = ^uint64(0)
	}
	if r := space % 64; r != 0 {
		m[len(m)-1] = uint64(1)<<uint(r) - 1
	}
	return m
}

// inputPatterns 预计算每个输入位的真值表：
// pats[p] 的第 x 位 = (x >> p) & 1，超出 space 的高位恒为 0。
func inputPatterns(numInputs, space int) []bitset {
	words := wordsOf(space)
	valid := validMask(space)
	pats := make([]bitset, numInputs)
	for p := 0; p < numInputs; p++ {
		pat := make(bitset, words)
		if p < 6 {
			var w uint64
			for j := 0; j < 64; j++ {
				if (j>>uint(p))&1 == 1 {
					w |= 1 << uint(j)
				}
			}
			for i := range pat {
				pat[i] = w
			}
		} else {
			for i := range pat {
				if (i>>uint(p-6))&1 == 1 {
					pat[i] = ^uint64(0)
				}
			}
		}
		for i := range pat {
			pat[i] &= valid[i]
		}
		pats[p] = pat
	}
	return pats
}

func newBitsets(n, words int) []bitset {
	bs := make([]bitset, n)
	for i := range bs {
		bs[i] = make(bitset, words)
	}
	return bs
}

func cloneBitsets(src []bitset) []bitset {
	dst := make([]bitset, len(src))
	for i, b := range src {
		c := make(bitset, len(b))
		copy(c, b)
		dst[i] = c
	}
	return dst
}

func setConst(dst, valid bitset, v int) {
	if v == 0 {
		for i := range dst {
			dst[i] = 0
		}
		return
	}
	copy(dst, valid)
}

// simulate 从节点下标 from 起按拓扑序重算真值表 vals。
// from 之前的节点必须已就绪。
func simulate(c *Circuit, pats []bitset, valid bitset, vals []bitset, from int) {
	for i := from; i < len(c.Nodes); i++ {
		nd := &c.Nodes[i]
		dst := vals[i]
		switch nd.Op {
		case GateInput:
			copy(dst, pats[nd.Pin])
		case GateAnd:
			a, b := vals[nd.A], vals[nd.B]
			for w := range dst {
				dst[w] = a[w] & b[w]
			}
		case GateXor:
			a, b := vals[nd.A], vals[nd.B]
			for w := range dst {
				dst[w] = a[w] ^ b[w]
			}
		case GateNot:
			a := vals[nd.A]
			for w := range dst {
				dst[w] = ^a[w] & valid[w]
			}
		case GateOutput:
			copy(dst, vals[nd.A])
		}
	}
}

// lowestBit 返回最低置位下标；无置位时 ok 为 false。
func lowestBit(b bitset) (int, bool) {
	for w, x := range b {
		if x != 0 {
			tz := 0
			for x&1 == 0 {
				x >>= 1
				tz++
			}
			return w*64 + tz, true
		}
	}
	return 0, false
}

// anyAnd 报告 a & b 是否非零。
func anyAnd(a, b bitset) bool {
	for w := range a {
		if a[w]&b[w] != 0 {
			return true
		}
	}
	return false
}

// diffInto 计算 dst = a ^ b。
func diffInto(dst, a, b bitset) {
	for w := range dst {
		dst[w] = a[w] ^ b[w]
	}
}

// orInto 计算 dst |= src。
func orInto(dst, src bitset) {
	for w := range dst {
		dst[w] |= src[w]
	}
}

// andNotInto 计算 dst = a &^ b。
func andNotInto(dst, a, b bitset) {
	for w := range dst {
		dst[w] = a[w] &^ b[w]
	}
}

func (b bitset) bit(x int) int {
	return int((b[x/64] >> uint(x%64)) & 1)
}

// evalScalar 是独立的标量回放求值器：对单个输入向量逐节点求值，
// 可选注入一个固定故障。返回每个节点的 0/1 取值。
// 它与位并行仿真不共享任何代码路径，用于交叉验证与状态导出。
func evalScalar(c *Circuit, input uint32, fault *Fault) []byte {
	vals := make([]byte, len(c.Nodes))
	for i, nd := range c.Nodes {
		var v byte
		switch nd.Op {
		case GateInput:
			v = byte((input >> uint(nd.Pin)) & 1)
		case GateAnd:
			v = vals[nd.A] & vals[nd.B]
		case GateXor:
			v = vals[nd.A] ^ vals[nd.B]
		case GateNot:
			v = vals[nd.A] ^ 1
		case GateOutput:
			v = vals[nd.A]
		}
		if fault != nil && fault.Node == i {
			v = byte(fault.Stuck)
		}
		vals[i] = v
	}
	return vals
}
