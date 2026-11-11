package netsec

import (
	"fmt"
	"sort"
	"testing"
)

// The exhaustive test drives two tenant processes through every
// operation sequence of bounded depth over a two-byte file, cloning
// the host per branch, and checks the lock-table invariants after
// every single step: no conflicting locks coexist, same-owner ranges
// stay split/merged into normal form, and no lock outlives its owner
// (child close, duplicate release, exit and upgrade included).

type exState struct {
	host   *Host
	tenant [2]int // tenant index -> pid
}

type exOp struct {
	name  string
	apply func(st *exState)
}

func smallestFD(p *Process) (int, bool) {
	best := -1
	for fd := range p.fds {
		if best < 0 || fd < best {
			best = fd
		}
	}
	return best, best >= 0
}

func sortedFDs(p *Process) []int {
	var fds []int
	for fd := range p.fds {
		fds = append(fds, fd)
	}
	sort.Ints(fds)
	return fds
}

func exOps(st *exState) []exOp {
	var ops []exOp
	for i := 0; i < 2; i++ {
		i := i
		p := st.host.procs[st.tenant[i]]
		_, hasFD := smallestFD(p)
		via := func(name string, fn func(p *Process, f *File)) exOp {
			return exOp{name, func(nst *exState) {
				fn(nst.host.procs[nst.tenant[i]], nst.host.File("f"))
			}}
		}
		if !hasFD {
			ops = append(ops, via(fmt.Sprintf("t%d.open", i),
				func(p *Process, f *File) { p.Open(f) }))
		} else {
			ops = append(ops, via(fmt.Sprintf("t%d.dup", i),
				func(p *Process, f *File) {
					fd, _ := smallestFD(p)
					p.Dup(fd)
				}))
			ops = append(ops, via(fmt.Sprintf("t%d.close", i),
				func(p *Process, f *File) {
					fd, _ := smallestFD(p)
					p.Close(fd)
				}))
			for _, kind := range []Kind{POSIX, OFD} {
				for _, typ := range []LockType{Read, Write} {
					for _, r := range [][2]int64{{0, 1}, {1, 2}, {0, 2}} {
						kind, typ, r := kind, typ, r
						ops = append(ops, via(
							fmt.Sprintf("t%d.lock.%s.%s.[%d,%d)", i, kind, typ, r[0], r[1]),
							func(p *Process, f *File) {
								fd, _ := smallestFD(p)
								p.TryLock(fd, kind, typ, r[0], r[1])
							}))
					}
				}
			}
			for _, kind := range []Kind{POSIX, OFD} {
				kind := kind
				ops = append(ops, via(fmt.Sprintf("t%d.unlock.%s", i, kind),
					func(p *Process, f *File) {
						fd, _ := smallestFD(p)
						p.Unlock(fd, kind, 0, 2)
					}))
			}
		}
		ops = append(ops, exOp{fmt.Sprintf("t%d.forkChildExit", i), func(nst *exState) {
			child := nst.host.procs[nst.tenant[i]].Fork()
			for _, fd := range sortedFDs(child) {
				child.Close(fd)
			}
			child.Exit()
		}})
		ops = append(ops, exOp{fmt.Sprintf("t%d.exit", i), func(nst *exState) {
			nst.host.procs[nst.tenant[i]].Exit()
			nst.tenant[i] = nst.host.Spawn().pid
		}})
	}
	return ops
}

// cloneHost deep-copies the host so each DFS branch gets an
// independent state. The wait queue must be empty: the exhaustive
// space only uses non-blocking requests.
func cloneHost(h *Host) *Host {
	nh := &Host{
		nextPID: h.nextPID,
		nextOFD: h.nextOFD,
		procs:   make(map[int]*Process, len(h.procs)),
		files:   make(map[string]*File, len(h.files)),
	}
	fileMap := make(map[*File]*File)
	for path, f := range h.files {
		nf := &File{path: f.path}
		nh.files[path] = nf
		fileMap[f] = nf
	}
	procMap := make(map[*Process]*Process)
	ofdMap := make(map[*OpenFileDescription]*OpenFileDescription)
	for _, p := range h.procs {
		np := &Process{
			host:   nh,
			pid:    p.pid,
			alive:  p.alive,
			nextFD: p.nextFD,
			fds:    make(map[int]*OpenFileDescription, len(p.fds)),
		}
		nh.procs[np.pid] = np
		procMap[p] = np
	}
	for _, p := range h.procs {
		for fd, ofd := range p.fds {
			nofd, ok := ofdMap[ofd]
			if !ok {
				nofd = &OpenFileDescription{
					id:   ofd.id,
					host: nh,
					file: fileMap[ofd.file],
					refs: ofd.refs,
				}
				ofdMap[ofd] = nofd
			}
			procMap[p].fds[fd] = nofd
		}
	}
	for _, f := range h.files {
		nf := fileMap[f]
		if len(f.queue) != 0 {
			panic("cloneHost: pending waiters are not supported")
		}
		nf.locks = make([]fileLock, len(f.locks))
		for i, l := range f.locks {
			nl := l
			if l.owner.kind == POSIX {
				nl.owner = ownerKey{kind: POSIX, proc: procMap[l.owner.proc]}
			} else {
				nl.owner = ownerKey{kind: OFD, ofd: ofdMap[l.owner.ofd]}
			}
			nf.locks[i] = nl
		}
	}
	return nh
}

func checkInvariants(t *testing.T, h *Host, path []string) {
	t.Helper()
	fail := func(format string, args ...interface{}) {
		t.Helper()
		args = append(args, path)
		t.Fatalf(format+"\nop sequence: %v", args...)
	}

	// Descriptor accounting: refs must equal the number of descriptors
	// pointing at each open file description.
	want := make(map[*OpenFileDescription]int)
	for _, p := range h.procs {
		for _, ofd := range p.fds {
			want[ofd]++
		}
	}
	for ofd, n := range want {
		if ofd.refs != n {
			fail("ofd#%d refs=%d, want %d", ofd.id, ofd.refs, n)
		}
	}

	for _, f := range h.files {
		locks := heldLocks(f)
		for i, l := range locks {
			if l.start >= l.end {
				fail("degenerate lock %v", l)
			}
			switch l.owner.kind {
			case POSIX:
				if !l.owner.proc.alive {
					fail("POSIX lock %v held by a dead process", l)
				}
			case OFD:
				if l.owner.ofd.refs <= 0 {
					fail("OFD lock %v on a description with no references", l)
				}
			}
			for j := i + 1; j < len(locks); j++ {
				m := locks[j]
				overlap := l.start < m.end && m.start < l.end
				adjacent := l.end == m.start || m.end == l.start
				if l.owner == m.owner {
					if overlap {
						fail("same-owner locks overlap: %v vs %v", l, m)
					}
					if adjacent && l.typ == m.typ {
						fail("mergeable locks not coalesced: %v vs %v", l, m)
					}
				} else if overlap && conflicts(l.typ, m.typ) {
					fail("conflicting locks held by different owners: %v vs %v", l, m)
				}
			}
		}
	}
}

func TestExhaustiveSmallRanges(t *testing.T) {
	const depth = 4
	var dfs func(st *exState, path []string, d int)
	dfs = func(st *exState, path []string, d int) {
		checkInvariants(t, st.host, path)
		if d == 0 {
			return
		}
		for _, op := range exOps(st) {
			nst := &exState{host: cloneHost(st.host), tenant: st.tenant}
			op.apply(nst)
			np := make([]string, len(path)+1)
			copy(np, path)
			np[len(path)] = op.name
			dfs(nst, np, d-1)
		}
	}
	h := NewHost()
	h.File("f")
	st := &exState{host: h, tenant: [2]int{h.Spawn().pid, h.Spawn().pid}}
	dfs(st, nil, depth)
}
