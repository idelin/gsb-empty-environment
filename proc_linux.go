//go:build linux

package netsec

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

// nsEntry is the recorded identity of a PID namespace: the inode of its
// nsfs file and its nesting depth (number of entries in the NSpid field
// of any process living directly in it).
type nsEntry struct {
	inode uint64
	depth int
}

// LinuxProcTable resolves processes through /proc. The host must register
// every sandbox PID namespace with RegisterNamespace before callers from
// that namespace are served.
//
// Visibility rule: a target is visible from a registered namespace when it
// lives in that exact namespace (inode match) or in a descendant namespace
// (deeper NSpid chain). The descendant check assumes the registered
// namespaces form a single ancestor chain, which holds for a sandbox host
// nesting containers linearly.
type LinuxProcTable struct {
	procRoot string
	mu       sync.RWMutex
	ns       map[uint64]nsEntry
}

// NewLinuxProcTable returns a ProcTable backed by /proc.
func NewLinuxProcTable() *LinuxProcTable {
	return &LinuxProcTable{procRoot: "/proc", ns: make(map[uint64]nsEntry)}
}

// NamespaceID returns a stable identifier for the PID namespace pid lives
// in, derived from the inode of /proc/<pid>/ns/pid.
func (t *LinuxProcTable) NamespaceID(pid int) (uint64, error) {
	return t.nsInode(pid)
}

// RegisterNamespace records the namespace nsID so Lookup can test
// visibility from it. pid must be a process living directly in that
// namespace (e.g. the container init).
func (t *LinuxProcTable) RegisterNamespace(nsID uint64, pid int) error {
	inode, err := t.nsInode(pid)
	if err != nil {
		return err
	}
	_, nspid, err := t.readStatus(pid)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ns[nsID] = nsEntry{inode: inode, depth: len(nspid)}
	return nil
}

// Lookup implements ProcTable.
func (t *LinuxProcTable) Lookup(pid int, nsID uint64) (ProcInfo, bool) {
	t.mu.RLock()
	ns, registered := t.ns[nsID]
	t.mu.RUnlock()
	if !registered {
		return ProcInfo{}, false
	}

	start, err := t.readStartTime(pid)
	if err != nil {
		return ProcInfo{}, false
	}
	uid, nspid, err := t.readStatus(pid)
	if err != nil {
		return ProcInfo{}, false
	}
	inode, err := t.nsInode(pid)
	if err != nil {
		return ProcInfo{}, false
	}

	// Visible only from the target's own namespace or an ancestor.
	if inode != ns.inode && len(nspid) <= ns.depth {
		return ProcInfo{}, false
	}
	if len(nspid) < ns.depth {
		return ProcInfo{}, false
	}
	return ProcInfo{PID: nspid[ns.depth-1], StartTime: start, UID: uid}, true
}

func (t *LinuxProcTable) nsInode(pid int) (uint64, error) {
	link, err := os.Readlink(fmt.Sprintf("%s/%d/ns/pid", t.procRoot, pid))
	if err != nil {
		return 0, err
	}
	var inode uint64
	if _, err := fmt.Sscanf(link, "pid:[%d]", &inode); err != nil {
		return 0, fmt.Errorf("parse %q: %w", link, err)
	}
	return inode, nil
}

// readStartTime extracts field 22 (starttime) from /proc/<pid>/stat. The
// comm field may contain spaces and parentheses, so parsing resumes after
// the last ')'.
func (t *LinuxProcTable) readStartTime(pid int) (uint64, error) {
	raw, err := os.ReadFile(fmt.Sprintf("%s/%d/stat", t.procRoot, pid))
	if err != nil {
		return 0, err
	}
	s := string(raw)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0, fmt.Errorf("malformed stat for pid %d", pid)
	}
	fields := strings.Fields(s[i+1:])
	// fields[0] is field 3 (state); starttime is field 22.
	if len(fields) <= 19 {
		return 0, fmt.Errorf("short stat for pid %d", pid)
	}
	return strconv.ParseUint(fields[19], 10, 64)
}

// readStatus returns the real uid and the NSpid chain from
// /proc/<pid>/status.
func (t *LinuxProcTable) readStatus(pid int) (uint32, []int, error) {
	raw, err := os.ReadFile(fmt.Sprintf("%s/%d/status", t.procRoot, pid))
	if err != nil {
		return 0, nil, err
	}
	var uid uint32
	var nspid []int
	for _, line := range strings.Split(string(raw), "\n") {
		switch {
		case strings.HasPrefix(line, "Uid:"):
			f := strings.Fields(line)
			if len(f) < 2 {
				return 0, nil, fmt.Errorf("malformed Uid line for pid %d", pid)
			}
			v, err := strconv.ParseUint(f[1], 10, 32)
			if err != nil {
				return 0, nil, err
			}
			uid = uint32(v)
		case strings.HasPrefix(line, "NSpid:"):
			for _, tok := range strings.Fields(line)[1:] {
				v, err := strconv.Atoi(tok)
				if err != nil {
					return 0, nil, err
				}
				nspid = append(nspid, v)
			}
		}
	}
	if len(nspid) == 0 {
		return 0, nil, fmt.Errorf("missing NSpid for pid %d", pid)
	}
	return uid, nspid, nil
}
