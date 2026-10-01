// Package weightedvote 实现带权重投票的协同决策无冲突选择器。
//
// 多方分布式节点各自维护一份投票状态副本，基于持股权重提交
// 赞成/反对/弃权票；投票权重可随时间因果动态调整；
// 网络分区愈合后，通过 Merge 合并状态，自动汇总出确定性的最终定论。
//
// 合并语义为 CRDT（基于 Lamport 时钟 + 节点 ID 的 LWW-Register）：
// 满足交换律、结合律、幂等律，因此合并结果与合并顺序无关，天然确定性。
package weightedvote

import "fmt"

// Vote 表示一票的选择。
type Vote int

const (
	Abstain Vote = iota // 弃权
	Yes                 // 赞成
	No                  // 反对
)

func (v Vote) String() string {
	switch v {
	case Yes:
		return "Yes"
	case No:
		return "No"
	default:
		return "Abstain"
	}
}

// Timestamp 是因果时间戳：Lamport 逻辑时钟为主键，节点 ID 为决胜键，
// 构成全序，保证并发事件的合并结果唯一确定。
type Timestamp struct {
	Lamport uint64
	Node    string
}

// Compare 比较两个时间戳：先比 Lamport 时钟，再按节点 ID 字典序决胜。
func (t Timestamp) Compare(o Timestamp) int {
	if t.Lamport != o.Lamport {
		if t.Lamport < o.Lamport {
			return -1
		}
		return 1
	}
	if t.Node != o.Node {
		if t.Node < o.Node {
			return -1
		}
		return 1
	}
	return 0
}

func (t Timestamp) String() string {
	return fmt.Sprintf("%d@%s", t.Lamport, t.Node)
}

// VoteRecord 记录某投票人最新的一票及其因果时间戳。
type VoteRecord struct {
	Choice Vote
	Time   Timestamp
}

// WeightRecord 记录某投票人最新的权重及其因果时间戳。
type WeightRecord struct {
	Weight uint64
	Time   Timestamp
}
