package netsec

import (
	"fmt"
	"strings"
)

// DefaultMaxDepth 是最短攻击搜索的默认深度上限。
// 截尾/重放攻击的见证通常不超过发送对象长度，两倍余量足够。
const DefaultMaxDepth = 2*MaxFragments + 2

// Attack 是一次完整性违规的重放证书：Sequence 为攻击者实际投递的
// 片段序列，Sources[i] 指明 Sequence[i] 复制自发送对象的哪个片段，
// 作为认证未被伪造的依据——每个投递片段都能在发送方已认证集合中
// 找到来源，攻击仅由删除、重复、重排构成。
type Attack struct {
	Sequence []Fragment // 重放证书：被接收方接受为完整的片段序列
	Sources  []int      // Sources[i] = Sequence[i] 在发送对象中的下标
	Sent     string     // 发送对象重组出的原始内容
	Received string     // 接收方实际重组出的内容（与 Sent 不同）
}

// FindAttack 用广度优先搜索找出被 fsm 接受为完整、但重组内容不同于
// 发送对象的最短攻击序列。攻击者能力限定为删除、重复、重排 frags 中
// 已认证的片段。maxDepth <= 0 时使用 DefaultMaxDepth。
//
// 返回 (nil, nil) 表示在深度上限内不存在攻击；返回非 nil error 表示
// 输入违反模型约束。
func FindAttack(frags []Fragment, fsm *FSM, maxDepth int) (*Attack, error) {
	if err := Validate(frags, fsm); err != nil {
		return nil, err
	}
	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}
	sent := Assemble(frags)
	bySeq := make(map[int]int, len(frags))
	for i, f := range frags {
		bySeq[f.Seq] = i
	}

	type node struct {
		state   int
		content string
		path    []int // frags 下标序列
	}
	// 同一 (状态, 已重组内容) 的更短路径支配更长路径，据此剪枝。
	visited := map[string]bool{visitKey(fsm.Start, ""): true}
	queue := []node{{state: fsm.Start}}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if len(cur.path) >= maxDepth {
			continue
		}
		for i, f := range frags {
			next, ok := fsm.Delta[Transition{State: cur.state, Seq: f.Seq}]
			if !ok {
				continue // 接收方丢弃，等价于删除
			}
			content := cur.content + f.Data
			path := append(append([]int{}, cur.path...), i)
			if fsm.Accept[next] {
				if content != sent {
					return buildAttack(frags, path, sent, content), nil
				}
				continue // 接受且内容一致：正常投递，非攻击
			}
			key := visitKey(next, content)
			if visited[key] {
				continue
			}
			visited[key] = true
			queue = append(queue, node{state: next, content: content, path: path})
		}
	}
	return nil, nil
}

func visitKey(state int, content string) string {
	return fmt.Sprintf("%d\x00%s", state, content)
}

func buildAttack(frags []Fragment, path []int, sent, received string) *Attack {
	a := &Attack{
		Sequence: make([]Fragment, len(path)),
		Sources:  make([]int, len(path)),
		Sent:     sent,
		Received: received,
	}
	for i, idx := range path {
		a.Sequence[i] = frags[idx]
		a.Sources[i] = idx
	}
	return a
}

// Verify 独立校验重放证书，确认攻击成立且认证未被伪造：
//  1. 每个投递片段都与发送对象中某个已认证片段完全一致（来源合法）；
//  2. 序列驱动 fsm 从初态进入接受状态（被当作完整报文）；
//  3. 重组内容与发送对象不同（完整性确实被破坏）。
func Verify(frags []Fragment, fsm *FSM, a *Attack) error {
	if err := Validate(frags, fsm); err != nil {
		return err
	}
	if a == nil || len(a.Sequence) == 0 {
		return fmt.Errorf("netsec: 证书为空")
	}
	if len(a.Sources) != len(a.Sequence) {
		return fmt.Errorf("netsec: 来源标注与序列长度不符")
	}
	for i, f := range a.Sequence {
		src := a.Sources[i]
		if src < 0 || src >= len(frags) || frags[src] != f {
			return fmt.Errorf("netsec: 第 %d 个投递片段无合法来源，疑似伪造认证", i)
		}
	}
	state := fsm.Start
	for i, f := range a.Sequence {
		next, ok := fsm.Delta[Transition{State: state, Seq: f.Seq}]
		if !ok {
			return fmt.Errorf("netsec: 第 %d 个片段被接收方丢弃，序列未被接受", i)
		}
		state = next
	}
	if !fsm.Accept[state] {
		return fmt.Errorf("netsec: 序列未使接收机进入接受状态")
	}
	if got := Assemble(a.Sequence); got != a.Received {
		return fmt.Errorf("netsec: 证书记载内容 %q 与重组结果 %q 不符", a.Received, got)
	}
	if a.Received == Assemble(frags) {
		return fmt.Errorf("netsec: 重组内容与发送对象一致，完整性未被破坏")
	}
	return nil
}

// Certificate 输出可读的证书文本，包含重放序列、来源映射与结论。
func (a *Attack) Certificate() string {
	var b strings.Builder
	fmt.Fprintf(&b, "重放证书（长度 %d）\n", len(a.Sequence))
	for i, f := range a.Sequence {
		fmt.Fprintf(&b, "  投递[%d]: seq=%d data=%q  <- 发送片段[%d]（已认证，未伪造）\n",
			i, f.Seq, f.Data, a.Sources[i])
	}
	fmt.Fprintf(&b, "发送内容: %q\n接收内容: %q\n", a.Sent, a.Received)
	b.WriteString("依据: 全部投递片段均来自发送方已认证集合，攻击仅由删除/重复/重排构成，")
	b.WriteString("说明协议认证了每个片段却未认证总片数与次序。")
	return b.String()
}
