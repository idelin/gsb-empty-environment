package netsec

import "errors"

// ErrResponseFailed is returned when the handle was injected successfully but
// the completion response could not be delivered. The handle itself stays
// with the target process: the proxy has already relinquished ownership and
// must not close it. Recovery (closing or reclaiming the handle) is the
// host's responsibility through its out-of-band reconciliation channel.
var ErrResponseFailed = errors.New("netsec: response delivery failed after handle injection")

// Manager is the request component a trusted proxy host uses to serve
// sandboxed file-open requests.
//
// A request is always bound to one concrete process instance (PID plus a
// trusted generation). The manager enforces two validity gates:
//
//   - immediately before executing the open syscall, and
//   - immediately before injecting the resulting handle.
//
// If either gate fails, or the open itself fails, no handle is delivered and
// every proxy-owned handle involved is closed immediately. A handle can be
// injected at most once, and only into the process instance the request was
// submitted for.
type Manager struct {
	registry *Registry
	system   System
}

// NewManager builds a Manager backed by registry and the privileged system
// interface sys.
func NewManager(registry *Registry, sys System) *Manager {
	return &Manager{registry: registry, system: sys}
}

// Handle processes one file-open request.
//
// ref is the trusted process reference (PID and generation) supplied by the
// process supervisor, requestID is the id the sandbox assigned, and path is
// the already-copied path bytes. The method is safe for concurrent use:
// independent requests may be handled in parallel.
//
// Contract on errors:
//
//   - If the request never reaches successful injection, no handle enters the
//     target process and any handle opened on the proxy side has been closed.
//   - If injection succeeds but SendResponse fails, ErrResponseFailed is
//     returned. The handle remains owned by the target process and is not
//     closed or injected a second time.
func (m *Manager) Handle(ref ProcessRef, requestID RequestID, path []byte) error {
	req, err := m.registry.Submit(ref, requestID, path)
	if err != nil {
		return err
	}
	m.checkpoint(CheckpointAfterSubmit)

	// Gate 1: the request and its process instance must still be valid before
	// we perform any privileged work on their behalf.
	m.checkpoint(CheckpointBeforeOpen)
	if _, err := m.registry.Validate(ref, requestID); err != nil {
		return err
	}

	handle, err := m.system.Open(ref.PID, req.Path())
	if err != nil {
		m.registry.MarkFailed(req)
		return err
	}

	// From here on the proxy owns a handle. The deferred close below only
	// acts while ownership has not been transferred: once InjectHandle
	// succeeds the proxy must never touch the handle again.
	delivered := false
	defer func() {
		if !delivered {
			_ = m.system.Close(handle)
		}
	}()

	// Gate 2: re-validate immediately before delivery. A restart that landed
	// between Open and here must result in the handle being closed, never
	// injected into the replacement process.
	m.checkpoint(CheckpointBeforeInject)
	if _, err := m.registry.Validate(ref, requestID); err != nil {
		m.registry.MarkFailed(req)
		return err
	}

	// The system interface guarantees injection targets exactly
	// ref.Generation and fails otherwise. Recording the transfer only after
	// success keeps the registry transition honest, and because Submit
	// rejects duplicate active requests the same request can never drive a
	// second injection.
	if err := m.system.InjectHandle(ref, handle); err != nil {
		m.registry.MarkFailed(req)
		return err
	}
	if err := m.registry.MarkTransferred(req); err != nil {
		// The handle already crossed into the target process; the proxy must
		// not close it. Surface the inconsistency without touching it.
		return err
	}
	delivered = true

	// The handle now belongs to the target process. A response failure must
	// not cause us to close or re-inject it.
	m.checkpoint(CheckpointBeforeResponse)
	if err := m.system.SendResponse(ref, requestID); err != nil {
		return ErrResponseFailed
	}
	return nil
}

// checkpoint invokes the optional Checkpoint hook. Systems without the hook
// (all production implementations) pay only a type-assertion cost.
func (m *Manager) checkpoint(point Checkpoint) {
	if checkpointer, ok := m.system.(Checkpointer); ok {
		checkpointer.Checkpoint(point)
	}
}
