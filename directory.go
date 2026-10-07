// Package dirdb 提供进程内目录存储：对象按 id、唯一名称与可重复标签建索引。
// 批量修改是事务性的：批内允许名称交换，任何冲突导致整批失败并回滚。
// 读快照在三个索引间保持一致；删除对象会清除其全部反向标签引用。
package dirdb

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Object 是目录中存储的条目。Name 全局唯一；Tags 可重复出现在多个对象上。
type Object struct {
	ID   string
	Name string
	Tags []string
}

// clone 返回对象的深拷贝。
func (o Object) clone() Object {
	tags := make([]string, len(o.Tags))
	copy(tags, o.Tags)
	return Object{ID: o.ID, Name: o.Name, Tags: tags}
}

// OpKind 表示批量操作的类型。
type OpKind int

const (
	// OpUpsert 插入或替换同 id 对象。
	OpUpsert OpKind = iota
	// OpDelete 按 id 删除对象。
	OpDelete
)

// Op 是批量事务中的一条操作。
type Op struct {
	Kind   OpKind
	Object Object // OpUpsert 使用
	ID     string // OpDelete 使用
}

// Upsert 构造一条插入/替换操作。
func Upsert(o Object) Op { return Op{Kind: OpUpsert, Object: o} }

// Delete 构造一条按 id 删除操作。
func Delete(id string) Op { return Op{Kind: OpDelete, ID: id} }

// 错误定义。
var (
	ErrEmptyID       = errors.New("dirdb: object id is empty")
	ErrEmptyName     = errors.New("dirdb: object name is empty")
	ErrIDConflict    = errors.New("dirdb: duplicate id in batch")
	ErrNameConflict  = errors.New("dirdb: name already taken")
	ErrObjectMissing = errors.New("dirdb: object not found")
)

// indexes 是三个索引的不可变组合，任一时刻目录只持有其中一份，
// 因此按任意索引读取都天然跨索引一致。
type indexes struct {
	byID   map[string]Object          // id -> object
	byName map[string]string          // name -> id
	byTag  map[string]map[string]bool // tag -> id 集合（反向引用）
}

// cloneIndexes 深拷贝全部索引，供事务在副本上试算。
func cloneIndexes(src *indexes) *indexes {
	dst := &indexes{
		byID:   make(map[string]Object, len(src.byID)),
		byName: make(map[string]string, len(src.byName)),
		byTag:  make(map[string]map[string]bool, len(src.byTag)),
	}
	for id, o := range src.byID {
		dst.byID[id] = o.clone()
	}
	for name, id := range src.byName {
		dst.byName[name] = id
	}
	for tag, ids := range src.byTag {
		set := make(map[string]bool, len(ids))
		for id := range ids {
			set[id] = true
		}
		dst.byTag[tag] = set
	}
	return dst
}

// detach 从名称与标签索引中移除对象（删除或替换前的清理）。
func detach(ix *indexes, o Object) {
	delete(ix.byName, o.Name)
	for _, tag := range o.Tags {
		if set, ok := ix.byTag[tag]; ok {
			delete(set, o.ID)
			if len(set) == 0 {
				delete(ix.byTag, tag)
			}
		}
	}
}

// attach 把对象写入全部三个索引；调用前必须保证名称空闲。
func attach(ix *indexes, o Object) {
	ix.byID[o.ID] = o
	ix.byName[o.Name] = o.ID
	for _, tag := range o.Tags {
		set, ok := ix.byTag[tag]
		if !ok {
			set = make(map[string]bool)
			ix.byTag[tag] = set
		}
		set[o.ID] = true
	}
}

// Directory 是线程安全的内存目录。
type Directory struct {
	mu sync.RWMutex
	ix *indexes
}

// New 返回空目录。
func New() *Directory {
	return &Directory{ix: &indexes{
		byID:   make(map[string]Object),
		byName: make(map[string]string),
		byTag:  make(map[string]map[string]bool),
	}}
}

// Apply 以事务方式执行一批操作。整个过程在索引副本上进行，
// 全部成功才一次性切换；任何冲突都使整批失败，目录保持原状。
//
// 提交分两阶段：先从副本摘除所有被触及对象的旧索引项，
// 再统一写入新值并校验名称唯一性。因此批内两条操作互换名称是
// 合法的——名称占用按批末状态判定，与操作顺序无关。
func (d *Directory) Apply(ops []Op) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	next := cloneIndexes(d.ix)
	seen := make(map[string]bool, len(ops))

	// 第一阶段：校验并摘除被触及对象的旧索引项。
	var inserts []Object
	for _, op := range ops {
		switch op.Kind {
		case OpUpsert:
			o := op.Object
			if o.ID == "" {
				return ErrEmptyID
			}
			if o.Name == "" {
				return ErrEmptyName
			}
			if seen[o.ID] {
				return fmt.Errorf("%v: %s", ErrIDConflict, o.ID)
			}
			seen[o.ID] = true
			if old, ok := next.byID[o.ID]; ok {
				detach(next, old)
				delete(next.byID, o.ID)
			}
			inserts = append(inserts, o.clone())
		case OpDelete:
			if op.ID == "" {
				return ErrEmptyID
			}
			if seen[op.ID] {
				return fmt.Errorf("%v: %s", ErrIDConflict, op.ID)
			}
			seen[op.ID] = true
			old, ok := next.byID[op.ID]
			if !ok {
				return fmt.Errorf("%v: %s", ErrObjectMissing, op.ID)
			}
			detach(next, old) // 清除全部反向标签引用
			delete(next.byID, op.ID)
		default:
			return fmt.Errorf("dirdb: unknown op kind %d", op.Kind)
		}
	}

	// 第二阶段：写入新对象，名称必须空闲。
	for _, o := range inserts {
		if holder, ok := next.byName[o.Name]; ok {
			return fmt.Errorf("%v: %q held by %s", ErrNameConflict, o.Name, holder)
		}
		attach(next, o)
	}

	d.ix = next // 原子提交；此前任何返回都等于回滚
	return nil
}

// Snapshot 是目录的一致只读视图：三个索引来自同一份拷贝，
// 后续写操作不影响快照内容。
type Snapshot struct {
	ix *indexes
}

// Snapshot 抓取当前一致快照。
func (d *Directory) Snapshot() *Snapshot {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return &Snapshot{ix: cloneIndexes(d.ix)}
}

// Get 按 id 查询。
func (s *Snapshot) Get(id string) (Object, bool) {
	o, ok := s.ix.byID[id]
	return o.clone(), ok
}

// Lookup 按唯一名称查询。
func (s *Snapshot) Lookup(name string) (Object, bool) {
	id, ok := s.ix.byName[name]
	if !ok {
		return Object{}, false
	}
	return s.Get(id)
}

// Tagged 返回带有指定标签的全部对象，按 id 排序保证结果稳定。
func (s *Snapshot) Tagged(tag string) []Object {
	set := s.ix.byTag[tag]
	out := make([]Object, 0, len(set))
	for id := range set {
		out = append(out, s.ix.byID[id].clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Len 返回对象总数。
func (s *Snapshot) Len() int { return len(s.ix.byID) }

// rebuildIndexes 从唯一的 byID 字典重建名称与标签索引，
// 用于核对目录内部索引是否一致（测试与自检共用）。
func rebuildIndexes(byID map[string]Object) *indexes {
	ix := &indexes{
		byID:   make(map[string]Object, len(byID)),
		byName: make(map[string]string, len(byID)),
		byTag:  make(map[string]map[string]bool),
	}
	for id, o := range byID {
		ix.byID[id] = o.clone()
		ix.byName[o.Name] = id
		for _, tag := range o.Tags {
			set, ok := ix.byTag[tag]
			if !ok {
				set = make(map[string]bool)
				ix.byTag[tag] = set
			}
			set[id] = true
		}
	}
	return ix
}

// equalIndexes 比较两份索引是否完全一致。
func equalIndexes(a, b *indexes) bool {
	if len(a.byID) != len(b.byID) || len(a.byName) != len(b.byName) || len(a.byTag) != len(b.byTag) {
		return false
	}
	for id, oa := range a.byID {
		ob, ok := b.byID[id]
		if !ok || oa.Name != ob.Name || !equalStrings(oa.Tags, ob.Tags) {
			return false
		}
	}
	for name, id := range a.byName {
		if b.byName[name] != id {
			return false
		}
	}
	for tag, setA := range a.byTag {
		setB, ok := b.byTag[tag]
		if !ok || len(setA) != len(setB) {
			return false
		}
		for id := range setA {
			if !setB[id] {
				return false
			}
		}
	}
	return true
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Verify 用 byID 单字典重建全部索引并与内部索引比对，
// 一致返回 nil，否则返回描述性错误。供自检与测试调用。
func (d *Directory) Verify() error {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if !equalIndexes(rebuildIndexes(d.ix.byID), d.ix) {
		return errors.New("dirdb: indexes diverged from byID dictionary")
	}
	return nil
}
