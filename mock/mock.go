// Package mock 提供用于测试 gateway 的模拟物理连接与模拟服务端。
//
// 两条连接会“故意”返回相同的服务端语句编号（都从 1 开始），
// 用来验证网关的连接代次绑定：编号相同但物理连接不同，绝不能串用。
package mock

import (
	"context"
	"fmt"
	"sync"

	"prescreen.local/ps-59b347fb09/gateway"
)

// Call 记录一次模拟服务端调用，供测试核对实际执行的模板与参数归属。
type Call struct {
	ConnID     string
	DatabaseID string
	Kind       string // "prepare" | "exec" | "close"
	Template   string
	ServerID   uint64
	Args       []gateway.NamedArg
	PrepareSeq int // prepare 时该连接内的序号（同编号下区分新旧代）
}

// ScriptedError 是可按条件注入的错误。
type ScriptedError struct {
	msg  string
	kind string // "" | "unknown" | "schema"

	// 返回 true 的下一次匹配调用会返回该错误。
	Match func(c Call) bool
	once  bool
	fired bool
}

func NewUnknownStatementError(msg string) error { return &unknownStmtError{msg: msg} }
func NewSchemaChangedError(msg string) error    { return &schemaChangedError{msg: msg} }

type unknownStmtError struct{ msg string }

func (e *unknownStmtError) Error() string          { return e.msg }
func (e *unknownStmtError) UnknownStatement() bool { return true }

type schemaChangedError struct{ msg string }

func (e *schemaChangedError) Error() string       { return e.msg }
func (e *schemaChangedError) SchemaChanged() bool { return true }

// Server 是模拟的数据库服务端，被多条物理连接共享统计。
type Server struct {
	mu sync.Mutex

	calls []Call

	// 每次连接独立从 1 分配编号，因此不同连接会出现相同编号。
	nextIDByConn map[string]uint64
	prepareSeq   map[string]int

	// 当前仍“存活”的语句，键为 connID|serverID，值为最近一次 prepare 序号。
	live map[string]int

	errors []*ScriptedError

	// ExecGate 非空时，exec 会阻塞在 gate 上，直到 gate 被关闭，用于模拟在途执行。
	ExecGate chan struct{}
	// GateOnce 为 true 时，gate 只对第一次 exec 生效。
	GateOnce bool
	gateUsed bool

	// PrepareGate 非空时，指定连接的第一次 prepare 会阻塞直到 gate 关闭。
	PrepareGate     chan struct{}
	PrepareGateConn string
	prepareGateUsed bool
	PrepareBlocked  chan struct{}

	// ExecInterceptor 非空时，每次 exec 进入即调用（可用于阻塞模拟在途执行）。
	execInterceptor func(ctx context.Context)
	interceptorMu   sync.Mutex
	serverInflight  int
}

func NewServer() *Server {
	return &Server{
		nextIDByConn: make(map[string]uint64),
		prepareSeq:   make(map[string]int),
		live:         make(map[string]int),
	}
}

// SetExecInterceptor 注册执行拦截器，在每次 exec 进入时同步调用。
func (s *Server) SetExecInterceptor(fn func(ctx context.Context)) {
	s.interceptorMu.Lock()
	defer s.interceptorMu.Unlock()
	s.execInterceptor = fn
}

func (s *Server) runInterceptor(ctx context.Context) {
	s.interceptorMu.Lock()
	fn := s.execInterceptor
	s.interceptorMu.Unlock()
	if fn != nil {
		fn(ctx)
	}
}

// ServerInflight 返回当前阻塞/执行中的 exec 数量（测试观测用）。
func (s *Server) ServerInflight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.serverInflight
}

// InjectError 注册一个脚本错误。once=true 表示只触发一次。
func (s *Server) InjectError(match func(Call) bool, once bool, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errors = append(s.errors, &ScriptedError{msg: msg, Match: match, once: once})
}

// InjectUnknownStatement 注入一个实现了 UnknownStatement 标记的一次性错误。
func (s *Server) InjectUnknownStatement(match func(Call) bool, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errors = append(s.errors, &ScriptedError{msg: msg, kind: "unknown", Match: match, once: true})
}

func stmtKey(connID string, id uint64) string {
	return fmt.Sprintf("%s|%d", connID, id)
}

func (s *Server) takeError(c Call) error {
	for _, e := range s.errors {
		if e.once && e.fired {
			continue
		}
		if e.Match(c) {
			if e.once {
				e.fired = true
			}
			switch e.kind {
			case "unknown":
				return &unknownStmtError{msg: e.msg}
			case "schema":
				return &schemaChangedError{msg: e.msg}
			default:
				return &plainError{msg: e.msg}
			}
		}
	}
	return nil
}

type plainError struct{ msg string }

func (e *plainError) Error() string { return e.msg }

// Calls 返回调用记录的副本。
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Call, len(s.calls))
	copy(out, s.calls)
	return out
}

// CallsOfKind 按调用类型过滤记录。
func (s *Server) CallsOfKind(kind string) []Call {
	var out []Call
	for _, c := range s.Calls() {
		if c.Kind == kind {
			out = append(out, c)
		}
	}
	return out
}

// LiveCount 返回当前服务端仍存活的语句数。
func (s *Server) LiveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.live)
}

// Connection 是一条模拟物理连接。
type Connection struct {
	id         string
	databaseID string
	server     *Server
}

func NewConnection(server *Server, id, databaseID string) *Connection {
	return &Connection{id: id, databaseID: databaseID, server: server}
}

func (c *Connection) ID() string         { return c.id }
func (c *Connection) DatabaseID() string { return c.databaseID }

func (c *Connection) Prepare(ctx context.Context, template string, paramTypes []gateway.ParamType) (uint64, error) {
	s := c.server

	// 可选的 prepare 门控：模拟准备期间连接被重建。
	if s.PrepareGate != nil && c.id == s.PrepareGateConn {
		s.mu.Lock()
		useGate := !s.prepareGateUsed
		if useGate {
			s.prepareGateUsed = true
		}
		notify := s.PrepareBlocked
		s.mu.Unlock()
		if useGate {
			if notify != nil {
				close(notify)
			}
			select {
			case <-s.PrepareGate:
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}
	}

	s.mu.Lock()
	s.nextIDByConn[c.id]++
	id := s.nextIDByConn[c.id] // 每条连接都从 1 开始 -> 跨连接编号相同
	s.prepareSeq[c.id]++
	seq := s.prepareSeq[c.id]
	call := Call{ConnID: c.id, DatabaseID: c.databaseID, Kind: "prepare", Template: template, ServerID: id, PrepareSeq: seq}
	s.calls = append(s.calls, call)
	if err := s.takeError(call); err != nil {
		s.mu.Unlock()
		return 0, err
	}
	s.live[stmtKey(c.id, id)] = seq
	s.mu.Unlock()
	return id, nil
}

func (c *Connection) ExecStmt(ctx context.Context, serverStmtID uint64, args []gateway.NamedArg) (*gateway.Result, error) {
	s := c.server
	s.mu.Lock()
	s.serverInflight++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.serverInflight--
		s.mu.Unlock()
	}()

	s.runInterceptor(ctx)

	// 可选的在途阻塞：模拟执行尚未返回的时间窗。
	if s.ExecGate != nil {
		s.mu.Lock()
		useGate := !s.GateOnce || !s.gateUsed
		if useGate {
			s.gateUsed = true
		}
		s.mu.Unlock()
		if useGate {
			select {
			case <-s.ExecGate:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}

	s.mu.Lock()
	// 记录该次执行实际携带的模板（取最近一次 prepare 的模板）与参数。
	var template string
	for i := len(s.calls) - 1; i >= 0; i-- {
		if s.calls[i].Kind == "prepare" && s.calls[i].ConnID == c.id {
			template = s.calls[i].Template
			break
		}
	}
	call := Call{
		ConnID:     c.id,
		DatabaseID: c.databaseID,
		Kind:       "exec",
		Template:   template,
		ServerID:   serverStmtID,
		Args:       append([]gateway.NamedArg(nil), args...),
	}
	s.calls = append(s.calls, call)
	if err := s.takeError(call); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	// 模拟服务端校验编号：连接上不存在该存活编号即报 unknown statement。
	if _, ok := s.live[stmtKey(c.id, serverStmtID)]; !ok {
		s.mu.Unlock()
		return nil, NewUnknownStatementError(fmt.Sprintf("conn %s: unknown server statement %d", c.id, serverStmtID))
	}
	s.mu.Unlock()

	// 回显参数，方便测试核对参数归属。
	values := make([]any, len(args))
	for i, a := range args {
		values[i] = a.Value
	}
	return &gateway.Result{Rows: [][]any{values}, Affected: 1}, nil
}

func (c *Connection) CloseStmt(ctx context.Context, serverStmtID uint64) error {
	s := c.server
	s.mu.Lock()
	key := stmtKey(c.id, serverStmtID)
	seq, live := s.live[key]
	call := Call{ConnID: c.id, DatabaseID: c.databaseID, Kind: "close", ServerID: serverStmtID, PrepareSeq: seq}
	if live {
		delete(s.live, key)
	}
	s.calls = append(s.calls, call)
	if err := s.takeError(call); err != nil {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	return nil
}

var _ gateway.DriverConnection = (*Connection)(nil)
