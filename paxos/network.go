package paxos

import "sync/atomic"

// Network 是进程内的确定性消息传输层，用于测试与演示。
// 链路状态用原子布尔矩阵表示，可动态构造/愈合网络分区；
// 所有消息投递都是同步函数调用，不引入额外线程。
// Prepares/Accepts/Commits 统计消息次数，用于验证租约绕过 Phase 1。
type Network struct {
	ids       []int
	idx       map[int]int
	nodes     map[int]*Node
	connected [][]atomic.Bool

	Prepares atomic.Int64
	Accepts  atomic.Int64
	Commits  atomic.Int64
}

// NewNetwork 创建 size 个节点的全连接网络（节点 ID 为 0..size-1）。
func NewNetwork(size int) *Network {
	nw := &Network{
		idx:       make(map[int]int, size),
		nodes:     make(map[int]*Node, size),
		connected: make([][]atomic.Bool, size),
	}
	for i := 0; i < size; i++ {
		nw.ids = append(nw.ids, i)
		nw.idx[i] = i
		nw.connected[i] = make([]atomic.Bool, size)
		for j := 0; j < size; j++ {
			nw.connected[i][j].Store(true)
		}
	}
	return nw
}

// Size 返回集群节点数。
func (nw *Network) Size() int { return len(nw.ids) }

// IDs 返回全部节点 ID。
func (nw *Network) IDs() []int { return append([]int(nil), nw.ids...) }

// Register 把节点挂到网络上（构造 Node 后调用一次）。
func (nw *Network) Register(n *Node) { nw.nodes[n.ID()] = n }

func (nw *Network) linked(from, to int) bool {
	return nw.connected[nw.idx[from]][nw.idx[to]].Load()
}

// Partition 切断 a 组与 b 组之间的双向链路（组内保持连通）。
func (nw *Network) Partition(a, b []int) {
	for _, x := range a {
		for _, y := range b {
			nw.connected[nw.idx[x]][nw.idx[y]].Store(false)
			nw.connected[nw.idx[y]][nw.idx[x]].Store(false)
		}
	}
}

// Heal 恢复全网连通。
func (nw *Network) Heal() {
	for i := range nw.connected {
		for j := range nw.connected[i] {
			nw.connected[i][j].Store(true)
		}
	}
}

// Prepare 投递 Phase 1a；链路断开时返回 ok=false。
func (nw *Network) Prepare(from, to int, req PrepareRequest) (PrepareResponse, bool) {
	nw.Prepares.Add(1)
	if !nw.linked(from, to) {
		return PrepareResponse{}, false
	}
	return nw.nodes[to].HandlePrepare(req), true
}

// Accept 投递 Phase 2a；链路断开时返回 ok=false。
func (nw *Network) Accept(from, to int, req AcceptRequest) (AcceptResponse, bool) {
	nw.Accepts.Add(1)
	if !nw.linked(from, to) {
		return AcceptResponse{}, false
	}
	return nw.nodes[to].HandleAccept(req), true
}

// Commit 投递提交通知；链路断开时静默丢弃。
func (nw *Network) Commit(from, to int, msg CommitMessage) {
	nw.Commits.Add(1)
	if !nw.linked(from, to) {
		return
	}
	nw.nodes[to].HandleCommit(msg)
}
