// Package gateway 实现多租户数据库网关的预处理语句执行组件。
//
// 设计要点：
//   - 每个句柄 Entry 绑定物理连接代次、数据库身份、模板摘要与参数类型；
//   - 参数始终通过驱动的结构化绑定传递，任何失败都只返回错误，
//     组件内部不存在也不允许退回 SQL 文本拼接；
//   - 重准备采用 RCU：新执行切到新服务端语句，旧语句在最后一个在途
//     执行结束后才发送服务端 Close；
//   - 异步完成回调按代次加当前语句指针判旧，旧回调一律丢弃，
//     不能覆盖新句柄的状态。
package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// Gateway 管理一条物理连接上的全部预处理句柄。
// 物理连接重建时调用 Rebuild，使所有旧服务端编号立即失效。
type Gateway struct {
	mu sync.Mutex

	conn       DriverConnection
	connGen    uint64
	connID     string
	databaseID string
	entries    map[*Entry]struct{}

	closed bool
}

// New 创建网关。conn 是当前物理连接；databaseID 是该连接绑定的租户身份。
func New(conn DriverConnection, databaseID string) *Gateway {
	return &Gateway{
		conn:       conn,
		connGen:    1,
		connID:     conn.ID(),
		databaseID: databaseID,
		entries:    make(map[*Entry]struct{}),
	}
}

// Handle 描述一个预处理句柄的不可变绑定信息。
type Handle struct {
	DatabaseID string
	Template   string
	Digest     string
	ParamTypes []ParamType
	ConnID     string
	ConnGen    uint64
}

// Prepare 在当前连接代次上准备语句并返回句柄。
func (g *Gateway) Prepare(ctx context.Context, databaseID, template string, paramTypes []ParamType) (*Entry, error) {
	digest := DigestTemplate(template)

	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil, ErrClosed
	}
	conn := g.conn
	gen := g.connGen
	connID := g.connID
	g.mu.Unlock()

	serverID, err := conn.Prepare(ctx, template, paramTypes)
	if err != nil {
		// 准备失败：直接返回错误，绝不退化为文本执行。
		return nil, err
	}

	typesCopy := append([]ParamType(nil), paramTypes...)
	entry := &Entry{
		g:          g,
		databaseID: databaseID,
		template:   template,
		digest:     digest,
		paramTypes: typesCopy,
	}
	entry.stmt = &preparedStmt{
		entry:      entry,
		serverID:   serverID,
		connID:     connID,
		connGen:    gen,
		databaseID: databaseID,
		digest:     digest,
		createdGen: gen,
		abort:      make(chan struct{}),
	}
	g.mu.Lock()
	// 对账：准备期间若发生了连接重建，刚拿到的编号已属于旧连接。
	if g.connGen != gen || g.conn != conn {
		stale := entry.stmt
		entry.stmt = nil
		g.entries[entry] = struct{}{}
		g.mu.Unlock()
		stale.markObsolete() // 旧连接已消失，closeOnServer 会跳过服务端关闭
		return entry, nil
	}
	g.entries[entry] = struct{}{}
	g.mu.Unlock()
	return entry, nil
}

// Rebuild 用一条重建后的物理连接替换当前连接，并递增连接代次。
// 所有现存句柄上的服务端编号立即失效；旧连接上的语句无法再关闭，
// 直接废弃（在途回调会因代次不符而被丢弃）。
func (g *Gateway) Rebuild(newConn DriverConnection) {
	g.mu.Lock()
	g.conn = newConn
	g.connID = newConn.ID()
	g.databaseID = newConn.DatabaseID()
	g.connGen++
	entries := make([]*Entry, 0, len(g.entries))
	for e := range g.entries {
		entries = append(entries, e)
	}
	g.mu.Unlock()

	// 作废所有在旧连接上准备的语句并唤醒其在途执行；
	// 这些语句所属连接已不存在，closeOnServer 会跳过服务端关闭。
	for _, e := range entries {
		e.mu.Lock()
		old := e.stmt
		e.stmt = nil
		e.mu.Unlock()
		if old != nil {
			old.markObsolete()
		}
	}
}

// Close 关闭网关。后续 Prepare/Exec 返回 ErrClosed。
// 已在途的执行由其自身回调收尾，不再产生新的服务端语句关闭动作。
func (g *Gateway) Close() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
}

// CurrentGeneration 返回当前物理连接代次，供宿主观测/测试使用。
func (g *Gateway) CurrentGeneration() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.connGen
}

// DigestTemplate 计算模板摘要（SHA256 十六进制）。
func DigestTemplate(template string) string {
	sum := sha256.Sum256([]byte(template))
	return hex.EncodeToString(sum[:])
}

// Entry 是一个预处理语句句柄。
type Entry struct {
	g *Gateway

	// 不可变绑定。
	databaseID string
	template   string
	digest     string
	paramTypes []ParamType

	mu   sync.Mutex
	cond *sync.Cond

	stmt     *preparedStmt // 当前生效的服务端语句；可能为 nil（重准备间隙）
	closed   bool
	inflight int
	gen      uint64 // 句柄内语句代次，每次重准备递增
}

// preparedStmt 是某一次服务端准备的不可变记录。
type preparedStmt struct {
	entry      *Entry
	serverID   uint64
	connID     string
	connGen    uint64
	databaseID string
	digest     string
	createdGen uint64

	mu       sync.Mutex
	obsolete bool
	aborted  bool
	inflight int
	closed   bool
	abort    chan struct{}
}

// Handle 返回句柄当前的绑定快照。
func (e *Entry) Handle() Handle {
	e.mu.Lock()
	defer e.mu.Unlock()
	stmt := e.stmt
	h := Handle{
		DatabaseID: e.databaseID,
		Template:   e.template,
		Digest:     e.digest,
		ParamTypes: append([]ParamType(nil), e.paramTypes...),
	}
	if stmt != nil {
		h.ConnID = stmt.connID
		h.ConnGen = stmt.connGen
	}
	return h
}

// CurrentServerStmtID 返回当前生效语句的服务端编号（测试/观测用），无则返回 false。
func (e *Entry) CurrentServerStmtID() (uint64, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stmt == nil {
		return 0, false
	}
	return e.stmt.serverID, true
}

// Generation 返回句柄内的语句代次，重准备后递增（测试/观测用）。
func (e *Entry) Generation() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.gen
}

// validate 在执行前校验调用方身份、模板与连接代次，并检查参数数量与类型。
func (e *Entry) validate(g *Gateway, databaseID, template string, args []NamedArg) (*preparedStmt, DriverConnection, error) {
	if databaseID != e.databaseID {
		return nil, nil, ErrIdentityMismatch
	}
	if DigestTemplate(template) != e.digest {
		return nil, nil, ErrTemplateMismatch
	}
	if len(args) != len(e.paramTypes) {
		return nil, nil, &argCountError{want: len(e.paramTypes), got: len(args)}
	}
	for i, arg := range args {
		if arg.Ordinal != i+1 {
			return nil, nil, &argTypeError{ordinal: i + 1, want: e.paramTypes[i], got: "bad-ordinal"}
		}
		if arg.Type != e.paramTypes[i] {
			return nil, nil, &argTypeError{ordinal: i + 1, want: e.paramTypes[i], got: arg.Type}
		}
	}

	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil, nil, ErrClosed
	}
	conn := g.conn
	connGen := g.connGen
	g.mu.Unlock()

	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, nil, ErrClosed
	}
	stmt := e.stmt
	e.mu.Unlock()

	if stmt == nil || stmt.connGen != connGen || stmt.connID != conn.ID() {
		// 物理连接已重建：旧编号在新连接上无效，必须重新准备。
		return nil, conn, ErrConnectionMismatch
	}
	return stmt, conn, nil
}

// beginOp 在 stmt 上登记一个在途执行。若语句已作废/关闭则拒绝。
func (s *preparedStmt) beginOp() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.obsolete || s.closed {
		return false
	}
	s.inflight++
	return true
}

// endOp 结束一个在途执行；若语句已作废且这是最后一个在途执行，则关闭服务端语句。
func (s *preparedStmt) endOp() {
	s.mu.Lock()
	s.inflight--
	drain := s.obsolete && s.inflight == 0 && !s.closed
	s.mu.Unlock()
	if drain {
		s.closeOnServer()
	}
}

// markObsolete 把语句标记为旧代；若无在途执行立即关闭，否则等待排空。
func (s *preparedStmt) markObsolete() {
	s.mu.Lock()
	s.obsolete = true
	if !s.aborted {
		s.aborted = true
		close(s.abort)
	}
	drain := s.inflight == 0 && !s.closed
	s.mu.Unlock()
	if drain {
		s.closeOnServer()
	}
}

// closeOnServer 幂等关闭服务端语句。调用时保证没有在途执行。
func (s *preparedStmt) closeOnServer() {
	s.mu.Lock()
	if s.closed || s.inflight != 0 {
		s.mu.Unlock()
		return
	}
	s.closed = true
	if !s.aborted {
		s.aborted = true
		close(s.abort)
	}
	conn := s.entry.g.currentConnFor(s.connGen)
	serverID := s.serverID
	s.mu.Unlock()

	if conn == nil {
		// 所属物理连接已重建，编号随连接消失，无法也无需再关闭。
		return
	}
	_ = conn.CloseStmt(context.Background(), serverID)
}

// currentConnFor 返回指定代次的连接；代次已过期则返回 nil。
func (g *Gateway) currentConnFor(gen uint64) DriverConnection {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.connGen != gen {
		return nil
	}
	return g.conn
}

// reprepare 在当前连接上重新准备，并把旧语句以 RCU 方式替换掉。
func (e *Entry) reprepare(ctx context.Context) (*preparedStmt, DriverConnection, error) {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, nil, ErrClosed
	}
	old := e.stmt

	gw := e.g
	gw.mu.Lock()
	if gw.closed {
		gw.mu.Unlock()
		e.mu.Unlock()
		return nil, nil, ErrClosed
	}
	conn := gw.conn
	gen := gw.connGen
	connID := gw.connID
	gw.mu.Unlock()
	e.mu.Unlock()

	serverID, err := conn.Prepare(ctx, e.template, e.paramTypes)
	if err != nil {
		return nil, nil, err
	}

	stmt := &preparedStmt{
		entry:      e,
		serverID:   serverID,
		connID:     connID,
		connGen:    gen,
		databaseID: e.databaseID,
		digest:     e.digest,
		createdGen: gen,
		abort:      make(chan struct{}),
	}

	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		// 句柄在准备期间被关闭：立刻回收刚准备的语句。
		stmt.markObsolete()
		return nil, nil, ErrClosed
	}
	// 准备期间若连接被重建，新编号属于旧连接，作废并返回连接错配，
	// 让上层在最新连接上重试。
	if e.g.CurrentGeneration() != gen {
		e.mu.Unlock()
		stmt.markObsolete()
		if old != nil {
			old.markObsolete()
		}
		return nil, nil, ErrConnectionMismatch
	}
	e.stmt = stmt
	e.gen++
	e.mu.Unlock()

	if old != nil {
		old.markObsolete()
	}
	return stmt, conn, nil
}

// Reprepare 在当前物理连接上显式重新准备语句（例如宿主感知到模式变化）。
// 旧语句以 RCU 方式替换，并在在途执行排空后关闭。
func (e *Entry) Reprepare(ctx context.Context) error {
	_, _, err := e.reprepare(ctx)
	return err
}

// Close 关闭句柄。当前语句在最后一个在途执行结束后才在服务端关闭；
// 重复关闭返回 ErrClosed（第一次关闭生效）。
func (e *Entry) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return ErrClosed
	}
	e.closed = true
	stmt := e.stmt
	e.stmt = nil
	e.mu.Unlock()

	if stmt != nil {
		stmt.markObsolete()
	}
	return nil
}

// WaitDrained 等待句柄上所有在途执行结束（测试/优雅停机用）。
func (e *Entry) WaitDrained() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for e.inflight != 0 {
		if e.cond == nil {
			e.cond = sync.NewCond(&e.mu)
		}
		e.cond.Wait()
	}
}

func (e *Entry) enterInflight() {
	e.mu.Lock()
	e.inflight++
	e.mu.Unlock()
}

func (e *Entry) leaveInflight() {
	e.mu.Lock()
	e.inflight--
	if e.inflight == 0 && e.cond != nil {
		e.cond.Broadcast()
	}
	e.mu.Unlock()
}
