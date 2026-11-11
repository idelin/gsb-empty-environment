package netsec

import "sync"

// Host simulates the kernel side of a storage service: processes,
// their descriptor tables and the open file descriptions that locks
// are anchored to.
type Host struct {
	mu      sync.Mutex
	nextPID int
	nextOFD int
	procs   map[int]*Process
	files   map[string]*File
}

func NewHost() *Host {
	return &Host{
		nextPID: 1,
		procs:   make(map[int]*Process),
		files:   make(map[string]*File),
	}
}

// File returns the inode for path, creating it on first use.
func (h *Host) File(path string) *File {
	h.mu.Lock()
	defer h.mu.Unlock()
	f, ok := h.files[path]
	if !ok {
		f = &File{path: path}
		h.files[path] = f
	}
	return f
}

// Spawn creates a fresh process with an empty descriptor table.
func (h *Host) Spawn() *Process {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.spawnLocked()
}

func (h *Host) spawnLocked() *Process {
	p := &Process{
		host:  h,
		pid:   h.nextPID,
		alive: true,
		fds:   make(map[int]*OpenFileDescription),
	}
	h.nextPID++
	h.procs[p.pid] = p
	return p
}

// OpenFileDescription is the kernel object behind one or more
// descriptors; OFD locks are anchored here, not to a pid.
type OpenFileDescription struct {
	id   int
	host *Host
	file *File
	refs int // guarded by host.mu
}

// Process is a simulated tenant of the storage service.
type Process struct {
	host   *Host
	pid    int
	alive  bool
	fds    map[int]*OpenFileDescription
	nextFD int
}

// PID returns the process id.
func (p *Process) PID() int { return p.pid }

// Open creates a new open file description and returns a descriptor.
func (p *Process) Open(f *File) int {
	h := p.host
	h.mu.Lock()
	defer h.mu.Unlock()
	ofd := &OpenFileDescription{id: h.nextOFD, host: h, file: f, refs: 1}
	h.nextOFD++
	fd := p.nextFD
	p.nextFD++
	p.fds[fd] = ofd
	return fd
}

// Dup duplicates fd; the new descriptor shares the same open file
// description and therefore the same OFD locks.
func (p *Process) Dup(fd int) int {
	h := p.host
	h.mu.Lock()
	defer h.mu.Unlock()
	ofd, ok := p.fds[fd]
	if !ok {
		return -1
	}
	ofd.refs++
	nfd := p.nextFD
	p.nextFD++
	p.fds[nfd] = ofd
	return nfd
}

// Fork creates a child process whose descriptors reference the same
// open file descriptions. OFD locks are shared with the parent; POSIX
// record locks are not inherited.
func (p *Process) Fork() *Process {
	h := p.host
	h.mu.Lock()
	defer h.mu.Unlock()
	c := h.spawnLocked()
	c.nextFD = p.nextFD
	for fd, ofd := range p.fds {
		c.fds[fd] = ofd
		ofd.refs++
	}
	return c
}

// Close closes one descriptor. Closing any descriptor on a file drops
// every POSIX lock this process holds on that file; OFD locks survive
// until the last reference to the description is closed.
func (p *Process) Close(fd int) {
	h := p.host
	h.mu.Lock()
	ofd, ok := p.fds[fd]
	if !ok {
		h.mu.Unlock()
		return
	}
	delete(p.fds, fd)
	h.mu.Unlock()
	ofd.file.unlockOwner(ownerKey{kind: POSIX, proc: p})
	releaseOFD(ofd)
}

func releaseOFD(ofd *OpenFileDescription) {
	h := ofd.host
	h.mu.Lock()
	ofd.refs--
	last := ofd.refs == 0
	h.mu.Unlock()
	if last {
		ofd.file.unlockOwner(ownerKey{kind: OFD, ofd: ofd})
	}
}

// Exit closes every descriptor the process owns.
func (p *Process) Exit() {
	h := p.host
	h.mu.Lock()
	if !p.alive {
		h.mu.Unlock()
		return
	}
	p.alive = false
	ofds := make([]*OpenFileDescription, 0, len(p.fds))
	for _, ofd := range p.fds {
		ofds = append(ofds, ofd)
	}
	p.fds = make(map[int]*OpenFileDescription)
	delete(h.procs, p.pid)
	h.mu.Unlock()
	seen := make(map[*File]bool)
	for _, ofd := range ofds {
		if !seen[ofd.file] {
			seen[ofd.file] = true
			ofd.file.unlockOwner(ownerKey{kind: POSIX, proc: p})
		}
		releaseOFD(ofd)
	}
}

func (p *Process) owner(fd int, kind Kind) (ownerKey, *File, bool) {
	h := p.host
	h.mu.Lock()
	defer h.mu.Unlock()
	ofd, ok := p.fds[fd]
	if !ok {
		return ownerKey{}, nil, false
	}
	if kind == POSIX {
		return ownerKey{kind: POSIX, proc: p}, ofd.file, true
	}
	return ownerKey{kind: OFD, ofd: ofd}, ofd.file, true
}

// Lock acquires a byte-range lock on [start, end), blocking until it
// is granted. Waiters are served in arrival order.
func (p *Process) Lock(fd int, kind Kind, typ LockType, start, end int64) bool {
	o, f, ok := p.owner(fd, kind)
	if !ok {
		return false
	}
	return f.lock(o, typ, start, end, true)
}

// TryLock is the non-blocking form of Lock.
func (p *Process) TryLock(fd int, kind Kind, typ LockType, start, end int64) bool {
	o, f, ok := p.owner(fd, kind)
	if !ok {
		return false
	}
	return f.lock(o, typ, start, end, false)
}

// Unlock releases [start, end), splitting held locks as needed.
func (p *Process) Unlock(fd int, kind Kind, start, end int64) {
	o, f, ok := p.owner(fd, kind)
	if !ok {
		return
	}
	f.unlock(o, start, end)
}
