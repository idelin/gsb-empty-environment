package lease

import (
	"sync"
	"time"
)

// Node 集群节点:可作为租约持有者(主)或接盘候选人。
type Node struct {
	ID    string
	coord *Coordinator

	mu        sync.Mutex
	lease     *Lease
	primary   bool
	frozen    bool // 移交期间冻结本地写入
	down      bool
	renewStop chan struct{}
}

// NewNode 创建节点并注册到协调中心。
func NewNode(id string, coord *Coordinator) *Node {
	n := &Node{ID: id, coord: coord}
	coord.register(n)
	return n
}

// Shutdown 注销节点(模拟停机)。
func (n *Node) Shutdown() {
	n.mu.Lock()
	n.down = true
	n.stopRenewLocked()
	n.lease = nil
	n.primary = false
	n.mu.Unlock()
	n.coord.unregister(n.ID)
}

// Acquire 获取租约并成为主节点,随后启动动态续期循环。
func (n *Node) Acquire(leaseID string, term time.Duration, ctx map[string]string) error {
	l, err := n.coord.Acquire(leaseID, n.ID, term, ctx)
	if err != nil {
		return err
	}
	n.mu.Lock()
	n.lease = &l
	n.primary = true
	n.frozen = false
	n.startRenewLocked()
	n.mu.Unlock()
	return nil
}

// IsPrimary 报告节点当前是否持有有效租约。
func (n *Node) IsPrimary() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.primary && n.lease != nil
}

// Lease 返回当前租约凭证快照。
func (n *Node) Lease() (Lease, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.lease == nil {
		return Lease{}, false
	}
	return *n.lease, true
}

// Write 模拟一次主节点业务写入;移交冻结期间返回 ErrLeaseFrozen 供客户端重试。
func (n *Node) Write(payload string) error {
	n.mu.Lock()
	if n.frozen {
		n.mu.Unlock()
		return ErrLeaseFrozen
	}
	if !n.primary || n.lease == nil {
		n.mu.Unlock()
		return ErrNotPrimary
	}
	leaseID, token := n.lease.ID, n.lease.FencingToken
	n.mu.Unlock()
	return n.coord.ValidateWrite(leaseID, n.ID, token)
}

// GracefulHandover 优雅移交:冻结本地写入,把租约凭证交给候选节点,返回移交耗时。
func (n *Node) GracefulHandover(candidateID string) (time.Duration, error) {
	start := time.Now()
	n.mu.Lock()
	if n.lease == nil || !n.primary {
		n.mu.Unlock()
		return 0, ErrNotHolder
	}
	n.frozen = true // 冻结本地写入
	leaseID := n.lease.ID
	n.mu.Unlock()

	if err := n.coord.Handover(leaseID, n.ID, candidateID); err != nil {
		n.mu.Lock()
		n.frozen = false // 移交失败,解冻继续服务
		n.mu.Unlock()
		return 0, err
	}

	n.mu.Lock()
	n.stopRenewLocked()
	n.lease = nil
	n.primary = false
	n.frozen = false
	n.mu.Unlock()
	return time.Since(start), nil
}

// AcceptHandover 接收交接包,立即无缝接管主角色(由协调中心调用)。
func (n *Node) AcceptHandover(pkg HandoverPackage) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.down {
		return ErrNodeDown
	}
	l := pkg.Lease
	n.lease = &l
	n.primary = true
	n.frozen = false
	n.startRenewLocked()
	return nil
}

// startRenewLocked 启动动态续期循环:间隔随剩余 TTL 动态收缩(remaining/3)。
func (n *Node) startRenewLocked() {
	n.stopRenewLocked()
	n.renewStop = make(chan struct{})
	go n.renewLoop(n.renewStop)
}

// stopRenewLocked 通知续期循环退出。循环内对 n.lease 的更新均有指针相等性
// 保护,旧循环即使稍晚退出也不会覆盖新租约,因此无需持锁等待其结束。
func (n *Node) stopRenewLocked() {
	if n.renewStop != nil {
		close(n.renewStop)
		n.renewStop = nil
	}
}

func (n *Node) renewLoop(stop chan struct{}) {
	for {
		n.mu.Lock()
		l := n.lease
		n.mu.Unlock()
		if l == nil {
			return
		}
		remaining := time.Until(l.ExpiresAt)
		if remaining <= 0 {
			n.mu.Lock()
			if n.lease == l {
				n.lease = nil
				n.primary = false
			}
			n.mu.Unlock()
			return
		}
		interval := remaining / 3 // 动态续期:剩余越短,续期越频繁
		if interval < time.Millisecond {
			interval = time.Millisecond
		}
		select {
		case <-stop:
			return
		case <-time.After(interval):
		}
		nl, err := n.coord.Renew(l.ID, n.ID, l.FencingToken)
		if err != nil {
			// token 失效(租约已移交)或租约丢失,退出续期。
			if err == ErrStaleFencing || err == ErrNotHolder || err == ErrLeaseNotFound {
				return
			}
			continue
		}
		n.mu.Lock()
		if n.lease == l {
			n.lease = &nl
		}
		n.mu.Unlock()
	}
}
