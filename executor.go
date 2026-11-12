package netsec

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// handleKey identifies a cached statement handle. The server-side
// statement number is never part of the key: it is only meaningful
// together with the physical connection generation that issued it.
type handleKey struct {
	database string
	digest   [32]byte
	sig      string
}

func keyFor(database string, t Template) handleKey {
	return handleKey{database: database, digest: t.Digest, sig: paramSig(t.ParamTypes)}
}

func paramSig(types []ParamType) string {
	b := make([]byte, len(types))
	for i, t := range types {
		b[i] = byte(t)
	}
	return string(b)
}

// binding pins one server-side statement ID to one physical connection
// generation, database identity and schema version. Executions hold an
// in-flight reference; the server-side statement is closed only after the
// last in-flight execution on a retired binding finishes.
type binding struct {
	conn       *PConn
	serverID   uint64
	database   string
	generation uint64
	schema     uint64
	inflight   sync.WaitGroup
}

func (b *binding) matches(pc *PConn, database string) bool {
	return b.conn == pc &&
		b.generation == pc.Generation &&
		b.database == database &&
		b.schema == pc.SchemaVersion
}

// retire closes the server-side statement once the last in-flight
// execution on this binding has finished.
func (b *binding) retire() {
	b.inflight.Wait()
	_ = b.conn.Conn.CloseStmt(context.Background(), b.serverID)
}

// handle is the cached prepared-statement slot for one handleKey.
type handle struct {
	key  handleKey
	tmpl Template

	mu     sync.Mutex
	epoch  uint64 // bumped every time a (re)prepare starts
	cur    *binding
	closed bool
}

func (h *handle) execute(ctx context.Context, src Source, args []Value) (Result, error) {
	b, err := h.bind(ctx, src)
	if err != nil {
		return Result{}, err
	}
	defer b.inflight.Done()
	return b.conn.Conn.Execute(ctx, b.serverID, args)
}

// bind returns a binding that is valid for the connection the pool hands
// out right now, re-preparing when the cached binding is stale.
func (h *handle) bind(ctx context.Context, src Source) (*binding, error) {
	for {
		pc, err := src.Acquire(ctx, h.key.database)
		if err != nil {
			return nil, err
		}
		if pc.Database != h.key.database {
			return nil, fmt.Errorf("%w: handle wants %q, connection is %q",
				ErrDatabaseMismatch, h.key.database, pc.Database)
		}

		h.mu.Lock()
		if h.closed {
			h.mu.Unlock()
			return nil, ErrClosed
		}
		if h.cur != nil && h.cur.matches(pc, h.key.database) {
			b := h.cur
			b.inflight.Add(1)
			h.mu.Unlock()
			return b, nil
		}
		epoch := h.epoch + 1
		h.epoch = epoch
		h.mu.Unlock()

		// Network prepare without holding the handle lock, so Close and
		// concurrent re-prepares are not blocked on I/O.
		id, err := pc.Conn.Prepare(ctx, h.tmpl.SQL, h.tmpl.ParamTypes)
		if err != nil {
			// Hard failure. There is no text-SQL fallback by design.
			return nil, err
		}

		h.mu.Lock()
		stale := h.closed || h.epoch != epoch
		closed := h.closed
		if !stale {
			nb := &binding{
				conn:       pc,
				serverID:   id,
				database:   h.key.database,
				generation: pc.Generation,
				schema:     pc.SchemaVersion,
			}
			nb.inflight.Add(1)
			old := h.cur
			h.cur = nb
			h.mu.Unlock()
			if old != nil {
				go old.retire()
			}
			return nb, nil
		}
		h.mu.Unlock()

		// A newer (re)prepare or a Close won the race while we were in
		// Prepare: this callback is stale and must not overwrite the
		// current binding. Dispose of the orphaned server-side statement
		// on the connection that issued it.
		_ = pc.Conn.CloseStmt(context.Background(), id)
		if closed {
			return nil, ErrClosed
		}
	}
}

// close marks the handle closed and closes the server-side statement only
// after the last in-flight execution finishes.
func (h *handle) close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	b := h.cur
	h.cur = nil
	h.mu.Unlock()
	if b != nil {
		b.inflight.Wait()
		_ = b.conn.Conn.CloseStmt(context.Background(), b.serverID)
	}
}

// Executor is the component the Go query host calls. It caches one handle
// per (database identity, template digest, parameter signature) and binds
// every execution to the current physical connection generation.
type Executor struct {
	src Source

	mu      sync.Mutex
	handles map[handleKey]*handle
}

func NewExecutor(src Source) *Executor {
	return &Executor{src: src, handles: make(map[handleKey]*handle)}
}

// Execute validates the arguments against the template signature, resolves
// (or re-prepares) the server-side statement for the current connection
// generation, and runs it with structured binds. Any failure — validation,
// prepare, execute, cancellation — is returned to the caller as-is; the
// executor never falls back to text SQL.
func (e *Executor) Execute(ctx context.Context, database string, tmpl Template, args ...Value) (Result, error) {
	if err := tmpl.ValidateArgs(args); err != nil {
		return Result{}, err
	}
	for {
		h := e.handleFor(database, tmpl)
		res, err := h.execute(ctx, e.src, args)
		if errors.Is(err, ErrClosed) {
			// Raced with CloseHandle: retry on a fresh handle.
			continue
		}
		return res, err
	}
}

func (e *Executor) handleFor(database string, t Template) *handle {
	k := keyFor(database, t)
	e.mu.Lock()
	defer e.mu.Unlock()
	if h, ok := e.handles[k]; ok {
		return h
	}
	h := &handle{key: k, tmpl: t}
	e.handles[k] = h
	return h
}

// CloseHandle closes the cached handle for the template, waiting for the
// last in-flight execution before closing the server-side statement.
func (e *Executor) CloseHandle(database string, t Template) {
	k := keyFor(database, t)
	e.mu.Lock()
	h, ok := e.handles[k]
	if ok {
		delete(e.handles, k)
	}
	e.mu.Unlock()
	if ok {
		h.close()
	}
}

// Close closes every cached handle, draining in-flight executions first.
func (e *Executor) Close() {
	e.mu.Lock()
	hs := make([]*handle, 0, len(e.handles))
	for k, h := range e.handles {
		hs = append(hs, h)
		delete(e.handles, k)
	}
	e.mu.Unlock()
	for _, h := range hs {
		h.close()
	}
}
