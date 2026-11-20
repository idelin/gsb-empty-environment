package caproute

import (
	"context"
	"encoding/hex"
	"io"
)

// resolve looks up a handle and enforces binding, tenant, generation,
// revocation and type checks. It never embeds a foreign resource label in the
// returned error; callers add the label only to the host-side audit event.
func (h *Host) resolve(caller InstanceID, handle Handle, op Operation) (*capability, *DenialError) {
	if len(handle) == 0 {
		return nil, denied(ReasonInvalidHandle, "empty handle")
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	capb, ok := h.handles[hex.EncodeToString(handle)]
	if !ok {
		// Covers forged bytes, foreign handles and revoked/closed handles
		// alike: the response is identical so no existence leaks.
		return nil, denied(ReasonInvalidHandle, "handle is not valid for this caller")
	}
	if capb.owner != caller {
		return capb, denied(ReasonInvalidHandle, "handle is not valid for this caller")
	}
	ins, ok := h.insts[caller]
	if !ok {
		return capb, denied(ReasonUnknownInstance, "caller is not registered")
	}
	if capb.generation != ins.generation {
		return capb, denied(ReasonStaleGeneration, "handle predates the current generation")
	}
	if capb.revoked {
		return capb, denied(ReasonRevokedHandle, "capability has been revoked")
	}
	if want, known := opType[op]; !known {
		return capb, denied(ReasonUnknownOperation, string(op))
	} else if want != capb.rtype {
		return capb, denied(ReasonTypeMismatch, "operation does not match the handle's resource type")
	}
	return capb, nil
}

func (h *Host) recordAudit(ev AuditEvent) {
	if h.audit != nil {
		h.audit.Record(ev)
	}
}

// baseEvent collects the fields known before resolution.
func baseEvent(caller InstanceID, op Operation, chain CallChain) AuditEvent {
	rtype := opType[op]
	return AuditEvent{
		Caller:       caller,
		ResourceType: rtype,
		Operation:    op,
		ChainLen:     len(chain),
	}
}

// serve performs the chain-intersection permission check, acquires a lease on
// the resource gate and invokes the resource callback.
func (h *Host) serve(ctx context.Context, caller InstanceID, capb *capability, op Operation, payload []byte, chain CallChain) ([]byte, error) {
	ev := baseEvent(caller, op, chain)
	ev.Target = capb.owner
	ev.Label = capb.gate.label
	ev.Generation = capb.generation

	eff := chain[0].allowed
	for i := 1; i < len(chain); i++ {
		eff = eff.intersect(chain[i].allowed)
	}
	if !eff.contains(op) {
		ev.Reason = ReasonOperationDenied
		ev.Detail = "operation is outside the intersection of the call chain"
		h.recordAudit(ev)
		return nil, denied(ReasonOperationDenied, "operation is not permitted by the effective call chain")
	}

	if !capb.gate.acquire() {
		ev.Reason = ReasonResourceClosed
		ev.Detail = "resource gate is closed"
		h.recordAudit(ev)
		return nil, denied(ReasonResourceClosed, "backing resource is closed")
	}
	defer capb.gate.release()

	out, err := capb.gate.resource.Serve(ctx, op, payload, chain)
	ev.Detail = "allowed"
	h.recordAudit(ev)
	return out, err
}

// Entry invokes a resource directly on behalf of an instance. The instance
// forms the first frame of a fresh call chain, restricted to exactly the
// capability's allowed operations.
func (h *Host) Entry(ctx context.Context, caller InstanceID, handle Handle, op Operation, payload []byte) ([]byte, error) {
	if !op.IsKnown() {
		ev := baseEvent(caller, op, nil)
		ev.Reason = ReasonUnknownOperation
		ev.Detail = "operation is not registered"
		h.recordAudit(ev)
		return nil, denied(ReasonUnknownOperation, string(op))
	}
	capb, dErr := h.resolve(caller, handle, op)
	ev := baseEvent(caller, op, nil)
	if dErr != nil {
		ev.Reason = dErr.Reason
		ev.Detail = dErr.detail
		if capb != nil {
			ev.Label = capb.gate.label
		}
		h.recordAudit(ev)
		return nil, dErr
	}
	ev.Target = capb.owner

	gen := h.Generation(caller)
	chain := CallChain{{
		Instance:   caller,
		Allowed:    capb.allowed.toList(),
		allowed:    capb.allowed,
		Generation: gen,
	}}
	return h.serve(ctx, caller, capb, op, payload, chain)
}

// Proxy performs a host-mediated nested call. chain is the current call
// chain of the caller; the target capability contributes a new frame. The
// effective permission is the intersection of every frame, so a more
// privileged handle can never widen a less privileged chain. Cycles are
// rejected and the chain may not exceed MaxChainDepth frames.
func (h *Host) Proxy(ctx context.Context, caller InstanceID, targetHandle Handle, op Operation, payload []byte, chain CallChain) ([]byte, error) {
	ev := baseEvent(caller, op, chain)
	if len(chain) == 0 {
		ev.Reason = ReasonNoCallChain
		ev.Detail = "proxy calls require an existing call chain"
		h.recordAudit(ev)
		return nil, denied(ReasonNoCallChain, "proxy call presented no call chain")
	}
	if len(chain) >= MaxChainDepth {
		ev.Reason = ReasonChainDepth
		ev.Detail = "chain already at maximum depth"
		h.recordAudit(ev)
		return nil, denied(ReasonChainDepth, "call chain exceeds maximum depth")
	}
	if !op.IsKnown() {
		ev.Reason = ReasonUnknownOperation
		ev.Detail = "operation is not registered"
		h.recordAudit(ev)
		return nil, denied(ReasonUnknownOperation, string(op))
	}

	capb, dErr := h.resolve(caller, targetHandle, op)
	if dErr != nil {
		ev.Reason = dErr.Reason
		ev.Detail = dErr.detail
		if capb != nil {
			ev.Label = capb.gate.label
		}
		h.recordAudit(ev)
		return nil, dErr
	}
	if chain.containsInstance(capb.owner) {
		ev.Target = capb.owner
		ev.Label = capb.gate.label
		ev.Reason = ReasonChainCycle
		ev.Detail = "target instance already appears in the call chain"
		h.recordAudit(ev)
		return nil, denied(ReasonChainCycle, "re-entrant call chain is not allowed")
	}

	gen := h.Generation(caller)
	newChain := append(chainCopy(chain), ChainFrame{
		Instance:   capb.owner,
		Allowed:    capb.allowed.toList(),
		allowed:    capb.allowed,
		Generation: gen,
	})
	return h.serve(ctx, caller, capb, op, payload, newChain)
}

// Delegate mints a new capability that can only be narrower than the source
// capability. Requesting any operation the source does not hold is rejected
// as widening. The delegated handle stays bound to the same resource but is
// stamped with the caller's current generation, so a reload invalidates it.
func (h *Host) Delegate(caller InstanceID, source Handle, ops []Operation) (Handle, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	ins, ok := h.insts[caller]
	if !ok {
		h.recordAudit(AuditEvent{Caller: caller, Reason: ReasonUnknownInstance, Detail: "caller is not registered"})
		return nil, denied(ReasonUnknownInstance, "caller is not registered")
	}
	capb, ok := h.handles[hex.EncodeToString(source)]
	ev := AuditEvent{Caller: caller, Generation: ins.generation, ChainLen: 0}
	if !ok || capb.owner != caller {
		ev.Reason = ReasonInvalidHandle
		ev.Detail = "source handle is not valid for this caller"
		if capb != nil {
			ev.Label = capb.gate.label
		}
		h.recordAudit(ev)
		return nil, denied(ReasonInvalidHandle, "source handle is not valid for this caller")
	}
	if capb.generation != ins.generation {
		ev.Label = capb.gate.label
		ev.Reason = ReasonStaleGeneration
		ev.Detail = "source handle predates the current generation"
		h.recordAudit(ev)
		return nil, denied(ReasonStaleGeneration, "source handle predates the current generation")
	}

	requested := makeOpSet(ops)
	ev.ResourceType = capb.rtype
	ev.Label = capb.gate.label
	ev.Target = capb.owner
	for op := range requested {
		want, known := opType[op]
		if !known {
			ev.Reason = ReasonUnknownOperation
			ev.Operation = op
			ev.Detail = "operation is not registered"
			h.recordAudit(ev)
			return nil, denied(ReasonUnknownOperation, string(op))
		}
		if want != capb.rtype {
			ev.Reason = ReasonTypeMismatch
			ev.Operation = op
			ev.Detail = "operation does not match the source resource type"
			h.recordAudit(ev)
			return nil, denied(ReasonTypeMismatch, "operation does not match the source resource type")
		}
		if !capb.allowed.contains(op) {
			ev.Reason = ReasonWidening
			ev.Operation = op
			ev.Detail = "delegation requested an operation absent from the source capability"
			h.recordAudit(ev)
			return nil, denied(ReasonWidening, "delegation can only narrow the source capability")
		}
	}
	if len(requested) == 0 {
		ev.Reason = ReasonWidening
		ev.Detail = "delegation requested an empty operation set"
		h.recordAudit(ev)
		return nil, denied(ReasonWidening, "delegation must permit at least one operation")
	}

	raw := make([]byte, 32)
	if _, err := io.ReadFull(h.rand, raw); err != nil {
		return nil, err
	}
	key := hex.EncodeToString(raw)
	if _, collision := h.handles[key]; collision {
		return nil, denied(ReasonInvalidHandle, "handle collision; retry")
	}
	minted := &capability{
		id:         raw,
		owner:      caller,
		gate:       capb.gate,
		rtype:      capb.rtype,
		allowed:    requested,
		generation: ins.generation,
	}
	h.handles[key] = minted
	ev.Detail = "delegation allowed"
	h.recordAudit(ev)

	out := make(Handle, len(raw))
	copy(out, raw)
	return out, nil
}

func chainCopy(in CallChain) CallChain {
	out := make(CallChain, len(in))
	copy(out, in)
	return out
}
