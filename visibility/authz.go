package visibility

import "sync"

// Authz 维护文档授权集合，支持发放不可变授权快照。它与 Index 相互独立：
// 撤回只影响之后发放的快照，已发放快照保持固定视图。
type Authz struct {
	mu        sync.RWMutex
	current   map[DocID]struct{}
	snapshots map[int64]map[DocID]struct{}
	nextID    int64
}

// NewAuthz 创建空授权器。
func NewAuthz() *Authz {
	return &Authz{
		current:   make(map[DocID]struct{}),
		snapshots: make(map[int64]map[DocID]struct{}),
	}
}

// Snapshot 是授权集合在某一时刻的不可变只读句柄。必须在用完后调用 Release，
// 否则授权器会一直保留该快照的副本。
type Snapshot struct {
	authz    *Authz
	id       int64
	released bool
}

// Grant 授予调用方对文档的访问权，返回授权器自身以便链式调用。
func (a *Authz) Grant(docs ...DocID) *Authz {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, doc := range docs {
		a.current[doc] = struct{}{}
	}
	return a
}

// Revoke 撤回对文档的访问权。对未授权文档的撤回是空操作；撤回不影响此前
// 已经发放、尚未释放的快照。
func (a *Authz) Revoke(docs ...DocID) *Authz {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, doc := range docs {
		delete(a.current, doc)
	}
	return a
}

// Snapshot 发放当前授权集合的不可变快照并登记持有。
func (a *Authz) Snapshot() *Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()

	id := a.nextID
	a.nextID++

	frozen := make(map[DocID]struct{}, len(a.current))
	for doc := range a.current {
		frozen[doc] = struct{}{}
	}
	a.snapshots[id] = frozen
	return &Snapshot{authz: a, id: id}
}

// Release 释放快照持有的授权副本。重复释放返回错误。
func (s *Snapshot) Release() error {
	if s == nil || s.authz == nil || s.released {
		return ErrSnapshotReleased
	}
	a := s.authz
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.snapshots[s.id]; !ok {
		s.released = true
		return ErrSnapshotReleased
	}
	delete(a.snapshots, s.id)
	s.released = true
	return nil
}

// validate 校验快照仍然有效，并返回其授权集合的副本。调用方可在持有索引
// 读锁时按固定锁序（Index -> Authz）调用；拿到副本后即释放授权锁，避免与
// Grant/Revoke 的 map 写入发生并发访问。
func (s *Snapshot) validate() (map[DocID]struct{}, error) {
	if s == nil || s.released {
		return nil, ErrSnapshotReleased
	}
	a := s.authz
	a.mu.RLock()
	defer a.mu.RUnlock()
	set, ok := a.snapshots[s.id]
	if !ok {
		s.released = true
		return nil, ErrSnapshotReleased
	}
	frozen := make(map[DocID]struct{}, len(set))
	for doc := range set {
		frozen[doc] = struct{}{}
	}
	return frozen, nil
}
