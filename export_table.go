package netsec

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
)

// ConnectionID identifies a single Cap'n Proto RPC connection. Export IDs are
// only meaningful within the connection whose table created them.
type ConnectionID = string

// MethodID is an interface method ordinal as carried by a Cap'n Proto Call.
type MethodID uint16

// ExportID is a connection-local capability handle. ID zero is never assigned.
type ExportID uint32

// MaxExportID is the largest export ID that can be represented on the wire.
const MaxExportID ExportID = ^ExportID(0)

var (
	// ErrUnknownExport means the target is not in this connection's export
	// table. A dispatcher must tear down the connection when it sees this
	// error: the peer is using a handle it was never given.
	ErrUnknownExport = errors.New("netsec: target is not in this connection's export table")

	// ErrConnectionMismatch means a dispatcher routed a call through a table
	// belonging to another connection.
	ErrConnectionMismatch = errors.New("netsec: export table belongs to a different connection")

	// ErrReleaseOverflow means a Release asked to drop more references than
	// the export currently holds. The table is left unchanged.
	ErrReleaseOverflow = errors.New("netsec: release count exceeds export reference count")

	// ErrInvalidReleaseCount means a Release carried a zero reference count.
	ErrInvalidReleaseCount = errors.New("netsec: release count must be positive")

	// ErrReferenceCountOverflow means retaining an export would overflow its
	// reference counter.
	ErrReferenceCountOverflow = errors.New("netsec: export reference count overflow")

	// ErrMethodNotAllowed means the method is outside the whitelist attached
	// to this particular caller's attenuated view.
	ErrMethodNotAllowed = errors.New("netsec: method is not in the caller's whitelist")

	// ErrTableClosed means the connection (and therefore the table) is gone.
	ErrTableClosed = errors.New("netsec: export table is closed")

	// ErrExportIDExhausted means every export ID has been assigned and none
	// has been confirmed as safe to reuse.
	ErrExportIDExhausted = errors.New("netsec: export id space exhausted")

	// ErrNoPendingRelease means the peer tried to confirm an ID that was not
	// released or had already been confirmed.
	ErrNoPendingRelease = errors.New("netsec: no pending release for export id")

	// ErrNilCapability means an attempt was made to export a nil target.
	ErrNilCapability = errors.New("netsec: cannot export a nil capability")
)

// Compatibility aliases for callers using the wording from the RPC protocol
// rather than this package.
var (
	ErrUnknownTarget                = ErrUnknownExport
	ErrTargetNotFound               = ErrUnknownExport
	ErrReleaseExceedsReferenceCount = ErrReleaseOverflow
	ErrMethodNotWhitelisted         = ErrMethodNotAllowed
)

// Capability is the target of an exported RPC method. The export table does
// not interpret params/results; it only enforces access before handing a call
// to the target.
type Capability interface {
	Call(ctx context.Context, method MethodID, params []byte) ([]byte, error)
}

// CapabilityFunc adapts a function to Capability.
type CapabilityFunc func(ctx context.Context, method MethodID, params []byte) ([]byte, error)

// Call invokes f.
func (f CapabilityFunc) Call(ctx context.Context, method MethodID, params []byte) ([]byte, error) {
	if f == nil {
		return nil, ErrNilCapability
	}
	return f(ctx, method, params)
}

// Export is an immutable, caller-specific snapshot of an exported capability.
// Calling it re-checks the whitelist so a dispatcher cannot accidentally turn
// an attenuated view back into the unrestricted target.
type Export struct {
	ID       ExportID
	RefCount uint32
	// Methods and AllMethods are informational snapshots for auditing. The
	// enforcement check below uses an immutable, unexported copy so callers
	// cannot widen the view by editing these fields.
	Methods    []MethodID
	AllMethods bool

	target  Capability
	allowed map[MethodID]struct{}
	all     bool
	table   *ExportTable
}

// Allows reports whether method is visible through this attenuated view.
func (e *Export) Allows(method MethodID) bool {
	if e == nil || e.target == nil {
		return false
	}
	if e.all {
		return true
	}
	_, ok := e.allowed[method]
	return ok
}

// Call invokes the exported target after checking the caller's whitelist.
func (e *Export) Call(ctx context.Context, method MethodID, params []byte) ([]byte, error) {
	if e == nil || e.target == nil {
		return nil, fmt.Errorf("%w: empty export view", ErrUnknownExport)
	}
	if e.table != nil && e.table.IsClosed() {
		return nil, ErrTableClosed
	}
	if !e.Allows(method) {
		return nil, fmt.Errorf("%w: method %d on export %d", ErrMethodNotAllowed, method, e.ID)
	}
	return e.target.Call(ctx, method, params)
}

// MethodWhitelist returns a copy of the methods visible through the view. It
// returns nil when the view is unrestricted.
func (e *Export) MethodWhitelist() []MethodID {
	if e == nil || e.all {
		return nil
	}
	out := make([]MethodID, 0, len(e.allowed))
	for method := range e.allowed {
		out = append(out, method)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

type exportRecord struct {
	id      ExportID
	owner   ConnectionID
	target  Capability
	methods []MethodID
	allowed map[MethodID]struct{}
	all     bool
	refs    uint32
}

type pendingRelease struct {
	id     ExportID
	owner  ConnectionID
	atRefs uint32
}

// ExportTable is a per-connection capability export table.
//
// A table must never be shared between connections. Each exported capability
// is visible only through the table that created it, and every release is
// accounted against that table's reference count.
type ExportTable struct {
	connectionID ConnectionID

	mu      sync.RWMutex
	nextID  ExportID
	entries map[ExportID]*exportRecord
	pending map[ExportID]*pendingRelease
	free    []ExportID
	closed  bool
}

// NewExportTable creates an empty table for one connection. Supplying a
// ConnectionID lets DispatchFrom reject calls accidentally routed to the wrong
// connection; omitting it still creates an independent table.
func NewExportTable(connectionID ...ConnectionID) *ExportTable {
	var id ConnectionID
	if len(connectionID) > 0 {
		id = connectionID[0]
	}
	return &ExportTable{
		connectionID: id,
		entries:      make(map[ExportID]*exportRecord),
		pending:      make(map[ExportID]*pendingRelease),
	}
}

// ConnectionID returns the connection this table is bound to.
func (t *ExportTable) ConnectionID() ConnectionID {
	if t == nil {
		return ""
	}
	return t.connectionID
}

// Export adds cap to the table with a deny-by-default method whitelist and
// returns a new connection-local export ID. An empty whitelist exposes no
// methods; use ExportAll to expose every method.
func (t *ExportTable) Export(cap Capability, methods ...MethodID) (ExportID, error) {
	return t.export(cap, methods, false)
}

// Add is an alias for Export.
func (t *ExportTable) Add(cap Capability, methods ...MethodID) (ExportID, error) {
	return t.Export(cap, methods...)
}

// ExportAll adds cap and exposes every method to this caller.
func (t *ExportTable) ExportAll(cap Capability) (ExportID, error) {
	return t.export(cap, nil, true)
}

// ExportFunc adds a function-backed capability.
func (t *ExportTable) ExportFunc(fn func(context.Context, MethodID, []byte) ([]byte, error), methods ...MethodID) (ExportID, error) {
	if fn == nil {
		return 0, ErrNilCapability
	}
	return t.Export(CapabilityFunc(fn), methods...)
}

func (t *ExportTable) export(cap Capability, methods []MethodID, all bool) (ExportID, error) {
	if t == nil {
		return 0, ErrTableClosed
	}
	if isNilCapability(cap) {
		return 0, ErrNilCapability
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return 0, ErrTableClosed
	}
	if t.entries == nil {
		t.entries = make(map[ExportID]*exportRecord)
	}
	if t.pending == nil {
		t.pending = make(map[ExportID]*pendingRelease)
	}

	id, err := t.allocateID()
	if err != nil {
		return 0, err
	}

	allowed := make(map[MethodID]struct{}, len(methods))
	copied := make([]MethodID, 0, len(methods))
	seen := make(map[MethodID]struct{}, len(methods))
	for _, method := range methods {
		if _, ok := seen[method]; ok {
			continue
		}
		seen[method] = struct{}{}
		allowed[method] = struct{}{}
		copied = append(copied, method)
	}
	sort.Slice(copied, func(i, j int) bool { return copied[i] < copied[j] })

	t.entries[id] = &exportRecord{
		id:      id,
		owner:   t.connectionID,
		target:  cap,
		methods: copied,
		allowed: allowed,
		all:     all,
		refs:    1,
	}
	return id, nil
}

func (t *ExportTable) allocateID() (ExportID, error) {
	if len(t.free) > 0 {
		id := t.free[0]
		copy(t.free, t.free[1:])
		t.free = t.free[:len(t.free)-1]
		return id, nil
	}
	if t.nextID >= MaxExportID {
		return 0, ErrExportIDExhausted
	}
	t.nextID++
	return t.nextID, nil
}

// Lookup returns a snapshot of an active export without checking a method.
func (t *ExportTable) Lookup(id ExportID) (*Export, error) {
	if t == nil {
		return nil, ErrTableClosed
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.closed {
		return nil, ErrTableClosed
	}
	rec, ok := t.entries[id]
	if !ok {
		_, released := t.pending[id]
		return nil, unknownExportError(id, t.connectionID, released)
	}
	return rec.snapshot(t), nil
}

// Resolve returns the caller-visible view for id after checking the method
// whitelist. An unknown or already-released ID is a connection-fatal error.
func (t *ExportTable) Resolve(id ExportID, method MethodID) (*Export, error) {
	return t.resolve(t.connectionID, id, method)
}

// ResolveFrom additionally verifies that the dispatcher is using the table
// belonging to connID. A mismatch is treated as a forged cross-connection
// reference and must disconnect the peer.
func (t *ExportTable) ResolveFrom(connID ConnectionID, id ExportID, method MethodID) (*Export, error) {
	return t.resolve(connID, id, method)
}

func (t *ExportTable) resolve(connID ConnectionID, id ExportID, method MethodID) (*Export, error) {
	if t == nil {
		return nil, ErrTableClosed
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.closed {
		return nil, ErrTableClosed
	}
	if t.connectionID != "" && connID != t.connectionID {
		return nil, connectionMismatchError(connID, t.connectionID, id)
	}

	rec, ok := t.entries[id]
	if !ok {
		_, released := t.pending[id]
		return nil, unknownExportError(id, t.connectionID, released)
	}
	if rec.owner != "" && rec.owner != connID {
		return nil, connectionMismatchError(connID, rec.owner, id)
	}
	if !rec.allows(method) {
		return nil, fmt.Errorf("%w: method %d on export %d", ErrMethodNotAllowed, method, id)
	}
	return rec.snapshot(t), nil
}

func (r *exportRecord) allows(method MethodID) bool {
	if r.all {
		return true
	}
	_, ok := r.allowed[method]
	return ok
}

func (r *exportRecord) snapshot(table *ExportTable) *Export {
	methods := make([]MethodID, len(r.methods))
	copy(methods, r.methods)
	allowed := make(map[MethodID]struct{}, len(r.allowed))
	for method := range r.allowed {
		allowed[method] = struct{}{}
	}
	return &Export{
		ID:         r.id,
		RefCount:   r.refs,
		Methods:    methods,
		AllMethods: r.all,
		allowed:    allowed,
		all:        r.all,
		target:     r.target,
		table:      table,
	}
}

// Retain records that another reference to the same export ID has been
// introduced to the peer. Releases must account for all retained references.
func (t *ExportTable) Retain(id ExportID, count uint32) error {
	if t == nil {
		return ErrTableClosed
	}
	if count == 0 {
		return fmt.Errorf("%w: retain count is zero", ErrInvalidReleaseCount)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrTableClosed
	}
	rec, ok := t.entries[id]
	if !ok {
		_, released := t.pending[id]
		return unknownExportError(id, t.connectionID, released)
	}
	if rec.refs > ^uint32(0)-count {
		return fmt.Errorf("%w: export %d holds %d", ErrReferenceCountOverflow, id, rec.refs)
	}
	rec.refs += count
	return nil
}

// AddRef is an alias for Retain.
func (t *ExportTable) AddRef(id ExportID, count uint32) error {
	return t.Retain(id, count)
}

// Release drops count references from the export. If count exceeds the
// recorded reference count the release is rejected atomically and the export
// remains usable. The counter can therefore never go negative.
//
// When the final reference is released, the ID is removed from active lookup
// but is kept reserved until ConfirmRelease is called. This prevents an
// attacker from making a call hit a newly exported capability that reused the
// old numeric ID before the peer has confirmed that the old reference is gone.
func (t *ExportTable) Release(id ExportID, count uint32) error {
	_, err := t.ReleaseAndRemaining(id, count)
	return err
}

// ReleaseAndRemaining is Release with the remaining reference count.
func (t *ExportTable) ReleaseAndRemaining(id ExportID, count uint32) (uint32, error) {
	if t == nil {
		return 0, ErrTableClosed
	}
	if count == 0 {
		return 0, fmt.Errorf("%w: export %d", ErrInvalidReleaseCount, id)
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return 0, ErrTableClosed
	}

	rec, ok := t.entries[id]
	if !ok {
		if _, released := t.pending[id]; released {
			// The export is already at zero. Treat the duplicate as an excess
			// release rather than allowing it to wrap or resurrect the entry.
			return 0, releaseOverflowError(id, 0, count, t.connectionID)
		}
		return 0, unknownExportError(id, t.connectionID, false)
	}

	if count > rec.refs {
		return rec.refs, releaseOverflowError(id, rec.refs, count, t.connectionID)
	}

	rec.refs -= count
	if rec.refs != 0 {
		return rec.refs, nil
	}

	delete(t.entries, id)
	if t.pending == nil {
		t.pending = make(map[ExportID]*pendingRelease)
	}
	t.pending[id] = &pendingRelease{
		id:     id,
		owner:  rec.owner,
		atRefs: count,
	}
	return 0, nil
}

// Decref is an alias for Release.
func (t *ExportTable) Decref(id ExportID, count uint32) error {
	return t.Release(id, count)
}

// ConfirmRelease notifies the table that the peer has acknowledged that it
// will send no further calls for id. Only then may the numeric ID be reused.
// Calling this before a release (or twice) is rejected.
func (t *ExportTable) ConfirmRelease(id ExportID) error {
	if t == nil {
		return ErrTableClosed
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrTableClosed
	}
	if _, active := t.entries[id]; active {
		return fmt.Errorf("%w: export %d is still active", ErrNoPendingRelease, id)
	}
	if _, ok := t.pending[id]; !ok {
		return fmt.Errorf("%w: export %d", ErrNoPendingRelease, id)
	}
	delete(t.pending, id)
	t.free = append(t.free, id)
	// Keep the free list ordered so reused IDs are deterministic and cheap to
	// audit.
	if len(t.free) > 1 && t.free[len(t.free)-1] < t.free[len(t.free)-2] {
		sort.Slice(t.free, func(i, j int) bool { return t.free[i] < t.free[j] })
	}
	return nil
}

// AcknowledgeRelease is an alias for ConfirmRelease.
func (t *ExportTable) AcknowledgeRelease(id ExportID) error {
	return t.ConfirmRelease(id)
}

// Invoke is the call path used by a message dispatcher. It enforces the
// connection's target table and the method whitelist before invoking the
// target.
func (t *ExportTable) Invoke(ctx context.Context, id ExportID, method MethodID, params []byte) ([]byte, error) {
	view, err := t.Resolve(id, method)
	if err != nil {
		if IsDisconnectRequired(err) {
			t.Close()
		}
		return nil, err
	}
	return view.Call(ctx, method, params)
}

// InvokeFrom is Invoke with an explicit dispatcher connection binding.
func (t *ExportTable) InvokeFrom(ctx context.Context, connID ConnectionID, id ExportID, method MethodID, params []byte) ([]byte, error) {
	view, err := t.ResolveFrom(connID, id, method)
	if err != nil {
		if IsDisconnectRequired(err) {
			t.Close()
		}
		return nil, err
	}
	return view.Call(ctx, method, params)
}

// Call is the dispatcher-facing call description.
type Call struct {
	Target ExportID
	Method MethodID
	Params []byte
}

// Dispatch resolves and invokes a Call.
func (t *ExportTable) Dispatch(ctx context.Context, call Call) ([]byte, error) {
	return t.Invoke(ctx, call.Target, call.Method, call.Params)
}

// DispatchFrom is Dispatch with an explicit connection binding.
func (t *ExportTable) DispatchFrom(ctx context.Context, connID ConnectionID, call Call) ([]byte, error) {
	return t.InvokeFrom(ctx, connID, call.Target, call.Method, call.Params)
}

// Close disconnects the table. All exports become unavailable, matching the
// Cap'n Proto rule that all capabilities served by a connection are
// disconnected when the connection goes away.
func (t *ExportTable) Close() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	t.entries = nil
	t.pending = nil
	t.free = nil
}

// Disconnect is an alias for Close.
func (t *ExportTable) Disconnect() {
	t.Close()
}

// IsClosed reports whether the connection-side table has been disconnected.
func (t *ExportTable) IsClosed() bool {
	if t == nil {
		return true
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.closed
}

// RefCount returns the current reference count for an active export.
func (t *ExportTable) RefCount(id ExportID) (uint32, error) {
	if t == nil {
		return 0, ErrTableClosed
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.closed {
		return 0, ErrTableClosed
	}
	rec, ok := t.entries[id]
	if !ok {
		_, released := t.pending[id]
		return 0, unknownExportError(id, t.connectionID, released)
	}
	return rec.refs, nil
}

// IsReleased reports whether id has reached zero references and is still
// reserved awaiting peer confirmation.
func (t *ExportTable) IsReleased(id ExportID) bool {
	if t == nil {
		return false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.pending[id]
	return ok
}

// PendingReleaseIDs returns the IDs awaiting peer confirmation.
func (t *ExportTable) PendingReleaseIDs() []ExportID {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	ids := make([]ExportID, 0, len(t.pending))
	for id := range t.pending {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// IsDisconnectRequired reports whether err means the dispatcher must close the
// connection. Only forged/missing targets and connection mismatches are fatal;
// an over-release or a denied method leaves the connection alive so the call
// can simply be rejected.
func IsDisconnectRequired(err error) bool {
	return errors.Is(err, ErrUnknownExport) || errors.Is(err, ErrConnectionMismatch)
}

// RequiresDisconnect is an alias for IsDisconnectRequired.
func RequiresDisconnect(err error) bool {
	return IsDisconnectRequired(err)
}

type tableError struct {
	kind      error
	id        ExportID
	owner     ConnectionID
	held      uint32
	requested uint32
	released  bool
	extra     string
}

func (e *tableError) Error() string {
	var state string
	if e.released {
		state = " (already released)"
	}
	var detail string
	if e.held != 0 || e.requested != 0 {
		detail = fmt.Sprintf(" (holds %d, requested %d)", e.held, e.requested)
	}
	return fmt.Sprintf("%v: export %d on connection %q%s%s%s", e.kind, e.id, e.owner, state, detail, e.extra)
}

func (e *tableError) Unwrap() error { return e.kind }

func unknownExportError(id ExportID, owner ConnectionID, released bool) error {
	return &tableError{kind: ErrUnknownExport, id: id, owner: owner, released: released}
}

func connectionMismatchError(got, want ConnectionID, id ExportID) error {
	return &tableError{
		kind:  ErrConnectionMismatch,
		id:    id,
		owner: got,
		extra: fmt.Sprintf(" (table belongs to %q)", want),
	}
}

func releaseOverflowError(id ExportID, held, requested uint32, owner ConnectionID) error {
	return &tableError{
		kind:      ErrReleaseOverflow,
		id:        id,
		owner:     owner,
		held:      held,
		requested: requested,
	}
}

// AttenuatedCapability exposes only the listed methods of another capability.
// Whitelists compose: wrapping an already-attenuated capability can only
// narrow access, never widen it.
type AttenuatedCapability struct {
	target  Capability
	allowed map[MethodID]struct{}
}

// Attenuate wraps target with a deny-by-default method whitelist.
func Attenuate(target Capability, methods ...MethodID) Capability {
	if isNilCapability(target) {
		return &AttenuatedCapability{}
	}
	allowed := make(map[MethodID]struct{}, len(methods))
	for _, method := range methods {
		allowed[method] = struct{}{}
	}
	return &AttenuatedCapability{target: target, allowed: allowed}
}

// Call checks the whitelist before delegating to the wrapped target.
func (a *AttenuatedCapability) Call(ctx context.Context, method MethodID, params []byte) ([]byte, error) {
	if a == nil || a.target == nil {
		return nil, fmt.Errorf("%w: empty attenuated capability", ErrUnknownExport)
	}
	if _, ok := a.allowed[method]; !ok {
		return nil, fmt.Errorf("%w: method %d", ErrMethodNotAllowed, method)
	}
	return a.target.Call(ctx, method, params)
}

// Methods returns the methods exposed by this view.
func (a *AttenuatedCapability) Methods() []MethodID {
	if a == nil {
		return nil
	}
	methods := make([]MethodID, 0, len(a.allowed))
	for method := range a.allowed {
		methods = append(methods, method)
	}
	sort.Slice(methods, func(i, j int) bool { return methods[i] < methods[j] })
	return methods
}

func isNilCapability(cap Capability) bool {
	if cap == nil {
		return true
	}
	value := reflect.ValueOf(cap)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
