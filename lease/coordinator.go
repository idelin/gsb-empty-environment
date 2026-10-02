package lease

import (
	"sync"
	"time"
)

// Coordinator 协调中心:负责租约的授予、续期、校验与主动移交仲裁。
type Coordinator struct {
	mu     sync.Mutex
	leases map[string]*Lease
	nodes  map[string]*Node
	token  uint64
}

// NewCoordinator 创建协调中心。
func NewCoordinator() *Coordinator {
	return &Coordinator{
		leases: make(map[string]*Lease),
		nodes:  make(map[string]*Node),
	}
}

func (c *Coordinator) register(n *Node) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nodes[n.ID] = n
}

func (c *Coordinator) unregister(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.nodes, id)
}

// Node 按 ID 返回已注册节点,供客户端解析当前主节点。
func (c *Coordinator) Node(id string) *Node {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nodes[id]
}

// Primary 返回租约当前持有者 ID。
func (c *Coordinator) Primary(leaseID string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.leases[leaseID]
	if !ok {
		return "", ErrLeaseNotFound
	}
	return l.HolderID, nil
}

// Acquire 授予租约;若已被持有且未过期则返回 ErrNotHolder。
func (c *Coordinator) Acquire(leaseID, holderID string, term time.Duration, ctx map[string]string) (Lease, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if l, ok := c.leases[leaseID]; ok && now.Before(l.ExpiresAt) && l.HolderID != holderID {
		return Lease{}, ErrNotHolder
	}
	c.token++
	l := &Lease{
		ID:           leaseID,
		HolderID:     holderID,
		FencingToken: c.token,
		Term:         term,
		ExpiresAt:    now.Add(term),
		Context:      copyContext(ctx),
	}
	c.leases[leaseID] = l
	cp := *l
	cp.Context = copyContext(l.Context)
	return cp, nil
}

// Renew 动态续期:持有者凭 fencing token 延长租约。
func (c *Coordinator) Renew(leaseID, holderID string, token uint64) (Lease, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.leases[leaseID]
	if !ok {
		return Lease{}, ErrLeaseNotFound
	}
	if l.HolderID != holderID {
		return Lease{}, ErrNotHolder
	}
	if l.FencingToken != token {
		return Lease{}, ErrStaleFencing
	}
	now := time.Now()
	if !now.Before(l.ExpiresAt) {
		return Lease{}, ErrLeaseExpired
	}
	l.ExpiresAt = now.Add(l.Term)
	cp := *l
	cp.Context = copyContext(l.Context)
	return cp, nil
}

// ValidateWrite 校验一次主节点写入是否合法(持有者、token、未过期)。
func (c *Coordinator) ValidateWrite(leaseID, holderID string, token uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.leases[leaseID]
	if !ok {
		return ErrLeaseNotFound
	}
	if l.HolderID != holderID {
		return ErrNotHolder
	}
	if l.FencingToken != token {
		return ErrStaleFencing
	}
	if !time.Now().Before(l.ExpiresAt) {
		return ErrLeaseExpired
	}
	return nil
}

// Handover 主动移交协议:原持有者 fromID 计划停机,将租约凭证无缝交给候选人 toID。
// 候选人继承剩余 TTL 与业务上下文,无需等待自然超时。
func (c *Coordinator) Handover(leaseID, fromID, toID string) error {
	c.mu.Lock()
	l, ok := c.leases[leaseID]
	if !ok {
		c.mu.Unlock()
		return ErrLeaseNotFound
	}
	if l.HolderID != fromID {
		c.mu.Unlock()
		return ErrNotHolder
	}
	candidate, ok := c.nodes[toID]
	if !ok {
		c.mu.Unlock()
		return ErrNoCandidate
	}
	now := time.Now()
	if !now.Before(l.ExpiresAt) {
		c.mu.Unlock()
		return ErrLeaseExpired
	}

	// 原子提交移交:fencing token 递增,旧持有者的续期与写立即失效。
	c.token++
	pkg := HandoverPackage{
		Lease: Lease{
			ID:           l.ID,
			HolderID:     toID,
			FencingToken: c.token,
			Term:         l.Term,
			ExpiresAt:    l.ExpiresAt, // 继承剩余 TTL
			Context:      copyContext(l.Context),
		},
		FromID:        fromID,
		TransferredAt: now,
	}
	prev := *l
	prev.Context = copyContext(l.Context)
	l.HolderID = toID
	l.FencingToken = c.token
	l.Context = copyContext(pkg.Lease.Context)
	c.mu.Unlock()

	// 锁外交付交接包,避免与候选节点写路径的锁序反转。
	if err := candidate.AcceptHandover(pkg); err != nil {
		c.mu.Lock()
		l.HolderID = prev.HolderID
		l.FencingToken = prev.FencingToken
		l.Context = prev.Context
		c.mu.Unlock()
		return err
	}
	return nil
}
