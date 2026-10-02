package merge

import (
	"sync"
	"sync/atomic"
	"time"
)

// pendingMerge 记录一次等待对端 Commit 的合并协商（用于超时重试）。
type pendingMerge struct {
	contact uint64
	sentAt  time.Time
}

// Node 是集群节点：维护本侧视图、Lamport 时钟，
// 通过周期性 PingReq 探活，并在发现异集群节点时执行合并握手。
type Node struct {
	ID    uint64
	net   *Network
	seeds []uint64 // 静态种子地址（含全部节点），用于重连后发现对侧分区

	mu    sync.Mutex
	view  View
	clock LamportClock

	inbox  chan Envelope
	stopCh chan struct{}
	wg     sync.WaitGroup

	gossipInterval time.Duration
	retryAfter     time.Duration

	pending map[uint64]pendingMerge // clusterID -> 等待中的合并协商

	mergeRounds atomic.Uint64 // 作为发起方完成的合并次数（测试观测用）
	randState   atomic.Uint64
}

// NewNode 创建节点。initial 为该节点初始可见的集群视图
// （分区场景下即本子网的成员与集群标识）。
func NewNode(id uint64, net *Network, seeds []uint64, initial View) *Node {
	n := &Node{
		ID:             id,
		net:            net,
		seeds:          seeds,
		view:           initial.Clone(),
		inbox:          make(chan Envelope, 1024),
		stopCh:         make(chan struct{}),
		gossipInterval: 20 * time.Millisecond,
		retryAfter:     100 * time.Millisecond,
		pending:        make(map[uint64]pendingMerge),
	}
	n.randState.Store(id*2654435761 + 1)
	net.attach(n)
	return n
}

// Start 启动消息处理与 gossip 两个后台循环。
func (n *Node) Start() {
	n.wg.Add(2)
	go n.handleLoop()
	go n.gossipLoop()
}

// Stop 停止后台循环并等待退出，保证无泄漏 goroutine。
func (n *Node) Stop() {
	close(n.stopCh)
	n.wg.Wait()
}

// Snapshot 返回当前视图深拷贝与本地时钟值（测试/演示观测用）。
func (n *Node) Snapshot() (View, uint64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.view.Clone(), n.clock.Value()
}

// MergeRounds 返回本节点作为合并发起方完成的合并次数。
func (n *Node) MergeRounds() uint64 {
	return n.mergeRounds.Load()
}

// NoteLocalFailure 模拟一次本地成员故障事件：将成员标记为 Suspect
// 并推进纪元。用于让两个分区在隔离期间“各自独立演化”。
func (n *Node) NoteLocalFailure(suspect uint64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if m, ok := n.view.Members[suspect]; ok {
		m.State = Suspect
		n.view.Members[suspect] = m
		n.view.Epoch++
	}
}

// gossipLoop 周期性推进时钟、重试未完成的合并、向随机种子发送 PingReq。
func (n *Node) gossipLoop() {
	defer n.wg.Done()
	ticker := time.NewTicker(n.gossipInterval)
	defer ticker.Stop()
	for {
		select {
		case <-n.stopCh:
			return
		case <-ticker.C:
			n.clock.Tick()
			n.retryPendingMerges()
			n.pingRandomSeed()
		}
	}
}

// handleLoop 串行消费收件箱消息。
func (n *Node) handleLoop() {
	defer n.wg.Done()
	for {
		select {
		case <-n.stopCh:
			return
		case env := <-n.inbox:
			n.handle(env)
		}
	}
}

func (n *Node) handle(env Envelope) {
	// 任何入站消息都先做时钟校正（Lamport 规则）。
	n.clock.Witness(env.Clock)
	switch env.Type {
	case MsgPingReq:
		n.handlePingReq(env)
	case MsgAck:
		n.handleAck(env)
	case MsgMergeHello:
		n.handleMergeHello(env)
	case MsgMergeCommit:
		n.handleMergeCommit(env)
	}
}

// pingRandomSeed 向一个随机种子（非自身）发送 PingReq。
func (n *Node) pingRandomSeed() {
	if len(n.seeds) == 0 {
		return
	}
	idx := n.nextRand() % uint64(len(n.seeds))
	target := n.seeds[idx]
	if target == n.ID {
		target = n.seeds[(idx+1)%uint64(len(n.seeds))]
	}
	if target == n.ID {
		return
	}
	n.mu.Lock()
	env := Envelope{
		Type:      MsgPingReq,
		From:      n.ID,
		To:        target,
		ClusterID: n.view.ClusterID,
		Epoch:     n.view.Epoch,
	}
	n.mu.Unlock()
	env.Clock = n.clock.Tick()
	n.net.Send(env)
}

// handlePingReq 应答心跳；若发现对端属于不同集群，触发合并握手。
func (n *Node) handlePingReq(env Envelope) {
	n.mu.Lock()
	foreign := env.ClusterID != n.view.ClusterID
	ack := Envelope{
		Type:      MsgAck,
		From:      n.ID,
		To:        env.From,
		ClusterID: n.view.ClusterID,
		Epoch:     n.view.Epoch,
	}
	n.mu.Unlock()
	ack.Clock = n.clock.Tick()
	n.net.Send(ack)
	if foreign {
		n.sendMergeHello(env.From)
	}
}

// handleAck 处理心跳应答；异集群应答同样触发合并握手（双向发现）。
func (n *Node) handleAck(env Envelope) {
	n.mu.Lock()
	foreign := env.ClusterID != n.view.ClusterID
	n.mu.Unlock()
	if foreign {
		n.sendMergeHello(env.From)
	}
}

// nextRand 是简单的 xorshift 伪随机源，避免全局锁竞争。
func (n *Node) nextRand() uint64 {
	x := n.randState.Load()
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	n.randState.Store(x)
	return x
}
