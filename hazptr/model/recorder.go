package model

// Recorder adapts an instrumented Go host to the explicit-visibility machine.
// The host calls the methods below at the corresponding runtime points; each
// call emits exactly one model event. Unlike plain assertion libraries, the
// recorder preserves the staging/publishing split so a host that forgets a
// publish fence is modeled as a still-invisible hazard pointer.
type Recorder struct {
	m      *Machine
	thread int
}

// NewRecorder binds a host thread to a shared machine.
func NewRecorder(m *Machine, threadID int) *Recorder {
	return &Recorder{m: m, thread: threadID}
}

// Machine exposes the underlying state machine.
func (r *Recorder) Machine() *Machine { return r.m }

func (r *Recorder) op(kind OpKind) Op {
	return Op{Kind: kind, Thread: r.thread}
}

// LoadRoot emits OpLoadRoot (read a visible root into reg).
func (r *Recorder) LoadRoot(root, reg, label string) Violation {
	op := r.op(OpLoadRoot)
	op.Root, op.Reg, op.Label = root, reg, label
	return r.m.Step(op)
}

// Protect emits OpProtect (stage a hazard pointer; still invisible).
func (r *Recorder) Protect(slot int, addr, label string) Violation {
	op := r.op(OpProtect)
	op.Slot, op.Addr, op.Label = slot, addr, label
	return r.m.Step(op)
}

// PublishProtection emits OpFlushHP (make staged hazard pointers visible).
func (r *Recorder) PublishProtection(label string) Violation {
	op := r.op(OpFlushHP)
	op.Label = label
	return r.m.Step(op)
}

// ClearProtection emits OpClearHP then OpFlushHP so the clear is published.
func (r *Recorder) ClearProtection(slot int, label string) Violation {
	clear := r.op(OpClearHP)
	clear.Slot, clear.Label = slot, label
	if v := r.m.Step(clear); v.Kind != ViolationNone {
		return v
	}
	return r.PublishProtection(label)
}

// VerifyRoot emits OpVerifyRoot (re-read root and compare with reg).
func (r *Recorder) VerifyRoot(root, reg, label string) Violation {
	op := r.op(OpVerifyRoot)
	op.Root, op.Reg, op.Label = root, reg, label
	return r.m.Step(op)
}

// Deref emits OpDeref; legal only after a consistent re-read.
func (r *Recorder) Deref(reg, label string) Violation {
	op := r.op(OpDeref)
	op.Reg, op.Label = reg, label
	return r.m.Step(op)
}

// Retire emits OpRetire (move an object into the retired set).
func (r *Recorder) Retire(addr, label string) Violation {
	op := r.op(OpRetire)
	op.Addr, op.Label = addr, label
	return r.m.Step(op)
}

// PublishRoot stages and then publishes a new root value.
func (r *Recorder) PublishRoot(root, addr, label string) Violation {
	set := r.op(OpSetRoot)
	set.Root, set.Addr, set.Label = root, addr, label
	if v := r.m.Step(set); v.Kind != ViolationNone {
		return v
	}
	flush := r.op(OpFlushRoot)
	flush.Label = label
	return r.m.Step(flush)
}

// Scan emits one generation-pinned OpScan over every thread.
func (r *Recorder) Scan(label string) Violation {
	op := r.op(OpScan)
	op.Label = label
	return r.m.Step(op)
}

// ScanThreads emits a constrained OpScan; non-nil subset models a partial
// scan and must be rejected when EnforceFullScan is enabled.
func (r *Recorder) ScanThreads(include []int, label string) Violation {
	op := r.op(OpScan)
	op.IncludeThreads, op.Label = append([]int(nil), include...), label
	return r.m.Step(op)
}

// Alloc emits OpAlloc, reusing a freed address when reuseAddr is non-empty.
func (r *Recorder) Alloc(reg, reuseAddr, label string) Violation {
	op := r.op(OpAlloc)
	op.Reg, op.ReuseAddr, op.Label = reg, reuseAddr, label
	return r.m.Step(op)
}
