package netsec

import "fmt"

// NodeKind 标识节点运算类型。
type NodeKind int

const (
	KindInput  NodeKind = iota // 输入位，按出现顺序占用位编号 0..n-1
	KindAnd                    // 二输入与
	KindXor                    // 二输入异或
	KindNot                    // 单输入非
	KindOutput                 // 安全输出位（拒绝=0，放行=1），全电路恰有一个
)

// 电路规模约束。
const (
	MaxNodes  = 128 // 节点数上限
	MaxInputs = 12  // 输入位数上限（输入空间 2^12 = 4096）
)

// Node 是布尔 DAG 中的一个节点。Args 为操作数节点下标，
// 必须严格小于本节点下标，即节点按拓扑序给出。
type Node struct {
	Kind NodeKind
	Args []int
}

// Circuit 是校验通过的布尔 DAG，内部缓存位并行仿真所需的常量。
type Circuit struct {
	nodes     []Node
	numInputs int
	outputIdx int
	words     int      // 每张真值表占用的 uint64 字数
	mask      []uint64 // 有效输入位掩码（低 2^numInputs 位置 1）
}

// NewCircuit 校验节点列表并构造电路。
func NewCircuit(nodes []Node) (*Circuit, error) {
	if len(nodes) == 0 {
		return nil, fmt.Errorf("netsec: 电路为空")
	}
	if len(nodes) > MaxNodes {
		return nil, fmt.Errorf("netsec: 节点数 %d 超过上限 %d", len(nodes), MaxNodes)
	}
	c := &Circuit{nodes: make([]Node, len(nodes)), outputIdx: -1}
	for i, nd := range nodes {
		c.nodes[i] = Node{Kind: nd.Kind, Args: append([]int(nil), nd.Args...)}
		want := -1
		switch nd.Kind {
		case KindInput:
			want = 0
			c.numInputs++
		case KindAnd, KindXor:
			want = 2
		case KindNot, KindOutput:
			want = 1
		default:
			return nil, fmt.Errorf("netsec: 节点 %d 类型非法: %d", i, int(nd.Kind))
		}
		if len(nd.Args) != want {
			return nil, fmt.Errorf("netsec: 节点 %d 需要 %d 个操作数，实际 %d", i, want, len(nd.Args))
		}
		if nd.Kind == KindOutput {
			if c.outputIdx >= 0 {
				return nil, fmt.Errorf("netsec: 输出节点不唯一（节点 %d）", i)
			}
			c.outputIdx = i
		}
		for _, a := range nd.Args {
			if a < 0 || a >= i {
				return nil, fmt.Errorf("netsec: 节点 %d 的操作数 %d 不是拓扑序前驱", i, a)
			}
		}
	}
	if c.outputIdx < 0 {
		return nil, fmt.Errorf("netsec: 缺少输出节点")
	}
	if c.numInputs > MaxInputs {
		return nil, fmt.Errorf("netsec: 输入位数 %d 超过上限 %d", c.numInputs, MaxInputs)
	}
	space := 1 << c.numInputs
	c.words = (space + 63) / 64
	c.mask = make([]uint64, c.words)
	for w := range c.mask {
		c.mask[w] = ^uint64(0)
	}
	if rem := space % 64; rem != 0 {
		c.mask[c.words-1] = (uint64(1) << rem) - 1
	}
	return c, nil
}

// NumInputs 返回输入位数。
func (c *Circuit) NumInputs() int { return c.numInputs }

// NodeCount 返回节点数。
func (c *Circuit) NodeCount() int { return len(c.nodes) }

// InputSpace 返回输入空间大小 2^NumInputs（不超过 4096）。
func (c *Circuit) InputSpace() int { return 1 << c.numInputs }

// Output 从逐节点状态中取安全输出位。
func (c *Circuit) Output(states []bool) bool { return states[c.outputIdx] }

// Eval 标量仿真：返回给定输入下每个节点的状态。
// 输入的第 i 位对应第 i 个输入节点（按出现顺序）。
func (c *Circuit) Eval(input uint32) []bool { return c.eval(input, nil) }

// EvalWithFault 标量仿真：节点 f.Node 的值被永久替换为 f.StuckAt。
func (c *Circuit) EvalWithFault(input uint32, f Fault) []bool { return c.eval(input, &f) }

func (c *Circuit) eval(input uint32, fault *Fault) []bool {
	states := make([]bool, len(c.nodes))
	inBit := 0
	for i, nd := range c.nodes {
		switch nd.Kind {
		case KindInput:
			states[i] = (input>>inBit)&1 == 1
			inBit++
		case KindAnd:
			states[i] = states[nd.Args[0]] && states[nd.Args[1]]
		case KindXor:
			states[i] = states[nd.Args[0]] != states[nd.Args[1]]
		case KindNot:
			states[i] = !states[nd.Args[0]]
		case KindOutput:
			states[i] = states[nd.Args[0]]
		}
		if fault != nil && fault.Node == i {
			states[i] = fault.StuckAt
		}
	}
	return states
}

// simulate 位并行仿真：返回每个节点在整个输入空间上的真值表，
// 表 t 的第 j 位是输入 j 下该节点的值。fault 非空时对应节点被卡滞。
func (c *Circuit) simulate(fault *Fault) [][]uint64 {
	vals := make([][]uint64, len(c.nodes))
	for i := range vals {
		vals[i] = make([]uint64, c.words)
	}
	inBit := 0
	for i, nd := range c.nodes {
		d := vals[i]
		switch nd.Kind {
		case KindInput:
			inputPattern(d, inBit, c.mask)
			inBit++
		case KindAnd:
			a, b := vals[nd.Args[0]], vals[nd.Args[1]]
			for w := 0; w < c.words; w++ {
				d[w] = a[w] & b[w]
			}
		case KindXor:
			a, b := vals[nd.Args[0]], vals[nd.Args[1]]
			for w := 0; w < c.words; w++ {
				d[w] = a[w] ^ b[w]
			}
		case KindNot:
			a := vals[nd.Args[0]]
			for w := 0; w < c.words; w++ {
				d[w] = ^a[w] & c.mask[w]
			}
		case KindOutput:
			copy(d, vals[nd.Args[0]])
		}
		if fault != nil && fault.Node == i {
			if fault.StuckAt {
				copy(d, c.mask)
			} else {
				for w := range d {
					d[w] = 0
				}
			}
		}
	}
	return vals
}

// inputPattern 生成第 bit 个输入位的真值表：以 2^(bit+1) 为周期的 0/1 块，
// 并按有效位掩码截断，避免高位垃圾经后续门传播。
func inputPattern(d []uint64, bit int, mask []uint64) {
	if bit >= 6 {
		// 周期不小于 64，整个字同为 0 或同为 1。
		for w := range d {
			if (w>>(bit-6))&1 == 1 {
				d[w] = mask[w]
			}
		}
		return
	}
	var word uint64
	for j := uint(0); j < 64; j++ {
		if (j>>bit)&1 == 1 {
			word |= 1 << j
		}
	}
	for w := range d {
		d[w] = word & mask[w]
	}
}

// bitOf 取真值表 t 的第 j 位。
func bitOf(t []uint64, j uint32) bool { return (t[j>>6]>>(j&63))&1 == 1 }

// lowestSet 返回真值表中最低置位的下标。
func lowestSet(t []uint64) (uint32, bool) {
	for w, x := range t {
		if x != 0 {
			return uint32(w*64 + trailingZeros64(x)), true
		}
	}
	return 0, false
}

func trailingZeros64(x uint64) int {
	n := 0
	for x&1 == 0 {
		x >>= 1
		n++
	}
	return n
}

// validateTests 校验测试向量均落在输入空间内。
func (c *Circuit) validateTests(tests []uint32) error {
	space := uint32(1) << c.numInputs
	for i, t := range tests {
		if t >= space {
			return fmt.Errorf("netsec: 测试向量 #%d = %d 超出输入空间 [0,%d)", i, t, space)
		}
	}
	return nil
}
