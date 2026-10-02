package main

import "fmt"

// LogEntry 复制日志中的一条事件记录。
// LSN 为本节点日志内的物理位置（单调递增）；
// Origin/Seq 标识事件的来源节点及其在来源节点上的逻辑时间，用于 GC 判定。
type LogEntry struct {
	LSN    uint64
	Origin string
	Seq    uint64
	Data   string
}

// Message 节点间消息：携带一条事件 + 发送方完整矩阵时钟快照。
type Message struct {
	From   string
	Entry  LogEntry
	Matrix map[string]map[string]uint64
}

// Node 集群节点：持有矩阵时钟与本地复制日志。
type Node struct {
	ID      string
	clock   *MatrixClock
	log     []LogEntry
	nextLSN uint64
}

func newNode(id string, nodes []string) *Node {
	return &Node{ID: id, clock: NewMatrixClock(id, nodes)}
}

func (n *Node) append(e LogEntry) {
	n.nextLSN++
	e.LSN = n.nextLSN
	n.log = append(n.log, e)
}

// receive 处理收到的消息：合并矩阵时钟，把事件追加到本地日志。
func (n *Node) receive(m Message) {
	n.clock.Merge(m.From, m.Matrix)
	n.append(m.Entry)
}

// GCCut 依据矩阵时钟计算安全水位：每个来源节点已被全网确认的最大 Seq。
func (n *Node) GCCut() map[string]uint64 {
	return n.clock.MinVector()
}

// Acknowledged 判断某条日志是否已被集群内每个节点确认知悉。
func (n *Node) Acknowledged(e LogEntry) bool {
	return e.Seq <= n.GCCut()[e.Origin]
}

// TruncatePrefix 物理截断日志前缀：从头部开始，凡被全网确认的事件
// 均可安全回收。返回截断水位 LSN 与被回收的条目。
func (n *Node) TruncatePrefix() (horizon uint64, removed []LogEntry) {
	cut := n.GCCut()
	idx := 0
	for idx < len(n.log) && n.log[idx].Seq <= cut[n.log[idx].Origin] {
		idx++
	}
	removed = n.log[:idx]
	if idx > 0 {
		horizon = n.log[idx-1].LSN
	}
	n.log = append([]LogEntry(nil), n.log[idx:]...)
	return horizon, removed
}

// Cluster 同步、确定性的 3 节点消息网络（便于演示与测试复现）。
type Cluster struct {
	order []string
	nodes map[string]*Node
}

func NewCluster(ids ...string) *Cluster {
	c := &Cluster{order: append([]string(nil), ids...), nodes: map[string]*Node{}}
	for _, id := range ids {
		c.nodes[id] = newNode(id, ids)
	}
	return c
}

func (c *Cluster) Node(id string) *Node { return c.nodes[id] }

// Send 从 from 向 to 发送一条事件消息（同步投递）。
func (c *Cluster) Send(from, to, data string) {
	src := c.nodes[from]
	seq := src.clock.Tick()
	entry := LogEntry{Origin: from, Seq: seq, Data: data}
	src.append(entry)
	c.nodes[to].receive(Message{From: from, Entry: entry, Matrix: src.clock.Snapshot()})
}

// BroadcastRound 一轮全互联广播：每个节点向其他所有节点各发一条消息。
func (c *Cluster) BroadcastRound(tag string) {
	for _, from := range c.order {
		for _, to := range c.order {
			if from != to {
				c.Send(from, to, fmt.Sprintf("%s:%s->%s", tag, from, to))
			}
		}
	}
}
