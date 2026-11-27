package mvcc

import (
	"errors"
	"sync"
)

// MultiXactId 是 32 位多事务锁组编号，同样按环形回绕分配。
type MultiXactId uint32

const (
	// InvalidMultiXactId 是非法 MultiXact 编号，固定排在所有编号之前。
	InvalidMultiXactId MultiXactId = 0
	// FirstMultiXactId 是首个正常分配的 MultiXact 编号。
	FirstMultiXactId MultiXactId = 1
)

// MultiXactStatus 是 MultiXact 成员的状态。前两种为纯共享行锁，
// 后三种携带真正的修改/删除语义；一个 MultiXact 中至多有一个修改成员。
type MultiXactStatus uint8

const (
	MultiXactStatusForKeyShare    MultiXactStatus = iota // KEY SHARE 行锁
	MultiXactStatusForShare                              // SHARE 行锁
	MultiXactStatusForNoKeyUpdate                        // 不修改键值的 UPDATE
	MultiXactStatusForUpdate                             // 修改键值的 UPDATE
	MultiXactStatusForDelete                             // DELETE
)

// IsLock 报告该成员是否仅持有共享行锁（不修改元组）。
func (s MultiXactStatus) IsLock() bool {
	return s == MultiXactStatusForKeyShare || s == MultiXactStatusForShare
}

// IsUpdate 报告该成员是否为真正的修改/删除事务。
func (s MultiXactStatus) IsUpdate() bool {
	return !s.IsLock()
}

// MultiXactMember 描述 MultiXact 中的一个成员事务。
type MultiXactMember struct {
	Xid    XID
	Status MultiXactStatus
}

var (
	// ErrInvalidMultiXact 表示引用了不存在的 MultiXact。
	ErrInvalidMultiXact = errors.New("mvcc: invalid MultiXactId")
	// ErrInvalidMemberXid 表示成员携带了非法（特殊）事务号。
	ErrInvalidMemberXid = errors.New("mvcc: MultiXact member uses a special XID")
	// ErrMultipleUpdaters 表示一个 MultiXact 中出现了多个修改成员。
	ErrMultipleUpdaters = errors.New("mvcc: MultiXact contains more than one updater")
)

// MultiXactStore 是内存版 MultiXact 成员存储，并发安全，
// 编号在 32 位空间内环形分配，回绕时跳过 0。
type MultiXactStore struct {
	mu      sync.RWMutex
	members map[MultiXactId][]MultiXactMember
	next    MultiXactId
}

// NewMultiXactStore 创建空存储，下一个编号从 FirstMultiXactId 开始。
func NewMultiXactStore() *MultiXactStore {
	return &MultiXactStore{
		members: make(map[MultiXactId][]MultiXactMember),
		next:    FirstMultiXactId,
	}
}

// Create 校验并登记一个新的 MultiXact，返回分配的编号。
// 成员切片会被拷贝，调用方后续修改入参不会影响存储内容。
func (s *MultiXactStore) Create(members []MultiXactMember) (MultiXactId, error) {
	updaters := 0
	for _, m := range members {
		if m.Xid < FirstNormalXid {
			return InvalidMultiXactId, ErrInvalidMemberXid
		}
		if m.Status.IsUpdate() {
			updaters++
			if updaters > 1 {
				return InvalidMultiXactId, ErrMultipleUpdaters
			}
		}
	}

	copied := append([]MultiXactMember(nil), members...)

	s.mu.Lock()
	defer s.mu.Unlock()

	id := s.next
	s.members[id] = copied
	s.next++
	if s.next == InvalidMultiXactId {
		s.next = FirstMultiXactId
	}
	return id, nil
}

// Members 返回某 MultiXact 的成员副本；编号不存在时返回 ErrInvalidMultiXact。
func (s *MultiXactStore) Members(id MultiXactId) ([]MultiXactMember, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	members, ok := s.members[id]
	if !ok {
		return nil, ErrInvalidMultiXact
	}
	return append([]MultiXactMember(nil), members...), nil
}

// Discard 丢弃一个 MultiXact（模拟被截断清理）。
func (s *MultiXactStore) Discard(id MultiXactId) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.members, id)
}

// NextId 返回下一个将被分配的 MultiXact 编号。
func (s *MultiXactStore) NextId() MultiXactId {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.next
}

// MultiXactIdPrecedes 判定 MultiXact 编号 a 是否早于 b。
// InvalidMultiXactId 不参与环形运算，固定早于一切正常编号；
// 正常编号之间使用有符号 32 位差值比较，支持回绕。
func MultiXactIdPrecedes(a, b MultiXactId) bool {
	if a == InvalidMultiXactId || b == InvalidMultiXactId {
		return a < b
	}
	return int32(a-b) < 0
}
