package paxos

// Ballot 是 Paxos 提案编号，打包进一个 uint64：
// 高 44 位为单调递增的轮次 Num，低 20 位为节点 ID（用于打破平局）。
// 字典序比较即 Paxos 的偏序关系，可直接用 < / > 比较。
type Ballot uint64

const nodeIDBits = 20

// NewBallot 构造一个提案编号。
func NewBallot(num, nodeID uint64) Ballot {
	return Ballot(num<<nodeIDBits | nodeID&(1<<nodeIDBits-1))
}

// Num 返回提案轮次。
func (b Ballot) Num() uint64 { return uint64(b) >> nodeIDBits }

// NodeID 返回发起该提案的节点 ID。
func (b Ballot) NodeID() uint64 { return uint64(b) & (1<<nodeIDBits - 1) }
