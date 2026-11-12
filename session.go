package netsec

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"maps"
	"slices"
	"sync"
)

// 会话轮换相关的哨兵错误。
var (
	// ErrSessionNotFound 表示会话标识不存在。
	ErrSessionNotFound = errors.New("netsec: session not found")
	// ErrSessionInvalidated 表示会话标识已在轮换中失效，不可再被使用。
	ErrSessionInvalidated = errors.New("netsec: session invalidated")
	// ErrAlreadyAuthenticated 表示会话已经完成认证，不能重复升级。
	ErrAlreadyAuthenticated = errors.New("netsec: session already authenticated")
	// ErrIdentityBoundElsewhere 表示该用户身份已绑定到另一个有效会话。
	ErrIdentityBoundElsewhere = errors.New("netsec: identity already bound to another session")
	// ErrEmptyWhitelist 表示轮换未提供任何允许复制的匿名状态键。
	ErrEmptyWhitelist = errors.New("netsec: empty state whitelist")
)

// SessionID 是不可预测的随机会话标识。
type SessionID string

// NewSessionID 生成一个 256 位的随机会话标识。
func NewSessionID() (SessionID, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return SessionID(hex.EncodeToString(raw)), nil
}

// AuthResult 是认证成功后的结果，与新会话标识原子绑定。
type AuthResult struct {
	UserID   string
	Role     string
	AuthTime int64
}

// Session 是一次会话的快照，匿名会话 UserID 为空。
type Session struct {
	id     SessionID
	userID string
	role   string
	authT  int64
	state  map[string]string
	dead   bool
	// successor 仅在墓碑（dead=true）上有值，指向轮换后的新标识。
	successor SessionID
}

// ID 返回会话标识。
func (s *Session) ID() SessionID { return s.id }

// UserID 返回已认证用户标识；匿名会话返回空串。
func (s *Session) UserID() string { return s.userID }

// Role 返回认证后的角色。
func (s *Session) Role() string { return s.role }

// AuthTime 返回认证时间。
func (s *Session) AuthTime() int64 { return s.authT }

// Authenticated 报告会话是否已升级为登录状态。
func (s *Session) Authenticated() bool { return s.userID != "" }

// Invalidated 报告会话标识是否已被轮换失效。
func (s *Session) Invalidated() bool { return s.dead }

// StateValue 返回指定状态键的值。
func (s *Session) StateValue(key string) (string, bool) {
	v, ok := s.state[key]
	return v, ok
}

// snapshot 复制一份会话快照，避免调用方绕过锁修改内部 map。
func (s *Session) snapshot() *Session {
	cp := *s
	cp.state = maps.Clone(s.state)
	if cp.state == nil {
		cp.state = map[string]string{}
	}
	return &cp
}

// InvalidationError 是旧会话标识在轮换失效后唯一可读到的结果。
// 它明确告知旧标识已失效，并指向取而代之的新标识。
type InvalidationError struct {
	OldID SessionID
	NewID SessionID
}

func (e *InvalidationError) Error() string {
	return "netsec: session " + string(e.OldID) + " invalidated, superseded by " + string(e.NewID)
}

// Is 使 errors.Is(err, ErrSessionInvalidated) 成立。
func (e *InvalidationError) Is(target error) bool { return target == ErrSessionInvalidated }

// Committer 负责轮换事务的外部副作用（如持久化新标识、签发 Cookie）。
// Commit 在任何会话状态变更之前、且在存储锁内被调用；返回错误则整个
// 轮换事务中止，存储保持原状，调用方可安全重试。
type Committer interface {
	Commit(oldID, newID SessionID, result AuthResult) error
}

// CommitterFunc 让普通函数满足 Committer 接口。
type CommitterFunc func(oldID, newID SessionID, result AuthResult) error

// Commit 实现 Committer。
func (f CommitterFunc) Commit(oldID, newID SessionID, result AuthResult) error {
	return f(oldID, newID, result)
}

// Store 是并发安全的会话存储，支持匿名会话的原子轮换升级。
type Store struct {
	mu       sync.Mutex
	sessions map[SessionID]*Session
	// owners 记录每个已认证用户当前持有的有效会话，保证身份唯一归属。
	owners map[string]SessionID
}

// NewStore 创建空的会话存储。
func NewStore() *Store {
	return &Store{
		sessions: make(map[SessionID]*Session),
		owners:   make(map[string]SessionID),
	}
}

// NewAnonymous 创建一个匿名会话，并写入初始状态。
func (st *Store) NewAnonymous(initial map[string]string) (SessionID, error) {
	id, err := NewSessionID()
	if err != nil {
		return "", err
	}
	state := maps.Clone(initial)
	if state == nil {
		state = map[string]string{}
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	st.sessions[id] = &Session{id: id, state: state}
	return id, nil
}

// PutState 向有效会话写入状态。失效（墓碑）或不存在的会话拒绝写入，
// 从而防止并发请求用旧匿名状态覆盖新登录状态。
func (st *Store) PutState(id SessionID, key, value string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	s, ok := st.sessions[id]
	if !ok {
		return ErrSessionNotFound
	}
	if s.dead {
		return &InvalidationError{OldID: id, NewID: successorOf(s)}
	}
	s.state[key] = value
	return nil
}

// Get 读取会话快照。旧标识只能得到明确的失效错误，不会泄露新会话内容。
func (st *Store) Get(id SessionID) (*Session, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	s, ok := st.sessions[id]
	if !ok {
		return nil, ErrSessionNotFound
	}
	if s.dead {
		return nil, &InvalidationError{OldID: id, NewID: successorOf(s)}
	}
	return s.snapshot(), nil
}

// Rotate 以原子事务把匿名会话 oldID 升级为登录会话：
//  1. 仅允许复制白名单中的匿名状态键，其余状态一律丢弃；
//  2. 认证结果与新标识原子绑定，旧标识同步置为失效墓碑；
//  3. 同一用户身份只能归属一个有效会话；
//  4. Committer 失败则整体回滚，存储不留任何中间状态。
func (st *Store) Rotate(oldID SessionID, result AuthResult, stateWhitelist []string, committer Committer) (SessionID, error) {
	if len(stateWhitelist) == 0 {
		return "", ErrEmptyWhitelist
	}
	if committer == nil {
		return "", errors.New("netsec: nil committer")
	}

	newID, err := NewSessionID()
	if err != nil {
		return "", err
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	old, ok := st.sessions[oldID]
	if !ok {
		return "", ErrSessionNotFound
	}
	if old.dead {
		return "", &InvalidationError{OldID: oldID, NewID: successorOf(old)}
	}
	if old.userID != "" {
		return "", ErrAlreadyAuthenticated
	}
	if result.UserID == "" {
		return "", errors.New("netsec: empty user id in auth result")
	}
	if bound, exists := st.owners[result.UserID]; exists && bound != oldID {
		return "", ErrIdentityBoundElsewhere
	}

	// 先在锁内执行外部提交；失败即返回，存储尚未发生任何变更。
	if err := committer.Commit(oldID, newID, result); err != nil {
		return "", err
	}

	// 仅复制白名单内的匿名状态，键缺失时跳过而非写入空值。
	carried := make(map[string]string, len(stateWhitelist))
	for _, key := range slices.Compact(slices.Clone(stateWhitelist)) {
		if v, ok := old.state[key]; ok {
			carried[key] = v
		}
	}

	st.sessions[newID] = &Session{
		id:     newID,
		userID: result.UserID,
		role:   result.Role,
		authT:  result.AuthTime,
		state:  carried,
	}

	// 旧标识保留为墓碑：只承载“已失效”这一明确结果。
	old.dead = true
	old.successor = newID
	old.userID = ""
	old.role = ""
	old.authT = 0
	old.state = map[string]string{}

	st.owners[result.UserID] = newID
	return newID, nil
}

// OwnerOf 返回用户身份当前绑定的有效会话标识。
func (st *Store) OwnerOf(userID string) (SessionID, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	id, ok := st.owners[userID]
	return id, ok
}

// successorOf 取出墓碑中记录的继任标识。
func successorOf(s *Session) SessionID { return s.successor }
