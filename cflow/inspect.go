package cflow

// FrameView is an immutable snapshot of one shadow-stack entry exposed to
// the test host for assertions.
type FrameView struct {
	// FuncID is the function executing in the frame.
	FuncID string

	// CallSite is the call site in the caller that created the frame.
	CallSite int

	// Generation is the module generation captured at entry.
	Generation int
}

// StackDepth returns the current shadow-stack depth of a thread. Unknown
// threads report zero.
func (r *Runtime) StackDepth(thread int) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	ts, ok := r.threads[thread]
	if !ok {
		return 0
	}
	return len(ts.stack)
}

// Stack returns a bottom-up copy of a thread's shadow stack.
func (r *Runtime) Stack(thread int) []FrameView {
	r.mu.Lock()
	defer r.mu.Unlock()

	ts, ok := r.threads[thread]
	if !ok {
		return nil
	}
	out := make([]FrameView, len(ts.stack))
	for i, f := range ts.stack {
		out[i] = FrameView{FuncID: f.funcID, CallSite: f.callSite, Generation: f.gen}
	}
	return out
}

// RecoveryAlive reports whether recovery point id is currently alive on the
// given thread. Points belonging to another thread always report false.
func (r *Runtime) RecoveryAlive(thread, id int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	ts, ok := r.threads[thread]
	if !ok {
		return false
	}
	rp, ok := ts.recovery[id]
	return ok && rp.alive
}

// RecoveryDepth returns the shadow-stack depth at which recovery point id
// was established on the thread. The boolean is false for unknown or dead
// points.
func (r *Runtime) RecoveryDepth(thread, id int) (int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	ts, ok := r.threads[thread]
	if !ok {
		return 0, false
	}
	rp, ok := ts.recovery[id]
	if !ok || !rp.alive {
		return 0, false
	}
	return rp.depth, true
}

// ModuleGeneration returns the recorded generation of a module. A negative
// result means the module is currently unloaded; zero means it was never
// registered.
func (r *Runtime) ModuleGeneration(moduleID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.modules[moduleID]
}
