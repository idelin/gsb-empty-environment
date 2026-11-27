package scim

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
)

// Store 是线程安全的内存 SCIM 资源库，提供全量快照事务：
// Transact 中的任意错误都会把整个库恢复到事务开始前的深拷贝快照，
// 从而保证 PATCH 多操作的原子回滚。宿主可参照该语义对接真实数据库事务。
type Store struct {
	mu          sync.RWMutex
	collections map[string]map[string]Resource
}

// NewStore 创建空存储。
func NewStore() *Store {
	return &Store{collections: map[string]map[string]Resource{}}
}

func (s *Store) collectionLocked(resourceType string) map[string]Resource {
	coll, ok := s.collections[resourceType]
	if !ok {
		coll = map[string]Resource{}
		s.collections[resourceType] = coll
	}
	return coll
}

// Put 写入（或覆盖）一个资源；缺少 id 时自动生成。返回入库副本。
func (s *Store) Put(resourceType string, r Resource) Resource {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := CloneResource(r)
	id, _ := cp["id"].(string)
	if id == "" {
		id = newID()
		cp["id"] = id
	}
	s.collectionLocked(resourceType)[id] = cp
	return CloneResource(cp)
}

// Get 返回资源的深拷贝。
func (s *Store) Get(resourceType, id string) (Resource, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.collections[resourceType][id]
	if !ok {
		return nil, false
	}
	return CloneResource(r), true
}

// List 返回某类资源的全部深拷贝。
func (s *Store) List(resourceType string) []Resource {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Resource, 0, len(s.collections[resourceType]))
	for _, r := range s.collections[resourceType] {
		out = append(out, CloneResource(r))
	}
	return out
}

// View 在读锁内执行只读事务。
func (s *Store) View(fn func(tx *Tx) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fn(&Tx{store: s})
}

// Transact 在写锁内执行原子事务；fn 返回错误时全量回滚。
func (s *Store) Transact(fn func(tx *Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := cloneCollections(s.collections)
	if err := fn(&Tx{store: s}); err != nil {
		s.collections = snapshot
		return err
	}
	return nil
}

func cloneCollections(in map[string]map[string]Resource) map[string]map[string]Resource {
	out := make(map[string]map[string]Resource, len(in))
	for rt, coll := range in {
		out[rt] = make(map[string]Resource, len(coll))
		for id, r := range coll {
			out[rt][id] = CloneResource(r)
		}
	}
	return out
}

// Tx 是持有锁的事务句柄，其方法不再额外加锁。
type Tx struct {
	store *Store
}

// Get 返回活动资源引用（仅应在 Transact 内修改）。
func (tx *Tx) Get(resourceType, id string) (Resource, bool) {
	r, ok := tx.store.collections[resourceType][id]
	return r, ok
}

// Put 在事务内写入资源。
func (tx *Tx) Put(resourceType string, r Resource) Resource {
	cp := CloneResource(r)
	id, _ := cp["id"].(string)
	if id == "" {
		id = newID()
		cp["id"] = id
	}
	coll := tx.store.collectionLocked(resourceType)
	coll[id] = cp
	return CloneResource(cp)
}

// Lookup 按 id 查找任意类型资源，优先 User/Group。
func (tx *Tx) Lookup(id string) (Resource, string, bool) {
	id = NormalizeReference(id)
	if id == "" {
		return nil, "", false
	}
	if r, ok := tx.Get("User", id); ok {
		return r, "User", true
	}
	if r, ok := tx.Get("Group", id); ok {
		return r, "Group", true
	}
	for rt, coll := range tx.store.collections {
		if rt == "User" || rt == "Group" {
			continue
		}
		if r, ok := coll[id]; ok {
			return r, rt, true
		}
	}
	return nil, "", false
}

// NormalizeReference 从 member.value / member.$ref 中提取裸 ID：
// 若是 URL 或相对引用，取最后一个路径段并去除 query。
func NormalizeReference(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	if i := strings.IndexAny(ref, "?#"); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		ref = ref[i+1:]
	}
	return ref
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "id-fallback"
	}
	return hex.EncodeToString(b[:])
}
