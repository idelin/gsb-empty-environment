package netsec

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// execRecord captures what the server would actually have run: the SQL
// text the connection itself has registered under the given statement ID,
// plus the structured bind values that arrived with it.
type execRecord struct {
	ServerID uint64
	SQL      string
	Args     []Value
}

// mockConn simulates one physical connection. Every Prepare returns the
// same fixed statement ID, reproducing the cross-connection ID collision
// that makes ID reuse across connections dangerous.
type mockConn struct {
	name     string
	fixedID  uint64
	mu       sync.Mutex
	prepared map[uint64]string
	execLog  []execRecord
	closeLog []uint64
	prepCnt  int

	// prepareHook runs inside Prepare before the ID is handed out.
	prepareHook func()
	// execHook runs inside Execute; returning an error aborts the call.
	execHook func(ctx context.Context) error
}

func newMockConn(name string, fixedID uint64) *mockConn {
	return &mockConn{name: name, fixedID: fixedID, prepared: map[uint64]string{}}
}

func (c *mockConn) Prepare(ctx context.Context, sql string, _ []ParamType) (uint64, error) {
	if c.prepareHook != nil {
		c.prepareHook()
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prepCnt++
	c.prepared[c.fixedID] = sql
	return c.fixedID, nil
}

func (c *mockConn) Execute(ctx context.Context, id uint64, args []Value) (Result, error) {
	if c.execHook != nil {
		if err := c.execHook(ctx); err != nil {
			return Result{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	sql, ok := c.prepared[id]
	if !ok {
		return Result{}, errors.New("unknown statement id on " + c.name)
	}
	c.execLog = append(c.execLog, execRecord{
		ServerID: id,
		SQL:      sql,
		Args:     append([]Value(nil), args...),
	})
	return Result{RowsAffected: 1}, nil
}

func (c *mockConn) CloseStmt(_ context.Context, id uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeLog = append(c.closeLog, id)
	return nil
}

func (c *mockConn) stats() (prep, exec int, closes []uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.prepCnt, len(c.execLog), append([]uint64(nil), c.closeLog...)
}

func (c *mockConn) execs() []execRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]execRecord(nil), c.execLog...)
}

// mockPool hands out the current physical connection and lets the test
// rebuild it (new generation), switch its database identity, or bump its
// schema version.
type mockPool struct {
	mu  sync.Mutex
	cur *PConn
}

func newMockPool(conn Conn, database string) *mockPool {
	return &mockPool{cur: &PConn{Conn: conn, Generation: 1, Database: database, SchemaVersion: 1}}
}

func (p *mockPool) Acquire(ctx context.Context, _ string) (*PConn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cur, nil
}

// rebuild replaces the physical connection, bumping the generation.
func (p *mockPool) rebuild(conn Conn, database string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cur = &PConn{
		Conn:          conn,
		Generation:    p.cur.Generation + 1,
		Database:      database,
		SchemaVersion: p.cur.SchemaVersion,
	}
}

// setDatabase flips the session's database identity in place, as happens
// when another tenant's session reused the physical connection.
func (p *mockPool) setDatabase(database string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cur.Database = database
}

// bumpSchema simulates a DDL / mode change on the current connection.
func (p *mockPool) bumpSchema() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cur.SchemaVersion++
}

var ordersTmpl = NewTemplate(
	"SELECT * FROM orders WHERE tenant = ? AND id = ?",
	TypeString, TypeInt64,
)

// Two connections hand out the same server-side statement ID (7). After a
// connection rebuild the executor must re-prepare on the new connection
// instead of reusing the cached ID, and the SQL that actually runs on the
// new connection must be our template with our tenant's parameters — not
// the foreign statement that lives under the same ID there.
func TestReconnectIsolation(t *testing.T) {
	connA := newMockConn("A", 7)
	pool := newMockPool(connA, "tenant_a")
	ex := NewExecutor(pool)
	ctx := context.Background()

	args := []Value{Str("tenant_a"), Int64(42)}
	if _, err := ex.Execute(ctx, "tenant_a", ordersTmpl, args...); err != nil {
		t.Fatalf("first execute: %v", err)
	}

	// Connection B belongs to another tenant's workload: statement ID 7
	// already means something destructive there.
	connB := newMockConn("B", 7)
	connB.prepared[7] = "DELETE FROM users"
	pool.rebuild(connB, "tenant_a")

	if _, err := ex.Execute(ctx, "tenant_a", ordersTmpl, args...); err != nil {
		t.Fatalf("execute after rebuild: %v", err)
	}

	if got := connA.execs(); len(got) != 1 || got[0].SQL != ordersTmpl.SQL {
		t.Fatalf("conn A executions = %+v, want exactly one run of our template", got)
	}
	gotB := connB.execs()
	if len(gotB) != 1 {
		t.Fatalf("conn B executions = %+v, want exactly one", gotB)
	}
	if gotB[0].SQL != ordersTmpl.SQL {
		t.Fatalf("conn B executed %q; the foreign statement must never run", gotB[0].SQL)
	}
	if gotB[0].Args[0] != Str("tenant_a") || gotB[0].Args[1] != Int64(42) {
		t.Fatalf("conn B got args %+v, want our tenant's binds", gotB[0].Args)
	}
	if prep, _, _ := connB.stats(); prep != 1 {
		t.Fatalf("conn B prepares = %d, want 1 (re-prepare on new generation)", prep)
	}
}

// A schema/mode change on the same physical connection invalidates the
// cached statement: it must be re-prepared, and the old server-side
// statement must be retired.
func TestSchemaChangeRepPrepares(t *testing.T) {
	connA := newMockConn("A", 7)
	pool := newMockPool(connA, "tenant_a")
	ex := NewExecutor(pool)
	ctx := context.Background()
	args := []Value{Str("tenant_a"), Int64(1)}

	if _, err := ex.Execute(ctx, "tenant_a", ordersTmpl, args...); err != nil {
		t.Fatalf("first execute: %v", err)
	}
	pool.bumpSchema()
	if _, err := ex.Execute(ctx, "tenant_a", ordersTmpl, args...); err != nil {
		t.Fatalf("execute after schema change: %v", err)
	}

	prep, exec, closes := connA.stats()
	if prep != 2 {
		t.Fatalf("prepares = %d, want 2 (re-prepare after schema change)", prep)
	}
	if exec != 2 {
		t.Fatalf("executions = %d, want 2", exec)
	}
	waitFor(t, func() bool {
		_, _, closes = connA.stats()
		return len(closes) == 1 && closes[0] == 7
	}, "old server-side statement to be retired")
}

// If the pool hands out a connection whose session was switched to another
// tenant's database, the executor must refuse to run rather than execute
// under the wrong identity.
func TestDatabaseMismatchRefused(t *testing.T) {
	connA := newMockConn("A", 7)
	pool := newMockPool(connA, "tenant_a")
	ex := NewExecutor(pool)

	pool.setDatabase("tenant_b")
	_, err := ex.Execute(context.Background(), "tenant_a", ordersTmpl, Str("tenant_a"), Int64(1))
	if !errors.Is(err, ErrDatabaseMismatch) {
		t.Fatalf("err = %v, want ErrDatabaseMismatch", err)
	}
	if prep, exec, _ := connA.stats(); prep != 0 || exec != 0 {
		t.Fatalf("connection touched (prep=%d exec=%d) despite identity mismatch", prep, exec)
	}
}

// Cancellation propagates to the caller as-is. The handle stays valid (the
// server-side statement is not poisoned by a cancelled execution) and no
// retry or text fallback happens behind the caller's back.
func TestCancellation(t *testing.T) {
	connA := newMockConn("A", 7)
	pool := newMockPool(connA, "tenant_a")
	ex := NewExecutor(pool)
	args := []Value{Str("tenant_a"), Int64(9)}

	if _, err := ex.Execute(context.Background(), "tenant_a", ordersTmpl, args...); err != nil {
		t.Fatalf("warm-up execute: %v", err)
	}

	started := make(chan struct{})
	connA.execHook = func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := ex.Execute(ctx, "tenant_a", ordersTmpl, args...)
		errCh <- err
	}()
	<-started
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	connA.execHook = nil

	// The handle must still be usable without a re-prepare.
	if _, err := ex.Execute(context.Background(), "tenant_a", ordersTmpl, args...); err != nil {
		t.Fatalf("execute after cancel: %v", err)
	}
	if prep, exec, _ := connA.stats(); prep != 1 || exec != 2 {
		t.Fatalf("prep=%d exec=%d, want prep=1 exec=2 (no spurious re-prepare)", prep, exec)
	}
}

// Wrong argument count (and wrong types) are rejected before any network
// I/O. The connection must see neither a prepare nor an execute, and the
// executor has no text-concatenation path to fall back to.
func TestParamCountAndTypeMismatch(t *testing.T) {
	connA := newMockConn("A", 7)
	pool := newMockPool(connA, "tenant_a")
	ex := NewExecutor(pool)
	ctx := context.Background()

	_, err := ex.Execute(ctx, "tenant_a", ordersTmpl, Str("tenant_a"))
	if !errors.Is(err, ErrParamCount) {
		t.Fatalf("short args: err = %v, want ErrParamCount", err)
	}
	_, err = ex.Execute(ctx, "tenant_a", ordersTmpl, Str("tenant_a"), Int64(1), Bool(true))
	if !errors.Is(err, ErrParamCount) {
		t.Fatalf("long args: err = %v, want ErrParamCount", err)
	}
	_, err = ex.Execute(ctx, "tenant_a", ordersTmpl, Str("tenant_a"), Str("not-an-int"))
	if !errors.Is(err, ErrParamType) {
		t.Fatalf("wrong type: err = %v, want ErrParamType", err)
	}

	if prep, exec, closes := connA.stats(); prep != 0 || exec != 0 || len(closes) != 0 {
		t.Fatalf("connection touched on invalid args: prep=%d exec=%d closes=%v", prep, exec, closes)
	}
}

// Close must not send CloseStmt to the server while an execution is still
// in flight; the statement is closed only after the last in-flight
// execution finishes. A handle closed out from under a caller is retried
// transparently on a fresh handle.
func TestCloseWaitsForInflight(t *testing.T) {
	connA := newMockConn("A", 7)
	pool := newMockPool(connA, "tenant_a")
	ex := NewExecutor(pool)
	ctx := context.Background()
	args := []Value{Str("tenant_a"), Int64(5)}

	if _, err := ex.Execute(ctx, "tenant_a", ordersTmpl, args...); err != nil {
		t.Fatalf("warm-up execute: %v", err)
	}

	release := make(chan struct{})
	started := make(chan struct{})
	connA.execHook = func(context.Context) error {
		close(started)
		<-release
		return nil
	}
	execDone := make(chan error, 1)
	go func() {
		_, err := ex.Execute(ctx, "tenant_a", ordersTmpl, args...)
		execDone <- err
	}()
	<-started

	closeDone := make(chan struct{})
	go func() {
		ex.CloseHandle("tenant_a", ordersTmpl)
		close(closeDone)
	}()

	select {
	case <-closeDone:
		t.Fatal("CloseHandle returned while an execution was still in flight")
	case <-time.After(50 * time.Millisecond):
	}
	if _, _, closes := connA.stats(); len(closes) != 0 {
		t.Fatalf("CloseStmt sent to server with execution in flight: %v", closes)
	}

	close(release)
	if err := <-execDone; err != nil {
		t.Fatalf("in-flight execute: %v", err)
	}
	<-closeDone
	if _, _, closes := connA.stats(); len(closes) != 1 || closes[0] != 7 {
		t.Fatalf("closes = %v, want exactly [7] after drain", closes)
	}

	// A later execute re-prepares on a fresh handle and runs our template.
	connA.execHook = nil
	if _, err := ex.Execute(ctx, "tenant_a", ordersTmpl, args...); err != nil {
		t.Fatalf("execute after close: %v", err)
	}
	if prep, _, _ := connA.stats(); prep != 2 {
		t.Fatalf("prepares = %d, want 2 (fresh handle re-prepares)", prep)
	}
}

// A prepare that completes after a newer re-prepare has already installed
// a binding is a stale callback: it must not overwrite the current handle,
// and its orphaned server-side statement must be disposed of on the
// connection that issued it.
func TestStalePrepareCallback(t *testing.T) {
	connA := newMockConn("A", 7)
	pool := newMockPool(connA, "tenant_a")
	ex := NewExecutor(pool)
	ctx := context.Background()
	args := []Value{Str("tenant_a"), Int64(3)}

	// The first prepare on conn A blocks inside the "network" call.
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	connA.prepareHook = func() {
		once.Do(func() { close(entered) })
		<-release
	}

	slow := make(chan error, 1)
	go func() {
		_, err := ex.Execute(ctx, "tenant_a", ordersTmpl, args...)
		slow <- err
	}()
	<-entered // conn A prepare is now in flight, holding epoch 1

	// The pool rebuilds the connection; a second execute re-prepares on
	// conn B and installs the current binding.
	connB := newMockConn("B", 7)
	pool.rebuild(connB, "tenant_a")
	if _, err := ex.Execute(ctx, "tenant_a", ordersTmpl, args...); err != nil {
		t.Fatalf("execute on rebuilt connection: %v", err)
	}

	// Releasing conn A's prepare makes its callback stale.
	close(release)
	if err := <-slow; err != nil {
		t.Fatalf("stale-callback execute: %v", err)
	}

	if got := connA.execs(); len(got) != 0 {
		t.Fatalf("conn A executed %+v; the stale generation must never run", got)
	}
	if _, _, closes := connA.stats(); len(closes) != 1 || closes[0] != 7 {
		t.Fatalf("conn A closes = %v, want [7] (orphaned statement disposed)", closes)
	}
	gotB := connB.execs()
	if len(gotB) != 2 {
		t.Fatalf("conn B executions = %+v, want both executes on the new generation", gotB)
	}
	for _, rec := range gotB {
		if rec.SQL != ordersTmpl.SQL {
			t.Fatalf("conn B executed %q, want our template", rec.SQL)
		}
		if rec.Args[0] != Str("tenant_a") || rec.Args[1] != Int64(3) {
			t.Fatalf("conn B got args %+v, want our tenant's binds", rec.Args)
		}
	}
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
