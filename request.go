package netsec

import (
	"errors"
	"sync"
)

// Errors returned by the request component. The host may wrap these; callers
// should use errors.Is rather than matching message text.
var (
	// ErrUnknownProcess is returned when no process instance is registered
	// for the reference.
	ErrUnknownProcess = errors.New("netsec: unknown process")
	// ErrStaleGeneration is returned when the referenced process instance has
	// been replaced by a newer generation (restart or PID reuse).
	ErrStaleGeneration = errors.New("netsec: process generation is no longer current")
	// ErrDuplicateRequest is returned when a request with the same ID is
	// already active for the live process instance.
	ErrDuplicateRequest = errors.New("netsec: duplicate active request id")
	// ErrRequestInvalid is returned at a validity gate when the request is no
	// longer live (for example it was invalidated by a restart) or has never
	// been registered.
	ErrRequestInvalid = errors.New("netsec: request is no longer valid")
	// ErrHandleAlreadyTransferred guards the single-transfer invariant: a
	// handle whose ownership has moved to a target process can never be
	// injected or closed by the proxy again.
	ErrHandleAlreadyTransferred = errors.New("netsec: handle ownership already transferred")
)

// requestState is the lifecycle state of a tracked request.
type requestState int

const (
	stateActive requestState = iota
	stateTransferred
	stateFailed
	stateStale
)

// requestKey identifies a request within a specific process instance. The
// generation is part of the key, so a reused request ID in a new generation
// never collides with the old request.
type requestKey struct {
	pid        ProcessID
	generation uint64
	requestID  RequestID
}

// Request is the registry record for one accepted file-open request.
//
// Path holds a private copy of the caller-supplied path bytes: the host
// copies the bytes out of untrusted shared memory before handing them over,
// and the registry makes its own copy as well so later mutation by the caller
// cannot change the path that gets opened.
type Request struct {
	ref       ProcessRef
	requestID RequestID
	path      []byte
	state     requestState
}

// Ref returns the process instance the request is bound to.
func (r *Request) Ref() ProcessRef { return r.ref }

// RequestID returns the request identifier.
func (r *Request) RequestID() RequestID { return r.requestID }

// Path returns a copy of the privately held path bytes. A copy is returned so
// callers cannot mutate the stored path through the returned slice.
func (r *Request) Path() []byte {
	path := make([]byte, len(r.path))
	copy(path, r.path)
	return path
}

// Registry tracks live process instances and the requests bound to them.
//
// It is the single authority on whether a request is still valid at the two
// gates the host must enforce: before executing the open syscall and before
// delivering the handle. All decisions are made under one mutex so that a
// concurrent restart cannot make a gate decision stale before it is acted on.
type Registry struct {
	mu sync.Mutex

	// liveGeneration maps a PID to the generation that currently owns it.
	liveGeneration map[ProcessID]uint64
	requests       map[requestKey]*Request
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		liveGeneration: make(map[ProcessID]uint64),
		requests:       make(map[requestKey]*Request),
	}
}

// StartProcess registers a fresh process instance with the given initial
// generation. It is typically called by the host's process supervisor when it
// learns about a new sandboxed process.
func (reg *Registry) StartProcess(ref ProcessRef) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	reg.liveGeneration[ref.PID] = ref.Generation
}

// RestartProcess records that pid has been replaced by a new instance with
// the given generation. Every request belonging to earlier generations is
// atomically marked stale, so any in-flight request fails at its next
// validity gate (or, if its handle was already open, gets closed by the
// host). Returns the previous generation, or zero if the PID was unknown.
func (reg *Registry) RestartProcess(pid ProcessID, generation uint64) (previous uint64) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	previous = reg.liveGeneration[pid]
	reg.liveGeneration[pid] = generation
	for key, req := range reg.requests {
		if key.pid == pid && key.generation != generation && req.state == stateActive {
			req.state = stateStale
		}
	}
	return previous
}

// ExitProcess removes a process instance. Its active requests become stale;
// a later process reusing the PID starts from a fresh generation.
func (reg *Registry) ExitProcess(pid ProcessID) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	delete(reg.liveGeneration, pid)
	for key, req := range reg.requests {
		if key.pid == pid && req.state == stateActive {
			req.state = stateStale
		}
	}
}

// Submit accepts a new request for the process instance named by ref. It
// fails if the process is unknown, the generation is no longer current, or a
// request with the same ID already occupies a non-terminal slot on the live
// instance (active or successfully transferred). Only failed or invalidated
// slots can be replaced, so an ID is never reused while the process instance
// that minted it is still alive.
//
// path is copied; the caller retains ownership of the slice it passed in.
func (reg *Registry) Submit(ref ProcessRef, requestID RequestID, path []byte) (*Request, error) {
	reg.mu.Lock()
	defer reg.mu.Unlock()

	generation, ok := reg.liveGeneration[ref.PID]
	if !ok {
		return nil, ErrUnknownProcess
	}
	if generation != ref.Generation {
		return nil, ErrStaleGeneration
	}

	key := requestKey{pid: ref.PID, generation: ref.Generation, requestID: requestID}
	if existing := reg.requests[key]; existing != nil &&
		existing.state != stateFailed && existing.state != stateStale {
		return nil, ErrDuplicateRequest
	}

	privatePath := make([]byte, len(path))
	copy(privatePath, path)
	req := &Request{
		ref:       ref,
		requestID: requestID,
		path:      privatePath,
		state:     stateActive,
	}
	reg.requests[key] = req
	return req, nil
}

// Validate is the validity gate. It returns the tracked request only while
// the process instance is still live, the generation still matches, and the
// request itself is still active. A restart, exit, or prior terminal
// transition all make the request invalid.
func (reg *Registry) Validate(ref ProcessRef, requestID RequestID) (*Request, error) {
	reg.mu.Lock()
	defer reg.mu.Unlock()

	generation, ok := reg.liveGeneration[ref.PID]
	if !ok {
		return nil, ErrUnknownProcess
	}
	if generation != ref.Generation {
		return nil, ErrStaleGeneration
	}

	req := reg.requests[requestKey{pid: ref.PID, generation: ref.Generation, requestID: requestID}]
	if req == nil || req.state != stateActive {
		return nil, ErrRequestInvalid
	}
	return req, nil
}

// MarkTransferred records that the request's handle ownership has moved into
// the target process. It fails if the request is not currently active, which
// protects the single-transfer invariant.
func (reg *Registry) MarkTransferred(req *Request) error {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if req.state != stateActive {
		return ErrHandleAlreadyTransferred
	}
	req.state = stateTransferred
	return nil
}

// MarkFailed records a terminal failure of an active request (open error,
// failed delivery gate, failed response). A failed request never becomes
// active again; the host must answer a retry with a fresh request ID.
func (reg *Registry) MarkFailed(req *Request) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if req.state == stateActive {
		req.state = stateFailed
	}
}

// activeCount reports how many requests are currently active. It is intended
// for tests and diagnostics.
func (reg *Registry) activeCount() int {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	count := 0
	for _, req := range reg.requests {
		if req.state == stateActive {
			count++
		}
	}
	return count
}
