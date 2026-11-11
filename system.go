package netsec

// System is the set of privileged operations the proxy host needs in order to
// open a file on behalf of a sandboxed process and hand the resulting handle
// back to that process.
//
// Implementations are expected to be safe for concurrent use. A real host
// implementation wraps the platform syscalls (openat, the descriptor
// injection mechanism, the IPC reply channel); tests use a controllable fake
// that can block at well-defined checkpoints so that process lifecycle events
// can be interleaved with request processing.
type System interface {
	// Open opens path on behalf of process pid and returns a handle owned by
	// the proxy. The returned Handle must not be usable inside the target
	// process until InjectHandle transfers ownership.
	Open(pid ProcessID, path []byte) (Handle, error)

	// InjectHandle moves handle into the address space of the process
	// identified by ref. It must fail rather than inject if the live process
	// instance does not match ref.Generation. On success the proxy ceases to
	// own the handle and must not close it.
	InjectHandle(ref ProcessRef, handle Handle) error

	// SendResponse delivers the completion reply for requestID to the process
	// instance identified by ref. It must fail rather than deliver if the live
	// process instance does not match ref.Generation.
	SendResponse(ref ProcessRef, requestID RequestID) error

	// Close releases a proxy-owned handle. It must not touch a handle whose
	// ownership has already been transferred to a target process: the
	// descriptor number may already refer to an unrelated object there.
	// Implementations should return ErrHandleAlreadyTransferred in that case.
	Close(handle Handle) error
}

// Checkpoint marks a well-defined point inside request processing. An
// implementation that supports it (notably the test fake) may block the
// calling goroutine there until released, which lets tests force process
// lifecycle events into precise windows of the request lifecycle.
//
// A System does not have to implement Checkpoint; the host treats its
// absence as an immediate no-op. Production implementations normally leave
// Checkpoint unimplemented.
type Checkpointer interface {
	Checkpoint(point Checkpoint)
}

// Checkpoint identifies a synchronization point in request processing.
type Checkpoint int

const (
	// CheckpointAfterSubmit is reached once the request has been accepted by
	// the registry but before any validation or syscall.
	CheckpointAfterSubmit Checkpoint = iota
	// CheckpointBeforeOpen is reached immediately before the pre-execution
	// validity gate and the Open call.
	CheckpointBeforeOpen
	// CheckpointBeforeInject is reached after a successful Open, immediately
	// before the pre-delivery validity gate and InjectHandle.
	CheckpointBeforeInject
	// CheckpointBeforeResponse is reached after a successful injection,
	// immediately before SendResponse.
	CheckpointBeforeResponse
)

// Handle is an opaque reference to an open file. Ownership follows a strict
// state machine: the proxy owns every handle it receives from Open, and a
// handle can be transferred to a target process at most once.
type Handle interface {
	// ID returns the stable identifier of the underlying kernel object.
	ID() int64
}

// ProcessID identifies a process slot. Process IDs can be reused after exit,
// which is why every process reference additionally carries a generation.
type ProcessID int64

// RequestID identifies a request inside a process instance. Request IDs can
// be reused once a process instance is gone, never while it is alive.
type RequestID uint64

// ProcessRef pinpoints one concrete process instance. The generation is a
// trusted value supplied by the host's process supervisor: every restart
// (including PID reuse) bumps the generation, so an old reference can never
// match a new process instance.
type ProcessRef struct {
	PID        ProcessID
	Generation uint64
}
