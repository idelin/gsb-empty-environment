package merge

import (
	"hash/fnv"
	"time"
)

// sendMergeHello 向异集群节点发送本侧完整视图，开启合并握手第一阶段。
// 幂等：重复发送不会产生副作用。
func (n *Node) sendMergeHello(to uint64) {
	n.mu.Lock()
	if n.view.Members == nil {
		n.mu.Unlock()
		return
	}
	env := Envelope{
		Type:      MsgMergeHello,
		From:      n.ID,
		To:        to,
		ClusterID: n.view.ClusterID,
		Epoch:     n.view.Epoch,
		Members:   memberSlice(n.view.Members),
	}
	n.pending[to] = pendingMerge{contact: to, sentAt: time.Now()}
	n.mu.Unlock()
	env.Clock = n.clock.Tick()
	n.net.Send(env)
}

// handleMergeHello 处理对端视图摘要：
// 双方用相同的确定性规则比较 (epoch, size, clusterID)，
// 键大的一方成为发起方，负责融合视图并广播 Commit；
// 键小的一方仅登记 pending，等待 Commit（带超时重试，绝不阻塞）。
func (n *Node) handleMergeHello(env Envelope) {
	n.mu.Lock()
	if env.ClusterID == n.view.ClusterID {
		n.mu.Unlock()
		return // 已同集群，无需合并
	}
	peerView := View{ClusterID: env.ClusterID, Epoch: env.Epoch, Members: memberMap(env.Members)}
	myKey := keyOf(n.view)
	peerKey := keyOf(peerView)

	if !myKey.greater(peerKey) {
		// 对端是发起方：登记等待其 Commit。
		n.pending[env.From] = pendingMerge{contact: env.From, sentAt: time.Now()}
		n.mu.Unlock()
		return
	}

	// 快速路径：对端视图是本侧子集且纪元不更新——对端只是落后，
	// 直接回送当前视图让其追赶，不再推进纪元（保证幂等）。
	if subsetOf(peerView.Members, n.view.Members) && n.view.Epoch >= peerView.Epoch {
		commit := Envelope{
			Type:      MsgMergeCommit,
			From:      n.ID,
			To:        env.From,
			ClusterID: n.view.ClusterID,
			Epoch:     n.view.Epoch,
			Members:   memberSlice(n.view.Members),
		}
		n.mu.Unlock()
		commit.Clock = n.clock.Tick()
		n.net.Send(commit)
		return
	}

	// 完整合并：成员取并集，纪元取 max+1，集群标识由父标识与新纪元
	// 确定性派生（保证区别于两个父集群，落后节点因此总能被识别并追赶），
	// 时钟校正为 max+1（由 Witness 完成）。
	merged := mergeViews(n.view, peerView)
	n.view = merged
	n.pending = make(map[uint64]pendingMerge)
	commit := Envelope{
		Type:      MsgMergeCommit,
		From:      n.ID,
		ClusterID: merged.ClusterID,
		Epoch:     merged.Epoch,
		Members:   memberSlice(merged.Members),
	}
	targets := make([]uint64, 0, len(merged.Members))
	for id := range merged.Members {
		if id != n.ID {
			targets = append(targets, id)
		}
	}
	n.mergeRounds.Add(1)
	n.mu.Unlock()

	commit.Clock = n.clock.Tick()
	n.broadcast(commit, targets)
}

// handleMergeCommit 应用发起方广播的融合视图，并向本侧成员泛洪转发。
// 仅当 Commit 纪元更新时才应用并转发，因此每个节点至多转发一次，
// 泛洪必然终止；重复投递是幂等无操作。
func (n *Node) handleMergeCommit(env Envelope) {
	n.mu.Lock()
	if env.Epoch < n.view.Epoch ||
		(env.Epoch == n.view.Epoch && env.ClusterID == n.view.ClusterID) {
		n.mu.Unlock()
		return // 已应用过或已过期：幂等忽略
	}
	n.view = View{
		ClusterID: env.ClusterID,
		Epoch:     env.Epoch,
		Members:   memberMap(env.Members),
	}
	n.pending = make(map[uint64]pendingMerge)
	fwd := Envelope{
		Type:      MsgMergeCommit,
		From:      n.ID,
		ClusterID: env.ClusterID,
		Epoch:     env.Epoch,
		Members:   env.Members,
	}
	targets := make([]uint64, 0, len(env.Members))
	for _, m := range env.Members {
		if m.ID != n.ID && m.ID != env.From {
			targets = append(targets, m.ID)
		}
	}
	n.mu.Unlock()

	fwd.Clock = n.clock.Tick()
	n.broadcast(fwd, targets)
}

// retryPendingMerges 对超时未收到 Commit 的协商重发 Hello，
// 保证消息丢失场景下合并最终完成（无死锁/活锁）。
func (n *Node) retryPendingMerges() {
	n.mu.Lock()
	var retries []uint64
	now := time.Now()
	for key, pm := range n.pending {
		if now.Sub(pm.sentAt) >= n.retryAfter {
			retries = append(retries, pm.contact)
			n.pending[key] = pendingMerge{contact: pm.contact, sentAt: now}
		}
	}
	n.mu.Unlock()
	for _, contact := range retries {
		n.sendMergeHello(contact)
	}
}

// mergeViews 融合两个视图：成员并集（冲突取 incarnation 较大者），
// 纪元 max+1，集群标识确定性派生（双方独立计算结果一致）。
func mergeViews(a, b View) View {
	merged := View{
		Epoch:   a.Epoch,
		Members: make(map[uint64]Member, len(a.Members)+len(b.Members)),
	}
	if b.Epoch > merged.Epoch {
		merged.Epoch = b.Epoch
	}
	merged.Epoch++
	for _, src := range []map[uint64]Member{a.Members, b.Members} {
		for id, m := range src {
			if cur, ok := merged.Members[id]; !ok || m.Incarnation > cur.Incarnation {
				merged.Members[id] = m
			}
		}
	}
	merged.ClusterID = deriveClusterID(a.ClusterID, b.ClusterID, merged.Epoch)
	return merged
}

// deriveClusterID 由两个父集群标识与新纪元派生新集群标识（FNV-1a）。
// 结果对双方确定性一致，且必不等于任一父标识，
// 因此未参与合并的落后节点总会被识别为异集群并触发追赶。
func deriveClusterID(a, b, epoch uint64) uint64 {
	lo, hi := a, b
	if lo > hi {
		lo, hi = hi, lo
	}
	h := fnv.New64a()
	var buf [8]byte
	put := func(v uint64) {
		for i := 0; i < 8; i++ {
			buf[i] = byte(v >> (8 * i))
		}
		h.Write(buf[:])
	}
	put(lo)
	put(hi)
	put(epoch)
	id := h.Sum64()
	for id == a || id == b {
		id++
	}
	return id
}

// broadcast 在锁外逐个发送消息；发送本身非阻塞，绝不持锁等待。
func (n *Node) broadcast(env Envelope, targets []uint64) {
	for _, to := range targets {
		env.To = to
		n.net.Send(env)
	}
}
