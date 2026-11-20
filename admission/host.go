package admission

import "sync"

// State 是宿主的完整可复制准入状态。MemoryStore 通过整体替换 State
// 实现比较并替换（CAS）语义：任何提交都作用在某一版本快照上，
// 提交期间若版本被并发推进则整批失败重试，保证“整批成功才发布”。
type State struct {
	version       uint64
	generation    uint64
	policy        Policy
	root          Instance
	rootEvidence  RootEvidence
	instances     map[Nonce]Instance
	inflight      map[Nonce]Instance
	names         map[string]Nonce
	inflightNames map[string]Nonce
	consumed      map[Digest]bool
	batchReceipts map[Digest]Receipt
}

func emptyState() *State {
	return &State{
		instances:     map[Nonce]Instance{},
		inflight:      map[Nonce]Instance{},
		names:         map[string]Nonce{},
		inflightNames: map[string]Nonce{},
		consumed:      map[Digest]bool{},
		batchReceipts: map[Digest]Receipt{},
	}
}

// clone 深拷贝状态，CAS 回调始终在隔离副本上修改，失败不会污染已发布状态。
func (s *State) clone() *State {
	cp := *s
	cp.instances = make(map[Nonce]Instance, len(s.instances))
	for k, v := range s.instances {
		cp.instances[k] = v
	}
	cp.inflight = make(map[Nonce]Instance, len(s.inflight))
	for k, v := range s.inflight {
		cp.inflight[k] = v
	}
	cp.inflightNames = make(map[string]Nonce, len(s.inflightNames))
	for k, v := range s.inflightNames {
		cp.inflightNames[k] = v
	}
	cp.names = make(map[string]Nonce, len(s.names))
	for k, v := range s.names {
		cp.names[k] = v
	}
	cp.consumed = make(map[Digest]bool, len(s.consumed))
	for k, v := range s.consumed {
		cp.consumed[k] = v
	}
	cp.batchReceipts = make(map[Digest]Receipt, len(s.batchReceipts))
	for k, v := range s.batchReceipts {
		cp.batchReceipts[k] = v
	}
	return &cp
}

// Generation 返回当前单调恢复代次。
func (s *State) Generation() uint64 { return s.generation }

// PolicyVersion 返回当前策略版本。
func (s *State) PolicyVersion() uint64 { return s.policy.Version }

// RootEvidence 返回当前信任锚证据。
func (s *State) RootEvidence() RootEvidence { return s.rootEvidence }

// Root 返回已认证根实例。
func (s *State) Root() Instance { return s.root }

// Instance 按随机数查找已发布实例。
func (s *State) Instance(nonce Nonce) (Instance, bool) {
	inst, ok := s.instances[nonce]
	return inst, ok
}

// InstanceByName 按工作负载名称查找已发布实例。
func (s *State) InstanceByName(name string) (Instance, bool) {
	nonce, ok := s.names[name]
	if !ok {
		return Instance{}, false
	}
	return s.instances[nonce], true
}

// Inflight 按随机数查找已预登记但尚未提交的实例。
func (s *State) Inflight(nonce Nonce) (Instance, bool) {
	inst, ok := s.inflight[nonce]
	return inst, ok
}

// Receipt 按批次摘要取回已发布回执（用于回执丢失后的幂等取回）。
func (s *State) Receipt(batchDigest Digest) (Receipt, bool) {
	r, ok := s.batchReceipts[batchDigest]
	return r, ok
}

// Store 抽象宿主持久层。生产宿主可将 CAS 映射到带版本号的原子事务，
// 内存实现见 MemoryStore。
type Store interface {
	Snapshot() *State
	CompareAndSwap(prev uint64, next *State) (bool, error)
	Replace(next *State) error
}

// MemoryStore 是带互斥锁的内存宿主存储，CAS 以状态版本号为比较键。
type MemoryStore struct {
	mu    sync.Mutex
	state *State
}

// NewMemoryStore 创建空内存存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{state: emptyState()}
}

// Snapshot 返回当前状态的深拷贝。
func (m *MemoryStore) Snapshot() *State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state.clone()
}

// CompareAndSwap 仅当存储版本仍为 prev 时整体替换为 next。
func (m *MemoryStore) CompareAndSwap(prev uint64, next *State) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.version != prev {
		return false, nil
	}
	committed := next.clone()
	committed.version = prev + 1
	m.state = committed
	return true, nil
}

// Replace 不经版本检查直接替换状态，仅供引导与快照恢复建立新一代次使用。
func (m *MemoryStore) Replace(next *State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	committed := next.clone()
	committed.version = m.state.version + 1
	m.state = committed
	return nil
}
