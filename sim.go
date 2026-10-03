package main

import "fmt"

// Sim 模拟一个可分区/愈合的集群网络，执行周期探活与对账。
type Sim struct {
	nodes map[string]*Node
	edges [][2]string

	Rounds       int // 已执行的探活轮次
	Probes       int // 探活次数（每次双向交换 4 字节摘要）
	Reconciles   int // 触发的区间对账次数
	DigestBytes  int // 摘要交换流量（字节）
	PayloadBytes int // 对账负载流量（估算，字节）
}

func NewSim() *Sim {
	return &Sim{nodes: make(map[string]*Node)}
}

func (s *Sim) AddNode(n *Node) {
	s.nodes[n.ID] = n
}

func (s *Sim) Node(id string) *Node { return s.nodes[id] }

// Partition 将集群划分为若干互不连通的子集群（子集群内部全互联）。
func (s *Sim) Partition(groups ...[]string) {
	s.edges = nil
	for _, g := range groups {
		for i := 0; i < len(g); i++ {
			for j := i + 1; j < len(g); j++ {
				s.edges = append(s.edges, [2]string{g[i], g[j]})
			}
		}
	}
}

// Heal 愈合分区，恢复全互联拓扑。
func (s *Sim) Heal() {
	ids := make([]string, 0, len(s.nodes))
	for id := range s.nodes {
		ids = append(ids, id)
	}
	s.Partition(ids)
}

// RunRound 执行一轮周期探活：每条边上的两个节点互相比对 4 字节
// Digest，失配则立即触发区间状态对账。
func (s *Sim) RunRound() {
	s.Rounds++
	for _, e := range s.edges {
		s.probe(s.nodes[e[0]], s.nodes[e[1]])
	}
}

// probe 一次探活：双向交换 4 字节 CRC 摘要。
func (s *Sim) probe(a, b *Node) {
	s.Probes++
	s.DigestBytes += 8 // 双向各 4 字节
	if a.Digest() == b.Digest() {
		return
	}
	s.reconcile(a, b)
}

// reconcile 区间状态对账：比对各环区间摘要，仅对失配区间
// 双向拉取成员记录并合并。
func (s *Sim) reconcile(a, b *Node) {
	s.Reconciles++
	ba, bb := a.BucketDigests(), b.BucketDigests()
	s.DigestBytes += 2 * NumBuckets * 4 // 双向交换区间摘要
	for i := 0; i < NumBuckets; i++ {
		if ba[i] == bb[i] {
			continue
		}
		for _, m := range a.MembersInBucket(i) {
			b.Upsert(m)
			s.PayloadBytes += len(m.ID) + 9
		}
		for _, m := range b.MembersInBucket(i) {
			a.Upsert(m)
			s.PayloadBytes += len(m.ID) + 9
		}
	}
}

// Converged 判断所有节点的全环摘要是否一致。
func (s *Sim) Converged() bool {
	var first uint32
	init := false
	for _, n := range s.nodes {
		if !init {
			first, init = n.Digest(), true
			continue
		}
		if n.Digest() != first {
			return false
		}
	}
	return true
}

// RunUntilConverged 逐轮探活直至收敛或超过 maxRounds，返回所用轮数；
// 未收敛返回 -1。
func (s *Sim) RunUntilConverged(maxRounds int) int {
	for i := 0; i < maxRounds; i++ {
		s.RunRound()
		if s.Converged() {
			return s.Rounds
		}
	}
	return -1
}

func (s *Sim) String() string {
	return fmt.Sprintf("rounds=%d probes=%d reconciles=%d digestBytes=%d payloadBytes=%d",
		s.Rounds, s.Probes, s.Reconciles, s.DigestBytes, s.PayloadBytes)
}
