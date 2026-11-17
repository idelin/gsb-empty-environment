package netsec

import "fmt"

// Gate 是节点运算类型。电路为拓扑有序的布尔 DAG：
// 每个节点的操作数下标必须小于自身下标。
type Gate uint8

const (
	GateInput  Gate = iota // 主输入，Pin 为输入位编号
	GateAnd                // 二输入与：A & B
	GateXor                // 二输入异或：A ^ B
	GateNot                // 单输入非：^A
	GateOutput             // 主输出，观测 A 的信号
)

func (g Gate) String() string {
	switch g {
	case GateInput:
		return "INPUT"
	case GateAnd:
		return "AND"
	case GateXor:
		return "XOR"
	case GateNot:
		return "NOT"
	case GateOutput:
		return "OUTPUT"
	}
	return fmt.Sprintf("Gate(%d)", uint8(g))
}

const (
	// MaxNodes 是电路节点数上限。
	MaxNodes = 128
	// MaxInputs 是主输入位数上限。
	MaxInputs = 12
	// MaxSpace 是输入组合空间上限（2^MaxInputs）。
	MaxSpace = 1 << MaxInputs
)

// Node 描述电路中的一个节点。未使用的字段必须为 -1（A、B）或 0（Pin）。
type Node struct {
	Op  Gate
	A   int // 操作数一下标，无则为 -1
	B   int // 操作数二下标（仅 AND/XOR），无则为 -1
	Pin int // 仅 GateInput：输入位编号
}

// Circuit 是有界布尔 DAG。Secure 为安全输出位对应的 GateOutput 节点下标，
// 其值为 0 表示拒绝、1 表示放行。
type Circuit struct {
	Nodes     []Node // 拓扑有序：操作数下标 < 节点下标
	NumInputs int    // 主输入位数，0..MaxInputs
	Secure    int    // 安全输出位节点下标（必须为 GateOutput）
}

// Validate 校验电路结构合法性。
func (c *Circuit) Validate() error {
	if c == nil {
		return fmt.Errorf("netsec: nil circuit")
	}
	if n := len(c.Nodes); n == 0 || n > MaxNodes {
		return fmt.Errorf("netsec: node count %d out of range [1,%d]", n, MaxNodes)
	}
	if c.NumInputs < 0 || c.NumInputs > MaxInputs {
		return fmt.Errorf("netsec: input count %d out of range [0,%d]", c.NumInputs, MaxInputs)
	}
	for i, nd := range c.Nodes {
		ref := func(j int) error {
			if j < 0 || j >= i {
				return fmt.Errorf("netsec: node %d (%s): operand %d not in [0,%d)", i, nd.Op, j, i)
			}
			return nil
		}
		switch nd.Op {
		case GateInput:
			if nd.Pin < 0 || nd.Pin >= c.NumInputs {
				return fmt.Errorf("netsec: node %d (INPUT): pin %d out of range [0,%d)", i, nd.Pin, c.NumInputs)
			}
		case GateAnd, GateXor:
			if err := ref(nd.A); err != nil {
				return err
			}
			if err := ref(nd.B); err != nil {
				return err
			}
		case GateNot, GateOutput:
			if err := ref(nd.A); err != nil {
				return err
			}
		default:
			return fmt.Errorf("netsec: node %d: unknown gate %d", i, uint8(nd.Op))
		}
	}
	if c.Secure < 0 || c.Secure >= len(c.Nodes) {
		return fmt.Errorf("netsec: secure node index %d out of range", c.Secure)
	}
	if c.Nodes[c.Secure].Op != GateOutput {
		return fmt.Errorf("netsec: secure node %d is %s, want OUTPUT", c.Secure, c.Nodes[c.Secure].Op)
	}
	return nil
}

// space 返回输入组合数 2^NumInputs。
func (c *Circuit) space() int { return 1 << uint(c.NumInputs) }

// outputNodes 返回所有主输出节点下标（升序）。
func (c *Circuit) outputNodes() []int {
	var outs []int
	for i, nd := range c.Nodes {
		if nd.Op == GateOutput {
			outs = append(outs, i)
		}
	}
	return outs
}

// faultableNodes 返回可被攻击者注入固定故障的内部节点下标（升序）。
// 内部节点指 AND/XOR/NOT 门；主输入与主输出不可注入。
func (c *Circuit) faultableNodes() []int {
	var ns []int
	for i, nd := range c.Nodes {
		switch nd.Op {
		case GateAnd, GateXor, GateNot:
			ns = append(ns, i)
		}
	}
	return ns
}

// Fault 表示一个固定故障：节点 Node 永久卡在 Stuck（0 或 1）。
type Fault struct {
	Node  int
	Stuck int
}
