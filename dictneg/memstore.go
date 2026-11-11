package dictneg

import "sync"

// MemStore 是进程内 Store 实现，供测试与不要求落盘的场景使用。
//
// 它额外提供两类故障注入：
//
//   - FailNext 让下一次状态/字典写入返回注入错误，模拟磁盘失败；
//   - 标记 volatile 的写入不保留数据，模拟“写入过程中掉电”——调用方
//     看不到成功，重启（重新构造角色）后旧状态仍在。
type MemStore struct {
	mu sync.Mutex

	dicts   map[Digest][]byte
	acc     map[string]*AcceptorState
	prop    map[string]*ProposerState
	failNex map[string]int

	// VolatileDicts/VolatileState 为 true 时，对应写入在返回成功后
	// 不真正保留，模拟掉电丢更新。
	VolatileDicts bool
	VolatileState bool
}

// NewMemStore 创建空的内存 Store。
func NewMemStore() *MemStore {
	return &MemStore{
		dicts:   map[Digest][]byte{},
		acc:     map[string]*AcceptorState{},
		prop:    map[string]*ProposerState{},
		failNex: map[string]int{},
	}
}

// FailNext 安排随后 n 次指定操作失败。op 取值：
// "dict"、"acceptor"、"proposer"。
func (m *MemStore) FailNext(op string, n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failNex[op] += n
}

func (m *MemStore) takeFail(op string) bool {
	if m.failNex[op] > 0 {
		m.failNex[op]--
		return true
	}
	return false
}

// PutDictionary 实现 Store。
func (m *MemStore) PutDictionary(d Digest, content []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.takeFail("dict") {
		return ErrInjectedFailure
	}
	if m.VolatileDicts {
		return nil
	}
	if _, ok := m.dicts[d]; ok {
		return nil
	}
	cp := make([]byte, len(content))
	copy(cp, content)
	m.dicts[d] = cp
	return nil
}

// GetDictionary 实现 Store。
func (m *MemStore) GetDictionary(d Digest) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	content, ok := m.dicts[d]
	if !ok {
		return nil, ErrUnknownDictionary
	}
	cp := make([]byte, len(content))
	copy(cp, content)
	return cp, nil
}

// HasDictionary 实现 Store。
func (m *MemStore) HasDictionary(d Digest) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.dicts[d]
	return ok
}

// SaveAcceptorState 实现 Store。
func (m *MemStore) SaveAcceptorState(name string, state *AcceptorState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.takeFail("acceptor") {
		return ErrInjectedFailure
	}
	if m.VolatileState {
		return nil
	}
	m.acc[name] = cloneAcceptor(state)
	return nil
}

// LoadAcceptorState 实现 Store。
func (m *MemStore) LoadAcceptorState(name string) (*AcceptorState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneAcceptor(m.acc[name]), nil
}

// SaveProposerState 实现 Store。
func (m *MemStore) SaveProposerState(name string, state *ProposerState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.takeFail("proposer") {
		return ErrInjectedFailure
	}
	if m.VolatileState {
		return nil
	}
	m.prop[name] = cloneProposer(state)
	return nil
}

// LoadProposerState 实现 Store。
func (m *MemStore) LoadProposerState(name string) (*ProposerState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneProposer(m.prop[name]), nil
}

func cloneAcceptor(s *AcceptorState) *AcceptorState {
	if s == nil {
		return nil
	}
	cp := *s
	if s.Installed != nil {
		cp.Installed = append([]Digest(nil), s.Installed...)
	}
	if s.NextSeq != nil {
		cp.NextSeq = make(map[string]uint64, len(s.NextSeq))
		for k, v := range s.NextSeq {
			cp.NextSeq[k] = v
		}
	}
	return &cp
}

func cloneProposer(s *ProposerState) *ProposerState {
	if s == nil {
		return nil
	}
	cp := *s
	return &cp
}
