package objstore

import (
	"errors"
	"sort"
	"sync"
	"time"
)

var (
	// ErrNotFound 表示对象从未存在过。
	ErrNotFound = errors.New("objstore: object not found")
	// ErrDeleted 表示对象已真正删除或已过宽限截止时刻；
	// 引用创建与重建请求一律拒绝，绝不隐式重建。
	ErrDeleted = errors.New("objstore: object is deleted")
	// ErrAlreadyExists 表示对象 id 已存在且仍存活。
	ErrAlreadyExists = errors.New("objstore: object already exists")
)

// State 是对象生命周期状态：Active → PendingDelete → Deleted（终态）。
type State int

const (
	Active State = iota
	PendingDelete
	Deleted
)

func (s State) String() string {
	switch s {
	case Active:
		return "Active"
	case PendingDelete:
		return "PendingDelete"
	case Deleted:
		return "Deleted"
	}
	return "Unknown"
}

type object struct {
	id       string
	gen      uint64
	state    State
	refs     int
	deleteAt time.Time
}

func (o *object) info() Info {
	return Info{
		ID:         o.id,
		State:      o.state,
		Generation: o.gen,
		Refs:       o.refs,
		DeleteAt:   o.deleteAt,
	}
}

// Info 是对象某一时刻的快照，也是引用账（ledger）的条目。
type Info struct {
	ID         string
	State      State
	Generation uint64
	Refs       int
	DeleteAt   time.Time
}

// Store 是带宽限期与代际机制的引用计数对象库。
//
// 语义：
//   - Delete 使对象进入待删态，并调度一个携带当前代际的清理任务；
//   - 宽限期内新增合法引用（AddRef）可撤销待删，但代际必须 +1，
//     旧清理任务再次触发时因代际不匹配而失效（只对原代生效）；
//   - 清理任务触发时若仍有引用未释放，则等待引用归零，
//     由最后一次 Release 在截止时刻过后完成真正删除；
//   - 一旦真正删除，id 永久退役：AddRef 与 Create 均失败，不能隐式重建。
type Store struct {
	clock   Clock
	grace   time.Duration
	mu      sync.Mutex
	objects map[string]*object
}

// NewStore 创建一个对象库；clk 为 nil 时使用真实时钟，grace 为删除宽限期。
func NewStore(clk Clock, grace time.Duration) *Store {
	if clk == nil {
		clk = RealClock()
	}
	return &Store{
		clock:   clk,
		grace:   grace,
		objects: make(map[string]*object),
	}
}

// Create 创建对象（初始代际为 1）。id 已存活返回 ErrAlreadyExists；
// id 已真正删除返回 ErrDeleted（id 永久退役，禁止重建）。
func (s *Store) Create(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if obj, ok := s.objects[id]; ok {
		if obj.state == Deleted {
			return ErrDeleted
		}
		return ErrAlreadyExists
	}
	s.objects[id] = &object{id: id, gen: 1, state: Active}
	return nil
}

// Delete 使对象进入待删态并调度清理任务；重复调用幂等，不顺延截止时刻。
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	obj, ok := s.objects[id]
	if !ok {
		s.mu.Unlock()
		return ErrNotFound
	}
	switch obj.state {
	case Deleted:
		s.mu.Unlock()
		return ErrDeleted
	case PendingDelete:
		s.mu.Unlock()
		return nil
	}
	obj.state = PendingDelete
	obj.deleteAt = s.clock.Now().Add(s.grace)
	gen := obj.gen
	s.mu.Unlock()
	s.clock.AfterFunc(s.grace, func() { s.sweep(id, gen) })
	return nil
}

// sweep 是清理任务入口：只对“仍为待删态且代际一致”的原代对象生效。
func (s *Store) sweep(id string, gen uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[id]
	if !ok || obj.state != PendingDelete || obj.gen != gen {
		return
	}
	if obj.refs > 0 {
		return // 等待引用释放，由最后一次 Release 完成真正删除
	}
	obj.state = Deleted
	obj.deleteAt = time.Time{}
}

// AddRef 为对象新增一个引用。
//
// 对象处于待删态且 now < deleteAt 时引用合法：撤销待删并将代际 +1；
// 对象已删除或已过宽限截止时刻时返回 ErrDeleted，绝不隐式重建。
func (s *Store) AddRef(id string) (*Ref, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[id]
	if !ok {
		return nil, ErrNotFound
	}
	if obj.state == Deleted {
		return nil, ErrDeleted
	}
	if obj.state == PendingDelete {
		if !s.clock.Now().Before(obj.deleteAt) {
			return nil, ErrDeleted
		}
		obj.state = Active
		obj.gen++
		obj.deleteAt = time.Time{}
	}
	obj.refs++
	return &Ref{store: s, id: id, gen: obj.gen}, nil
}

func (s *Store) release(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[id]
	if !ok {
		return
	}
	obj.refs--
	if obj.refs == 0 && obj.state == PendingDelete && !s.clock.Now().Before(obj.deleteAt) {
		obj.state = Deleted
		obj.deleteAt = time.Time{}
	}
}

// Get 返回对象快照；id 不存在时 ok 为 false。
func (s *Store) Get(id string) (Info, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[id]
	if !ok {
		return Info{}, false
	}
	return obj.info(), true
}

// Ledger 返回按 id 排序的引用账快照，用于核对不变量：
// Refs >= 0；Deleted 对象 Refs 必为 0；Generation 单调不减。
func (s *Store) Ledger() []Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Info, 0, len(s.objects))
	for _, obj := range s.objects {
		out = append(out, obj.info())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Ref 是对象的一个引用句柄；Release 幂等，多次调用只生效一次。
type Ref struct {
	store *Store
	id    string
	gen   uint64
	once  sync.Once
}

// ID 返回引用指向的对象 id。
func (r *Ref) ID() string { return r.id }

// Generation 返回引用建立时对象的代际。
func (r *Ref) Generation() uint64 { return r.gen }

// Release 释放引用；若对象处于待删态、已过宽限截止时刻且引用归零，
// 本次释放将完成真正删除。
func (r *Ref) Release() {
	r.once.Do(func() { r.store.release(r.id) })
}
