package netsec

import "sync"

// SignalCall records an authorized delivery.  Tests can use it to prove
// that denied requests never reached the host.
type SignalCall struct {
	FD      int
	PID     int
	Signal  int
	Siginfo *Siginfo
	Flags   uint32
}

// MemoryHost is an in-memory Host for tests and for embedding the policy in
// a simulator.  Its exported maps make it easy to model exits and PID reuse:
// delete a PID to make it exit, then insert a new ProcessInfo with the same
// PID and a different StartTime.
type MemoryHost struct {
	mu sync.Mutex

	Processes map[int]ProcessInfo
	FDs       map[int]PIDFDIdentity
	FDToPID   map[int]int
	SendCalls []SignalCall

	// Visibility, when set, overrides the package's namespace comparison.
	Visibility func(caller Caller, target ProcessInfo) bool
}

// NewMemoryHost returns an empty MemoryHost.
func NewMemoryHost() *MemoryHost {
	return &MemoryHost{
		Processes: make(map[int]ProcessInfo),
		FDs:       make(map[int]PIDFDIdentity),
		FDToPID:   make(map[int]int),
	}
}

// AddProcess inserts or replaces a process snapshot.
func (h *MemoryHost) AddProcess(info ProcessInfo) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.Processes == nil {
		h.Processes = make(map[int]ProcessInfo)
	}
	h.Processes[info.PID] = info
}

// SetProcess is an alias for AddProcess.
func (h *MemoryHost) SetProcess(info ProcessInfo) { h.AddProcess(info) }

// RemoveProcess models the process exiting.
func (h *MemoryHost) RemoveProcess(pid int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.Processes, pid)
}

// ReusePID replaces pid with a new process that reuses the PID.
func (h *MemoryHost) ReusePID(pid int, startTime uint64, uid uint32) ProcessInfo {
	info := ProcessInfo{PID: pid, StartTime: startTime, UID: uid}
	h.AddProcess(info)
	return info
}

// SetPIDFD records an identity for fd, allowing fd-reuse tests.
func (h *MemoryHost) SetPIDFD(fd int, id PIDFDIdentity) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.FDs == nil {
		h.FDs = make(map[int]PIDFDIdentity)
	}
	h.FDs[fd] = id
}

// SetPIDFDTarget records the process a fake pidfd refers to.
func (h *MemoryHost) SetPIDFDTarget(fd, pid int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.FDToPID == nil {
		h.FDToPID = make(map[int]int)
	}
	h.FDToPID[fd] = pid
}

// LookupProcess implements Host.
func (h *MemoryHost) LookupProcess(pid int) (ProcessInfo, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	info, ok := h.Processes[pid]
	if !ok {
		return ProcessInfo{}, ErrProcessNotFound
	}
	if info.PID == 0 {
		info.PID = pid
	}
	return info, nil
}

// SendSignal implements Host.
func (h *MemoryHost) SendSignal(fd, pid, sig int, info *Siginfo, flags uint32) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	var copied *Siginfo
	if info != nil {
		s := *info
		copied = &s
	}
	h.SendCalls = append(h.SendCalls, SignalCall{
		FD: fd, PID: pid, Signal: sig, Siginfo: copied, Flags: flags,
	})
	return nil
}

// CanSee implements VisibilityChecker.
func (h *MemoryHost) CanSee(caller Caller, target ProcessInfo) bool {
	h.mu.Lock()
	fn := h.Visibility
	h.mu.Unlock()
	if fn != nil {
		return fn(caller, target)
	}
	return visibleFrom(caller, target)
}

// PIDFDIdentity implements PIDFDVerifier.  Unknown descriptors report a
// zero identity, which is stable until a test assigns a different one.
func (h *MemoryHost) PIDFDIdentity(fd int) (PIDFDIdentity, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.FDs[fd], nil
}

// PIDFDTarget implements PIDFDTargetResolver.
func (h *MemoryHost) PIDFDTarget(fd int) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.FDToPID[fd], nil
}

// LastSignal returns the most recent authorized delivery.
func (h *MemoryHost) LastSignal() (SignalCall, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.SendCalls) == 0 {
		return SignalCall{}, false
	}
	return h.SendCalls[len(h.SendCalls)-1], true
}

// SignalCount returns the number of authorized deliveries.
func (h *MemoryHost) SignalCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.SendCalls)
}
