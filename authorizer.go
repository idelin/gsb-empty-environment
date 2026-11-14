package netsec

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// 授权失败原因。调用方可用 errors.Is 判定。
var (
	ErrUnknownSession   = errors.New("netsec: unknown session")
	ErrSessionExpired   = errors.New("netsec: session expired")
	ErrIdentityMismatch = errors.New("netsec: subject does not match session identity")
	ErrCrossDocument    = errors.New("netsec: operation targets a different document than the session")
	ErrUnknownDocument  = errors.New("netsec: unknown document")
	ErrStaleVersion     = errors.New("netsec: operation based on a stale document version")
	ErrFutureVersion    = errors.New("netsec: operation based on a future document version")
	ErrReplay           = errors.New("netsec: operation already applied (replay)")
	ErrPermissionDenied = errors.New("netsec: permission denied")
	ErrInvalidOperation = errors.New("netsec: malformed operation")
)

// OpType 是协同编辑操作类型。
type OpType string

const (
	OpInsert OpType = "insert"
	OpDelete OpType = "delete"
)

// Role 是主体在文档上的权限角色，权限递增：viewer < editor < owner。
type Role int

const (
	RoleNone Role = iota
	RoleViewer
	RoleEditor
	RoleOwner
)

func (r Role) String() string {
	switch r {
	case RoleViewer:
		return "viewer"
	case RoleEditor:
		return "editor"
	case RoleOwner:
		return "owner"
	default:
		return "none"
	}
}

// Operation 是一条待授权的编辑操作。操作必须绑定文档、会话、
// 主体与基准版本，四者缺一不可。
type Operation struct {
	ID          string // 客户端生成的全局唯一操作 ID，用于重放检测
	DocID       string
	SessionID   string
	SubjectID   string
	BaseVersion uint64 // 操作所基于的文档版本
	Type        OpType
	Position    int    // 基于 BaseVersion 内容的字符偏移
	Text        string // insert 内容
	Length      int    // delete 长度
}

// Session 是服务端签发的会话，绑定已认证主体与文档。
type Session struct {
	ID        string
	SubjectID string
	DocID     string
	ExpiresAt time.Time
}

// Authorizer 是供协同编辑服务调用的操作授权组件。
// 服务端必须在变换（transform）与合并之前调用 Authorize 过滤操作；
// 历史读取与回滚分别由 AuthorizeHistory / AuthorizeRollback 把关。
type Authorizer struct {
	mu       sync.Mutex
	sessions map[string]Session
	acls     map[string]map[string]Role // docID -> subjectID -> role
	versions map[string]uint64          // docID -> 当前已授权版本号
	seen     map[string]map[string]struct{}
	maxLag   uint64
	now      func() time.Time
}

// Option 配置 Authorizer。
type Option func(*Authorizer)

// WithMaxLag 设置允许的最大版本滞后窗口。基准版本落后于当前版本
// 超过该窗口的操作视为旧版本操作并拒绝。默认 16。
func WithMaxLag(lag uint64) Option {
	return func(a *Authorizer) { a.maxLag = lag }
}

// WithClock 注入时钟，便于测试会话过期。
func WithClock(now func() time.Time) Option {
	return func(a *Authorizer) { a.now = now }
}

func NewAuthorizer(opts ...Option) *Authorizer {
	a := &Authorizer{
		sessions: make(map[string]Session),
		acls:     make(map[string]map[string]Role),
		versions: make(map[string]uint64),
		seen:     make(map[string]map[string]struct{}),
		maxLag:   16,
		now:      time.Now,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// RegisterDocument 注册文档并将 owner 设为所有者。
func (a *Authorizer) RegisterDocument(docID, owner string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.acls[docID]; ok {
		return fmt.Errorf("netsec: document %q already registered", docID)
	}
	a.acls[docID] = map[string]Role{owner: RoleOwner}
	a.versions[docID] = 0
	a.seen[docID] = make(map[string]struct{})
	return nil
}

// Grant 授予主体在文档上的角色。
func (a *Authorizer) Grant(docID, subjectID string, role Role) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	acl, ok := a.acls[docID]
	if !ok {
		return ErrUnknownDocument
	}
	acl[subjectID] = role
	return nil
}

// IssueSession 为主体在指定文档上签发会话。主体必须已拥有该文档
// 的某种角色，否则拒绝签发。
func (a *Authorizer) IssueSession(sessionID, docID, subjectID string, ttl time.Duration) (Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	acl, ok := a.acls[docID]
	if !ok {
		return Session{}, ErrUnknownDocument
	}
	if acl[subjectID] == RoleNone {
		return Session{}, fmt.Errorf("%w: subject %q has no role on document %q",
			ErrPermissionDenied, subjectID, docID)
	}
	s := Session{
		ID:        sessionID,
		SubjectID: subjectID,
		DocID:     docID,
		ExpiresAt: a.now().Add(ttl),
	}
	a.sessions[sessionID] = s
	return s, nil
}

// RevokeSession 使会话失效。
func (a *Authorizer) RevokeSession(sessionID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, sessionID)
}

// Version 返回文档当前已授权版本号。
func (a *Authorizer) Version(docID string) (uint64, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	v, ok := a.versions[docID]
	return v, ok
}

// Authorize 校验一条编辑操作。通过时原子地记录操作 ID 并推进
// 文档版本，返回新版本号；随后服务端才可变换并应用该操作。
// 校验顺序：会话与身份 -> 文档绑定 -> 权限 -> 重放 -> 版本。
func (a *Authorizer) Authorize(op Operation) (uint64, error) {
	if op.ID == "" || op.DocID == "" || op.SessionID == "" || op.SubjectID == "" {
		return 0, fmt.Errorf("%w: id, doc, session and subject are required", ErrInvalidOperation)
	}
	if op.Type != OpInsert && op.Type != OpDelete {
		return 0, fmt.Errorf("%w: unsupported op type %q", ErrInvalidOperation, op.Type)
	}
	if op.Position < 0 || op.Length < 0 {
		return 0, fmt.Errorf("%w: negative position or length", ErrInvalidOperation)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	sess, err := a.sessionLocked(op.SessionID)
	if err != nil {
		return 0, err
	}
	// 伪造客户端标识：操作声明的主体必须等于会话认证主体。
	if op.SubjectID != sess.SubjectID {
		return 0, fmt.Errorf("%w: op subject %q, session subject %q",
			ErrIdentityMismatch, op.SubjectID, sess.SubjectID)
	}
	// 跨文档：操作目标文档必须等于会话绑定文档。
	if op.DocID != sess.DocID {
		return 0, fmt.Errorf("%w: op doc %q, session doc %q",
			ErrCrossDocument, op.DocID, sess.DocID)
	}
	current, ok := a.versions[op.DocID]
	if !ok {
		return 0, ErrUnknownDocument
	}
	// 权限过滤：编辑操作要求 editor 及以上角色。
	if a.acls[op.DocID][op.SubjectID] < RoleEditor {
		return 0, fmt.Errorf("%w: subject %q cannot edit document %q",
			ErrPermissionDenied, op.SubjectID, op.DocID)
	}
	// 重放检查：同一文档内操作 ID 只能应用一次。优先于版本检查，
	// 使重放旧操作被明确识别为重放攻击。
	if _, dup := a.seen[op.DocID][op.ID]; dup {
		return 0, fmt.Errorf("%w: op %q", ErrReplay, op.ID)
	}
	// 版本检查：拒绝来自未来与超出滞后窗口的旧版本操作。
	if op.BaseVersion > current {
		return 0, fmt.Errorf("%w: base %d > current %d",
			ErrFutureVersion, op.BaseVersion, current)
	}
	if current-op.BaseVersion > a.maxLag {
		return 0, fmt.Errorf("%w: base %d, current %d, max lag %d",
			ErrStaleVersion, op.BaseVersion, current, a.maxLag)
	}

	a.seen[op.DocID][op.ID] = struct{}{}
	a.versions[op.DocID] = current + 1
	return current + 1, nil
}

// AuthorizeHistory 校验主体是否可读取文档历史。viewer 及以上可读。
func (a *Authorizer) AuthorizeHistory(docID, sessionID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.authorizeAccessLocked(docID, sessionID, RoleViewer, "read history of")
}

// AuthorizeRollback 校验主体是否可将文档回滚到目标版本。
// 回滚会改写历史，要求 owner 角色，且目标版本不得超过当前版本。
func (a *Authorizer) AuthorizeRollback(docID, sessionID string, targetVersion uint64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.authorizeAccessLocked(docID, sessionID, RoleOwner, "roll back"); err != nil {
		return err
	}
	current := a.versions[docID]
	if targetVersion > current {
		return fmt.Errorf("%w: rollback target %d beyond current version %d",
			ErrInvalidOperation, targetVersion, current)
	}
	return nil
}

// CommitRollback 在回滚应用后同步授权组件的版本状态。
// 调用前必须先通过 AuthorizeRollback。
func (a *Authorizer) CommitRollback(docID string, targetVersion uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.versions[docID] = targetVersion
}

func (a *Authorizer) authorizeAccessLocked(docID, sessionID string, min Role, action string) error {
	sess, err := a.sessionLocked(sessionID)
	if err != nil {
		return err
	}
	if sess.DocID != docID {
		return fmt.Errorf("%w: session bound to doc %q, requested %q",
			ErrCrossDocument, sess.DocID, docID)
	}
	acl, ok := a.acls[docID]
	if !ok {
		return ErrUnknownDocument
	}
	if acl[sess.SubjectID] < min {
		return fmt.Errorf("%w: subject %q cannot %s document %q",
			ErrPermissionDenied, sess.SubjectID, action, docID)
	}
	return nil
}

func (a *Authorizer) sessionLocked(sessionID string) (Session, error) {
	sess, ok := a.sessions[sessionID]
	if !ok {
		return Session{}, ErrUnknownSession
	}
	if !a.now().Before(sess.ExpiresAt) {
		return Session{}, ErrSessionExpired
	}
	return sess, nil
}
