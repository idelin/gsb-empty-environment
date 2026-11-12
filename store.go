package netsec

import (
	"errors"
	"sync"
)

// ObjectState 表示对象在创建事务中的生命周期状态。
type ObjectState int

const (
	// StatePending 表示对象已分配但尚未完成标签持久化，对外不可见。
	StatePending ObjectState = iota
	// StateCommitted 表示对象已完成完整授权状态，对外可见。
	StateCommitted
)

// Object 是资源宿主中存储的对象记录。
type Object struct {
	ID     string
	Tags   TagSet
	Digest string
	State  ObjectState
}

// Store 抽象对象与标签的持久化。实现必须保证：
// Get 只返回 StateCommitted 的对象，pending 对象对外部读者不可见。
type Store interface {
	// CreateObject 分配一个 pending 状态的占位对象。
	CreateObject(id string) error
	// PutTags 持久化最终合并标签及其摘要。
	PutTags(id string, tags TagSet, digest string) error
	// Commit 将对象置为对外可见。
	Commit(id string) error
	// Delete 回滚删除对象（用于事务失败路径）。
	Delete(id string) error
	// Get 返回已提交对象；pending 或不存在时 ok=false。
	Get(id string) (obj Object, ok bool)
}

var (
	// ErrObjectExists 表示对象 ID 冲突。
	ErrObjectExists = errors.New("netsec: object already exists")
	// ErrObjectNotFound 表示对象不存在。
	ErrObjectNotFound = errors.New("netsec: object not found")
)

// MemStore 是 Store 的内存实现。
type MemStore struct {
	mu      sync.RWMutex
	objects map[string]*Object
}

// NewMemStore 创建空的内存存储。
func NewMemStore() *MemStore {
	return &MemStore{objects: make(map[string]*Object)}
}

func (s *MemStore) CreateObject(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objects[id]; ok {
		return ErrObjectExists
	}
	s.objects[id] = &Object{ID: id, State: StatePending}
	return nil
}

func (s *MemStore) PutTags(id string, tags TagSet, digest string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[id]
	if !ok {
		return ErrObjectNotFound
	}
	cp := make(TagSet, len(tags))
	for k, v := range tags {
		cp[k] = v
	}
	obj.Tags = cp
	obj.Digest = digest
	return nil
}

func (s *MemStore) Commit(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[id]
	if !ok {
		return ErrObjectNotFound
	}
	obj.State = StateCommitted
	return nil
}

func (s *MemStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, id)
	return nil
}

func (s *MemStore) Get(id string) (Object, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	obj, ok := s.objects[id]
	if !ok || obj.State != StateCommitted {
		return Object{}, false
	}
	cp := *obj
	return cp, true
}

// 可失败存储的故障注入点。
const (
	FailCreateObject = "create_object"
	FailPutTags      = "put_tags"
	FailCommit       = "commit"
	FailDelete       = "delete"
)

// ErrInjected 是可失败存储注入的模拟故障。
var ErrInjected = errors.New("netsec: injected storage failure")

// FailableStore 包装 Store，在指定操作点注入故障，
// 用于模拟对象创建与标签持久化各阶段的失败。
type FailableStore struct {
	Inner Store

	mu      sync.Mutex
	failAt  map[string]bool
	oneShot map[string]bool
}

// NewFailableStore 包装 inner 返回可失败存储。
func NewFailableStore(inner Store) *FailableStore {
	return &FailableStore{
		Inner:   inner,
		failAt:  make(map[string]bool),
		oneShot: make(map[string]bool),
	}
}

// Arm 在指定故障点开启注入；oneShot 为 true 时只失败一次。
func (f *FailableStore) Arm(point string, oneShot bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failAt[point] = true
	f.oneShot[point] = oneShot
}

// Disarm 关闭指定故障点。
func (f *FailableStore) Disarm(point string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.failAt, point)
}

func (f *FailableStore) shouldFail(point string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.failAt[point] {
		return false
	}
	if f.oneShot[point] {
		delete(f.failAt, point)
	}
	return true
}

func (f *FailableStore) CreateObject(id string) error {
	if f.shouldFail(FailCreateObject) {
		return ErrInjected
	}
	return f.Inner.CreateObject(id)
}

func (f *FailableStore) PutTags(id string, tags TagSet, digest string) error {
	if f.shouldFail(FailPutTags) {
		return ErrInjected
	}
	return f.Inner.PutTags(id, tags, digest)
}

func (f *FailableStore) Commit(id string) error {
	if f.shouldFail(FailCommit) {
		return ErrInjected
	}
	return f.Inner.Commit(id)
}

func (f *FailableStore) Delete(id string) error {
	if f.shouldFail(FailDelete) {
		return ErrInjected
	}
	return f.Inner.Delete(id)
}

func (f *FailableStore) Get(id string) (Object, bool) {
	return f.Inner.Get(id)
}
