package inventory

// Network 模拟副本间的通信与网络分区。
// 同一分区组内的副本可以互相合并（ gossip ），
// 跨组的消息在分区期间无法送达，重连（Heal）后恢复。
type Network struct {
	replicas []*Replica
	groups   map[string]int // 副本 ID -> 分区组号
}

// NewNetwork 创建网络，初始时所有副本连通（同组）。
func NewNetwork(replicas ...*Replica) *Network {
	n := &Network{groups: make(map[string]int)}
	for _, r := range replicas {
		n.replicas = append(n.replicas, r)
		n.groups[r.ID] = 0
	}
	return n
}

// Partition 将副本划分为若干互不相通的组。
// 每个参数是一组副本 ID；未提及的副本各自单独成组。
func (n *Network) Partition(groups ...[]string) {
	next := 0
	assigned := make(map[string]bool)
	for _, g := range groups {
		for _, id := range g {
			n.groups[id] = next
			assigned[id] = true
		}
		next++
	}
	for _, r := range n.replicas {
		if !assigned[r.ID] {
			n.groups[r.ID] = next
			next++
		}
	}
}

// Heal 恢复网络，所有副本重新连通。
func (n *Network) Heal() {
	for id := range n.groups {
		n.groups[id] = 0
	}
}

// Sync 在每个分区组内部做全互联合并：
// 组内任意两个副本双向交换转让流水。
// 分区期间跨组副本互不可见，重连后调用 Sync 即完成状态收敛。
func (n *Network) Sync() {
	for i := 0; i < len(n.replicas); i++ {
		for j := i + 1; j < len(n.replicas); j++ {
			a, b := n.replicas[i], n.replicas[j]
			if n.groups[a.ID] != n.groups[b.ID] {
				continue // 分区隔离：消息无法送达
			}
			a.MergeFrom(b)
			b.MergeFrom(a)
		}
	}
}

// TotalRights 返回所有副本当前持有额度之和。
func (n *Network) TotalRights() int64 {
	var sum int64
	for _, r := range n.replicas {
		sum += r.Rights()
	}
	return sum
}

// TotalSpent 返回所有副本已扣减数量之和。
func (n *Network) TotalSpent() int64 {
	var sum int64
	for _, r := range n.replicas {
		sum += r.Spent()
	}
	return sum
}

// InFlight 返回在途额度：已被转让方扣减、但尚未被收款方
// 应用的转让消息金额之和。分区期间这部分额度暂不可用于扣减。
func (n *Network) InFlight() int64 {
	// 已知消息的并集（按 ID 去重）。
	known := make(map[string]Transfer)
	applied := make(map[string]bool)
	for _, r := range n.replicas {
		for _, t := range r.Ledger() {
			known[t.ID] = t
		}
	}
	for _, r := range n.replicas {
		for id := range r.applied {
			applied[id] = true
		}
	}
	var sum int64
	for id, t := range known {
		if !applied[id] {
			sum += t.Amount
		}
	}
	return sum
}
