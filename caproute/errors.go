package caproute

import "sync"

// Reason is a machine-readable denial reason. Reasons deliberately describe
// why a request failed without naming any other tenant's resource.
type Reason string

const (
	// ReasonUnknownOperation: the operation is not a registered one.
	ReasonUnknownOperation Reason = "unknown_operation"
	// ReasonTypeMismatch: the operation does not belong to the handle's
	// resource type (cross-type reinterpretation attempt).
	ReasonTypeMismatch Reason = "operation_type_mismatch"
	// ReasonInvalidHandle: the bytes are not a live handle owned by the
	// caller. The same opaque reason covers forged and foreign handles so
	// that probing cannot confirm another tenant's resources.
	ReasonInvalidHandle Reason = "invalid_handle"
	// ReasonUnknownInstance: the caller instance is not registered.
	ReasonUnknownInstance Reason = "unknown_instance"
	// ReasonStaleGeneration: the handle was minted under an earlier
	// generation and was invalidated by a reload.
	ReasonStaleGeneration Reason = "stale_generation"
	// ReasonRevokedHandle: the capability was revoked (for example by a
	// resource close) while its instance stayed live.
	ReasonRevokedHandle Reason = "revoked_handle"
	// ReasonOperationDenied: the operation is not part of the effective
	// permission for the call chain or delegation.
	ReasonOperationDenied Reason = "operation_denied"
	// ReasonResourceClosed: the backing resource has been closed.
	ReasonResourceClosed Reason = "resource_closed"
	// ReasonChainCycle: appending the proxy frame would revisit an
	// instance already present in the call chain.
	ReasonChainCycle Reason = "call_chain_cycle"
	// ReasonChainDepth: the chain already holds MaxChainDepth frames.
	ReasonChainDepth Reason = "call_chain_depth_exceeded"
	// ReasonNoCallChain: a proxy/entry call arrived without a chain.
	ReasonNoCallChain Reason = "missing_call_chain"
	// ReasonWidening: a delegation requested rights the delegator does
	// not hold. Delegation can only narrow.
	ReasonWidening Reason = "delegation_widening"
	// ReasonResourceInUse: a close/destroy operation collided with state.
	ReasonResourceInUse Reason = "resource_in_use"
)

// DenialError is returned for every policy rejection. Its message contains
// only the reason code and, where useful, data belonging to the caller; it
// never carries labels or identities of resources owned by another tenant.
type DenialError struct {
	Reason Reason
	detail string
}

func (e *DenialError) Error() string {
	if e.detail == "" {
		return "caproute: denied: " + string(e.Reason)
	}
	return "caproute: denied: " + string(e.Reason) + ": " + e.detail
}

func denied(reason Reason, detail string) *DenialError {
	return &DenialError{Reason: reason, detail: detail}
}

// AuditEvent records one policy decision. Audit records are host-side only
// and may include the resource label; plug-in-facing errors do not.
type AuditEvent struct {
	// Reason is empty for an allowed invocation, otherwise a denial reason.
	Reason Reason
	// Caller is the immediate calling instance.
	Caller InstanceID
	// Target is the instance that owns the target handle, if resolved.
	Target InstanceID
	// ResourceType is the requested type, even when resolution fails.
	ResourceType ResourceType
	// Operation is the requested operation.
	Operation Operation
	// Generation is the caller's generation at decision time.
	Generation Generation
	// ChainLen is the depth of the presented call chain.
	ChainLen int
	// Label is the host-side resource label. It must never be rendered to
	// a plug-in and is absent for unresolved handles.
	Label string
	// Detail is an internal, human-readable explanation.
	Detail string
}

// AuditSink receives the full audit trail. Implementations must be safe for
// concurrent use.
type AuditSink interface {
	Record(event AuditEvent)
}

// MemoryAudit keeps events in memory. It is safe for concurrent use and is
// handy for tests and simple embedders.
type MemoryAudit struct {
	mu     sync.Mutex
	events []AuditEvent
}

// Record appends an event.
func (m *MemoryAudit) Record(event AuditEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
}

// Events returns a copy of the recorded events.
func (m *MemoryAudit) Events() []AuditEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AuditEvent, len(m.events))
	copy(out, m.events)
	return out
}

// Reset drops all recorded events.
func (m *MemoryAudit) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = nil
}
