package netsec

import "fmt"

// MaxFragments 是发送对象允许的最大片段数。
const MaxFragments = 8

// Fragment 是发送方已认证的单个片段。Seq 为它在原始报文中的位置，
// Data 为负载。模型假设每个片段都带有不可伪造的认证标签，
// 因此攻击者无法修改 Seq 或 Data，只能整体删除、重复或重排。
type Fragment struct {
	Seq  int
	Data string
}

// Transition 以 (当前状态, 片段序号) 标识一条接收规则。
type Transition struct {
	State int
	Seq   int
}

// FSM 是接收方的有限状态机：Start 为初始状态，Accept 为“报文完整”
// 接受状态集合，Delta 为转移函数。未定义的转移表示接收方丢弃该片段
// （等价于攻击者将其删除，搜索时无需展开）。
type FSM struct {
	Start  int
	Accept map[int]bool
	Delta  map[Transition]int
}

// Assemble 按接收顺序拼接负载，得到接收方重组出的报文内容。
func Assemble(frags []Fragment) string {
	out := ""
	for _, f := range frags {
		out += f.Data
	}
	return out
}

// Validate 检查发送对象与状态机是否满足模型约束。
func Validate(frags []Fragment, fsm *FSM) error {
	if len(frags) == 0 {
		return fmt.Errorf("netsec: 发送对象为空")
	}
	if len(frags) > MaxFragments {
		return fmt.Errorf("netsec: 片段数 %d 超过上限 %d", len(frags), MaxFragments)
	}
	seen := make(map[int]bool, len(frags))
	for i, f := range frags {
		if f.Seq < 0 {
			return fmt.Errorf("netsec: 片段 %d 序号非法: %d", i, f.Seq)
		}
		if seen[f.Seq] {
			return fmt.Errorf("netsec: 片段序号 %d 重复", f.Seq)
		}
		seen[f.Seq] = true
	}
	if fsm == nil {
		return fmt.Errorf("netsec: 状态机为 nil")
	}
	if len(fsm.Accept) == 0 {
		return fmt.Errorf("netsec: 状态机缺少接受状态")
	}
	for t, next := range fsm.Delta {
		if !seen[t.Seq] {
			return fmt.Errorf("netsec: 转移 %+v 引用了未知片段序号", t)
		}
		if next < 0 || t.State < 0 {
			return fmt.Errorf("netsec: 转移 %+v 引用了非法状态", t)
		}
	}
	return nil
}

// NewLastFragmentReceiver 构造典型的分片接收机：接受任意已认证片段，
// 收到序号为 last 的“末片”即宣告报文完整。它认证了每个片段却未认证
// 总片数，是截尾攻击的漏洞模型。
func NewLastFragmentReceiver(last int) *FSM {
	delta := make(map[Transition]int, last+1)
	for seq := 0; seq <= last; seq++ {
		next := 0
		if seq == last {
			next = 1
		}
		delta[Transition{State: 0, Seq: seq}] = next
	}
	return &FSM{Start: 0, Accept: map[int]bool{1: true}, Delta: delta}
}

// NewOrderedReceiver 构造严格按序接收的状态机：只有依次收到
// 0,1,...,last 才进入接受状态，用于对照说明顺序认证可抵御该攻击者。
func NewOrderedReceiver(last int) *FSM {
	delta := make(map[Transition]int, last+1)
	for seq := 0; seq <= last; seq++ {
		delta[Transition{State: seq, Seq: seq}] = seq + 1
	}
	return &FSM{Start: 0, Accept: map[int]bool{last + 1: true}, Delta: delta}
}

// NewCountingReceiver 构造按计数完成的接收机：收满 total 个已认证片段
// 即宣告完整，不校验序号与次序，因而重复片段可冒充完整报文。
func NewCountingReceiver(total int, seqs []int) *FSM {
	delta := make(map[Transition]int, total*len(seqs))
	for state := 0; state < total; state++ {
		for _, seq := range seqs {
			delta[Transition{State: state, Seq: seq}] = state + 1
		}
	}
	return &FSM{Start: 0, Accept: map[int]bool{total: true}, Delta: delta}
}
