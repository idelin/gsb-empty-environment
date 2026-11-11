// Package cflow implements the control-flow integrity core that an
// instrumented Go test host drives through synthetic runtime events.
//
// The component distinguishes a legitimate exceptional unwind from a
// tampered return address by maintaining, per thread:
//
//   - a shadow stack recording every function entry together with the call
//     site that produced it and the generation of the module the target
//     function belongs to;
//   - the recovery points that are currently alive on that thread.
//
// Indirect calls are only accepted when the dynamic target belongs to the
// type set allowed by the call site. When a module is unloaded its
// generation is retired, so every target resolved from an old image becomes
// invalid immediately.
package cflow

import (
	"fmt"
	"sync"
)

// Violation reasons. Each identifies the exact structural check that
// rejected an event, which is what the test host asserts on.
const (
	// ReasonUnknownFunc is reported when an event names a function that was
	// never registered.
	ReasonUnknownFunc = "unknown function"

	// ReasonUnknownCallSite is reported when an entry or indirect-call event
	// names a call site that does not exist on the caller, or uses a direct
	// site for an indirect call.
	ReasonUnknownCallSite = "unknown call site"

	// ReasonModuleUnloaded is reported when an event targets a function or
	// resolves a return frame whose module generation has been retired.
	ReasonModuleUnloaded = "module generation unloaded"

	// ReasonTypeMismatch is reported when an indirect-call target is not a
	// member of the call site's allowed type set.
	ReasonTypeMismatch = "indirect target outside allowed type set"

	// ReasonReturnEmpty is reported for a return on a thread whose shadow
	// stack is empty, i.e. the classic smashed-return-address signature.
	ReasonReturnEmpty = "return with empty shadow stack"

	// ReasonReturnMismatch is reported when the function named in a return
	// event is not the function on top of the thread's shadow stack.
	ReasonReturnMismatch = "return target does not match shadow stack top"

	// ReasonRecoveryExists is reported when a recovery point is established
	// twice while the first instance is still alive.
	ReasonRecoveryExists = "recovery point already alive"

	// ReasonRecoveryDepth is reported when a recovery point is established
	// above the current shadow stack, which can never be reached by an unwind.
	ReasonRecoveryDepth = "recovery point depth outside shadow stack"

	// ReasonRecoveryNotFound is reported for an unwind whose recovery point is
	// unknown, already dead, or owned by another thread.
	ReasonRecoveryNotFound = "recovery point not alive on this thread"

	// ReasonUnwindTop is reported when an unwind targets a recovery point
	// whose frame is already the top of the shadow stack: a real unwind must
	// pop at least one frame.
	ReasonUnwindTop = "unwind without any frame to pop"

	// ReasonUnwindMismatch is reported when the recovery point can no longer
	// be matched against the current shadow stack.
	ReasonUnwindMismatch = "recovery point does not match shadow stack"
)

// Runtime is the stateful control-flow component driven by the test host.
// All methods are safe for concurrent use; every state transition is
// serialized so that interleaved threads cannot observe half-applied events.
type Runtime struct {
	mu sync.Mutex

	// modules maps module ID to its current generation. A negative value
	// means the module is currently unloaded.
	modules map[string]int

	// funcs maps function ID to its static description.
	funcs map[string]FuncInfo

	// threads maps thread ID to its per-thread control-flow state.
	threads map[int]*threadState
}

// FuncInfo describes an instrumented function.
type FuncInfo struct {
	// ID is the unique function identifier carried by events.
	ID string

	// Module is the ID of the module this function belongs to.
	Module string

	// Type is the runtime type token the function implements. It is matched
	// against the type set of indirect call sites.
	Type string

	// CallSites describes every call instruction emitted inside the function.
	CallSites []CallSite
}

// CallSite describes one call instruction inside a function.
type CallSite struct {
	// ID identifies the call site within its containing function.
	ID int

	// Indirect marks an indirect call. A direct call site has no type set and
	// only verifies that the target generation is still loaded.
	Indirect bool

	// AllowedTypes is the set of runtime type tokens an indirect call may
	// resolve to. Ignored for direct call sites.
	AllowedTypes map[string]struct{}
}

// EventKind enumerates the events the instrumented host reports.
type EventKind int

const (
	// EventFuncEntry reports entry into FuncID via CallSite in the caller.
	// For a thread's root frame CallSite must be zero.
	EventFuncEntry EventKind = iota + 1

	// EventIndirectCall reports an indirect call at CallSite inside FuncID
	// resolving to TargetID.
	EventIndirectCall

	// EventReturn reports a normal return from FuncID.
	EventReturn

	// EventRecoveryPoint establishes recovery point RecoveryID at the current
	// shadow-stack depth on the reporting thread.
	EventRecoveryPoint

	// EventUnwind reports an exceptional unwind back to recovery point
	// RecoveryID on the reporting thread.
	EventUnwind
)

// Event is one piece of runtime instrumentation reported by the test host.
// Fields not relevant to a given Kind must be left zero-valued.
type Event struct {
	// Kind selects the transition; see the EventKind constants.
	Kind EventKind

	// Thread identifies the executing thread. State is never shared between
	// threads, which is what makes cross-thread forged events detectable.
	Thread int

	// FuncID is the function being entered, returned from, or the caller
	// containing CallSite for an indirect call.
	FuncID string

	// CallSite is the call site responsible for the transition.
	CallSite int

	// TargetID is the callee function for an indirect call.
	TargetID string

	// RecoveryID identifies a recovery point for establishment or unwind.
	RecoveryID int
}

// Violation is returned for the first event that breaks control-flow
// integrity. The runtime does not mutate any state while rejecting an event,
// so the caller can inspect the runtime as it was immediately before the
// offending event.
type Violation struct {
	// Index is the zero-based position of the rejected event inside the
	// batch handed to Process.
	Index int

	// Event is a copy of the rejected event.
	Event Event

	// Reason is one of the Reason* constants.
	Reason string

	// Detail carries human-readable diagnostic context.
	Detail string
}

func (v *Violation) Error() string {
	return fmt.Sprintf("cflow: event %d rejected (%s): %s", v.Index, v.Reason, v.Detail)
}

// Is reports identity against sentinel violations created with the Reason*
// constants, supporting errors.Is(err, ErrTypeMismatch) style checks.
func (v *Violation) Is(target error) bool {
	other, ok := target.(*Violation)
	if !ok {
		return false
	}
	return other.Reason != "" && other.Reason == v.Reason
}

// Sentinel violations so callers can match a reason with errors.Is without
// constructing a Violation themselves.
var (
	ErrUnknownFunc      = &Violation{Reason: ReasonUnknownFunc}
	ErrUnknownCallSite  = &Violation{Reason: ReasonUnknownCallSite}
	ErrModuleUnloaded   = &Violation{Reason: ReasonModuleUnloaded}
	ErrTypeMismatch     = &Violation{Reason: ReasonTypeMismatch}
	ErrReturnEmpty      = &Violation{Reason: ReasonReturnEmpty}
	ErrReturnMismatch   = &Violation{Reason: ReasonReturnMismatch}
	ErrRecoveryExists   = &Violation{Reason: ReasonRecoveryExists}
	ErrRecoveryDepth    = &Violation{Reason: ReasonRecoveryDepth}
	ErrRecoveryNotFound = &Violation{Reason: ReasonRecoveryNotFound}
	ErrUnwindTop        = &Violation{Reason: ReasonUnwindTop}
	ErrUnwindMismatch   = &Violation{Reason: ReasonUnwindMismatch}
)

// frame is one shadow-stack entry.
type frame struct {
	// funcID is the function executing in this frame.
	funcID string

	// callSite is the call site in the caller that created the frame, or
	// zero for a thread root frame.
	callSite int

	// gen is the module generation observed at entry. It pins the frame to
	// the exact module image the return address lives in.
	gen int
}

// recoveryPoint is a live exception-handling landing pad on one thread.
type recoveryPoint struct {
	id     int
	thread int

	// depth is the shadow-stack depth at which the point was established. An
	// unwind to it pops every frame above depth.
	depth int

	// alive becomes false once the frame owning the point has returned.
	alive bool
}

type threadState struct {
	stack    []frame
	recovery map[int]*recoveryPoint
}

// New creates an empty runtime.
func New() *Runtime {
	return &Runtime{
		modules: make(map[string]int),
		funcs:   make(map[string]FuncInfo),
		threads: make(map[int]*threadState),
	}
}

// RegisterModule introduces a module and returns its first generation.
// Reloading an unloaded module hands out a fresh generation, which
// invalidates every function pointer derived from the old image.
func (r *Runtime) RegisterModule(moduleID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	gen := r.modules[moduleID]
	if gen < 0 {
		r.modules[moduleID] = -gen + 1
	} else {
		r.modules[moduleID] = gen + 1
	}
	return r.modules[moduleID]
}

// UnloadModule retires the current generation of a module. Existing shadow
// frames keep their recorded generation for tamper diagnostics, but no new
// entry or indirect call may resolve into the retired image.
func (r *Runtime) UnloadModule(moduleID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if gen, ok := r.modules[moduleID]; ok && gen > 0 {
		r.modules[moduleID] = -gen
	}
}

// RegisterFunc records a function's static metadata. Re-registering a
// function replaces its metadata, mirroring a hot reload into a new module
// generation.
func (r *Runtime) RegisterFunc(info FuncInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.funcs[info.ID] = info
}

// Process applies a batch of events and stops at the first violation.
// Events before the returned index have been applied; the violating event
// and everything after it have not.
func (r *Runtime) Process(events ...Event) *Violation {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, evt := range events {
		if v := r.apply(i, evt); v != nil {
			return v
		}
	}
	return nil
}

// Step applies a single event, returning its violation if any.
func (r *Runtime) Step(evt Event) *Violation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.apply(0, evt)
}

func (r *Runtime) apply(index int, evt Event) *Violation {
	ts := r.thread(evt.Thread)

	switch evt.Kind {
	case EventFuncEntry:
		return r.applyEntry(index, evt, ts)
	case EventIndirectCall:
		return r.applyIndirect(index, evt, ts)
	case EventReturn:
		return r.applyReturn(index, evt, ts)
	case EventRecoveryPoint:
		return r.applyRecovery(index, evt, ts)
	case EventUnwind:
		return r.applyUnwind(index, evt, ts)
	default:
		return r.fail(index, evt, ReasonUnknownFunc, "unknown event kind %d", evt.Kind)
	}
}

func (r *Runtime) applyEntry(index int, evt Event, ts *threadState) *Violation {
	fn, v := r.lookupFunc(index, evt, evt.FuncID)
	if v != nil {
		return v
	}
	gen, v := r.liveGeneration(index, evt, fn.Module)
	if v != nil {
		return v
	}

	if len(ts.stack) > 0 {
		caller, ok := r.funcs[ts.stack[len(ts.stack)-1].funcID]
		if !ok {
			return r.fail(index, evt, ReasonUnknownFunc,
				"caller %q is not registered", ts.stack[len(ts.stack)-1].funcID)
		}
		if _, ok := findCallSite(caller, evt.CallSite); !ok {
			return r.fail(index, evt, ReasonUnknownCallSite,
				"caller %q has no call site %d", caller.ID, evt.CallSite)
		}
	}

	ts.stack = append(ts.stack, frame{
		funcID:   fn.ID,
		callSite: evt.CallSite,
		gen:      gen,
	})
	return nil
}

func (r *Runtime) applyIndirect(index int, evt Event, ts *threadState) *Violation {
	caller, v := r.lookupFunc(index, evt, evt.FuncID)
	if v != nil {
		return v
	}
	site, ok := findCallSite(caller, evt.CallSite)
	if !ok {
		return r.fail(index, evt, ReasonUnknownCallSite,
			"caller %q has no call site %d", caller.ID, evt.CallSite)
	}
	if !site.Indirect {
		return r.fail(index, evt, ReasonUnknownCallSite,
			"call site %d in %q is not indirect", evt.CallSite, caller.ID)
	}

	target, v := r.lookupFunc(index, evt, evt.TargetID)
	if v != nil {
		return v
	}
	if _, v := r.liveGeneration(index, evt, target.Module); v != nil {
		return v
	}
	if _, ok := site.AllowedTypes[target.Type]; !ok {
		return r.fail(index, evt, ReasonTypeMismatch,
			"call site %d in %q cannot target type %q (func %q)",
			evt.CallSite, caller.ID, target.Type, target.ID)
	}
	return nil
}

func (r *Runtime) applyReturn(index int, evt Event, ts *threadState) *Violation {
	if _, v := r.lookupFunc(index, evt, evt.FuncID); v != nil {
		return v
	}
	if len(ts.stack) == 0 {
		return r.fail(index, evt, ReasonReturnEmpty,
			"thread %d has no frame to return from", evt.Thread)
	}
	top := ts.stack[len(ts.stack)-1]
	if top.funcID != evt.FuncID {
		return r.fail(index, evt, ReasonReturnMismatch,
			"return from %q but shadow stack top is %q", evt.FuncID, top.funcID)
	}

	// A return address pointing into a retired module generation is a
	// redirected return: the image it was computed against no longer exists.
	fn := r.funcs[top.funcID]
	if gen := r.modules[fn.Module]; gen <= 0 || gen != top.gen {
		return r.fail(index, evt, ReasonModuleUnloaded,
			"return address of %q belongs to retired generation %d",
			top.funcID, top.gen)
	}

	ts.stack = ts.stack[:len(ts.stack)-1]
	r.killRecoveryAbove(ts, len(ts.stack))
	return nil
}

func (r *Runtime) applyRecovery(index int, evt Event, ts *threadState) *Violation {
	if rp, ok := ts.recovery[evt.RecoveryID]; ok && rp.alive {
		return r.fail(index, evt, ReasonRecoveryExists,
			"recovery point %d is already alive on thread %d", evt.RecoveryID, evt.Thread)
	}

	// The landing pad sits inside the frame on top of the stack, so its
	// depth is the current stack length minus one.
	depth := len(ts.stack) - 1
	if depth < 0 {
		return r.fail(index, evt, ReasonRecoveryDepth,
			"cannot establish recovery point %d on an empty thread", evt.RecoveryID)
	}

	ts.recovery[evt.RecoveryID] = &recoveryPoint{
		id:     evt.RecoveryID,
		thread: evt.Thread,
		depth:  depth,
		alive:  true,
	}
	return nil
}

func (r *Runtime) applyUnwind(index int, evt Event, ts *threadState) *Violation {
	rp, ok := ts.recovery[evt.RecoveryID]
	if !ok || !rp.alive {
		return r.fail(index, evt, ReasonRecoveryNotFound,
			"recovery point %d is not alive on thread %d", evt.RecoveryID, evt.Thread)
	}

	// The point is owned by this thread's map by construction. A forged
	// event from another thread looks it up in that thread's map and fails
	// the lookup above, which is the earliest possible rejection point.
	if rp.depth >= len(ts.stack) {
		return r.fail(index, evt, ReasonUnwindMismatch,
			"recovery point %d depth %d exceeds stack length %d",
			evt.RecoveryID, rp.depth, len(ts.stack))
	}
	if rp.depth == len(ts.stack)-1 {
		return r.fail(index, evt, ReasonUnwindTop,
			"unwind to recovery point %d would pop no frame", evt.RecoveryID)
	}

	ts.stack = ts.stack[:rp.depth+1]

	// Recovery points owned by popped frames are dead and can never be
	// targeted again; their IDs become reusable.
	r.killRecoveryAbove(ts, rp.depth+1)

	return nil
}

// killRecoveryAbove marks every recovery point whose owning frame has been
// removed as dead. survivingDepth is the new shadow-stack length: any point
// established at depth >= survivingDepth no longer has a live frame. Points
// belonging to frames still on the stack stay alive, which is what lets a
// landing pad catch repeated exceptions on the same frame. Dead entries are
// retained so that a later attempt to unwind to them yields
// ReasonRecoveryNotFound rather than silently matching a re-established
// point.
func (r *Runtime) killRecoveryAbove(ts *threadState, survivingDepth int) {
	for id, rp := range ts.recovery {
		if rp.alive && rp.depth >= survivingDepth {
			rp.alive = false
			ts.recovery[id] = rp
		}
	}
}

func (r *Runtime) thread(thread int) *threadState {
	ts, ok := r.threads[thread]
	if !ok {
		ts = &threadState{recovery: make(map[int]*recoveryPoint)}
		r.threads[thread] = ts
	}
	return ts
}

func (r *Runtime) lookupFunc(index int, evt Event, id string) (FuncInfo, *Violation) {
	fn, ok := r.funcs[id]
	if !ok {
		return FuncInfo{}, r.fail(index, evt, ReasonUnknownFunc,
			"function %q is not registered", id)
	}
	return fn, nil
}

func (r *Runtime) liveGeneration(index int, evt Event, moduleID string) (int, *Violation) {
	gen, ok := r.modules[moduleID]
	if !ok || gen <= 0 {
		return 0, r.fail(index, evt, ReasonModuleUnloaded,
			"module %q is unloaded or unknown", moduleID)
	}
	return gen, nil
}

func findCallSite(fn FuncInfo, id int) (CallSite, bool) {
	for _, site := range fn.CallSites {
		if site.ID == id {
			return site, true
		}
	}
	return CallSite{}, false
}

func (r *Runtime) fail(index int, evt Event, reason, format string, args ...any) *Violation {
	return &Violation{
		Index:  index,
		Event:  evt,
		Reason: reason,
		Detail: fmt.Sprintf(format, args...),
	}
}
