package fdtab

import (
	"fmt"
	"testing"
)

// This test exhaustively explores every reachable state of a two-process,
// three-slot configuration by breadth-first search over an independent
// oracle model. At every visited state the package under test is rebuilt
// by replaying the event history and checked against the oracle:
//
//   - every file's reference count equals the number of slots, across all
//     tables, that point at it, and it is released exactly when that count
//     reaches zero;
//   - close-on-exec flags are stored per slot and never leak between
//     slots, files, or tables;
//   - descriptor numbers are allocated lowest-first and reused after
//     close (checked structurally against the oracle's allocation);
//   - two processes share a table exactly when the oracle says they do.

const (
	exMaxFD    = 3
	exMaxState = 400000
)

// --- oracle ---------------------------------------------------------------

type oslot struct {
	file    int
	cloexec bool
}

type otable struct {
	slots map[int]oslot
}

func (t *otable) clone() *otable {
	cp := &otable{slots: make(map[int]oslot, len(t.slots))}
	for fd, s := range t.slots {
		cp.slots[fd] = s
	}
	return cp
}

func (t *otable) lowestFree() (int, bool) {
	for fd := 0; fd < exMaxFD; fd++ {
		if _, ok := t.slots[fd]; !ok {
			return fd, true
		}
	}
	return -1, false
}

type oracle struct {
	tbl [2]*otable
}

func (o *oracle) clone() *oracle {
	cp := &oracle{}
	for i, t := range o.tbl {
		if t == nil {
			continue
		}
		// Preserve sharing: identical pointers stay identical.
		for j := 0; j < i; j++ {
			if o.tbl[j] == t {
				cp.tbl[i] = cp.tbl[j]
			}
		}
		if cp.tbl[i] == nil {
			cp.tbl[i] = t.clone()
		}
	}
	return cp
}

// canonical encodes the observable state: which tables are shared, and
// each table's slots with file identities renamed in first-appearance
// order so that structurally equal states compare equal.
func (o *oracle) canonical() string {
	rename := map[int]int{}
	next := 0
	out := ""
	if o.tbl[0] == o.tbl[1] {
		out += "S|"
	} else {
		out += "P|"
	}
	for i := 0; i < 2; i++ {
		for fd := 0; fd < exMaxFD; fd++ {
			s, ok := o.tbl[i].slots[fd]
			if !ok {
				out += "."
				continue
			}
			id, seen := rename[s.file]
			if !seen {
				id = next
				rename[s.file] = id
				next++
			}
			ce := "0"
			if s.cloexec {
				ce = "1"
			}
			out += fmt.Sprintf("%d%s,", id, ce)
		}
		out += "|"
	}
	return out
}

// --- events ---------------------------------------------------------------

type event struct {
	op   string
	proc int
	a, b int
}

func allEvents() []event {
	var evs []event
	for p := 0; p < 2; p++ {
		evs = append(evs, event{"open", p, 0, 0})
		evs = append(evs, event{"exec", p, 0, 0})
		evs = append(evs, event{"unshare", p, 0, 0})
		for fd := 0; fd < exMaxFD; fd++ {
			evs = append(evs, event{"close", p, fd, 0})
			evs = append(evs, event{"dup", p, fd, 0})
			evs = append(evs, event{"cloexec", p, fd, 1})
			evs = append(evs, event{"cloexec", p, fd, 0})
			for nfd := 0; nfd < exMaxFD; nfd++ {
				evs = append(evs, event{"dup2", p, fd, nfd})
			}
		}
		for _, share := range []int{0, 1} {
			evs = append(evs, event{"reclone", p, share, 0})
		}
	}
	return evs
}

// --- world: package state + oracle, advanced in lockstep ------------------

type fileRec struct {
	f  *File
	id int
}

type world struct {
	procs  [2]*Process
	or     *oracle
	files  []*fileRec
	nextID int
}

func newWorld() *world {
	tbl := NewTable(exMaxFD)
	p0 := tbl.Process()
	ot := &otable{slots: map[int]oslot{}}
	w := &world{or: &oracle{}}
	w.procs[0] = p0
	w.procs[1] = p0.Clone(true)
	w.or.tbl[0] = ot
	w.or.tbl[1] = ot
	return w
}

// apply runs one event against both the package and the oracle, requiring
// that success and failure agree.
func (w *world) apply(t *testing.T, ev event) {
	t.Helper()
	p := w.procs[ev.proc]
	ot := w.or.tbl[ev.proc]
	switch ev.op {
	case "open":
		id := w.nextID
		w.nextID++
		f := NewFile(fmt.Sprintf("f%d", id))
		fd, err := p.Open(f)
		ofd, ok := ot.lowestFree()
		if !ok {
			if err != ErrTooManyFiles {
				t.Fatalf("open: err=%v, want ErrTooManyFiles", err)
			}
			return
		}
		if err != nil || fd != ofd {
			t.Fatalf("open: got (%d,%v), oracle wants (%d,nil)", fd, err, ofd)
		}
		ot.slots[ofd] = oslot{file: id}
		w.files = append(w.files, &fileRec{f: f, id: id})

	case "close":
		err := p.Close(ev.a)
		if _, ok := ot.slots[ev.a]; !ok {
			if err != ErrBadFD {
				t.Fatalf("close(%d): err=%v, want ErrBadFD", ev.a, err)
			}
			return
		}
		if err != nil {
			t.Fatalf("close(%d): %v", ev.a, err)
		}
		delete(ot.slots, ev.a)

	case "dup":
		nfd, err := p.Dup(ev.a)
		s, ok := ot.slots[ev.a]
		if !ok {
			if err != ErrBadFD {
				t.Fatalf("dup(%d): err=%v, want ErrBadFD", ev.a, err)
			}
			return
		}
		ofd, free := ot.lowestFree()
		if !free {
			if err != ErrTooManyFiles {
				t.Fatalf("dup(%d): err=%v, want ErrTooManyFiles", ev.a, err)
			}
			return
		}
		if err != nil || nfd != ofd {
			t.Fatalf("dup: got (%d,%v), oracle wants (%d,nil)", nfd, err, ofd)
		}
		ot.slots[ofd] = oslot{file: s.file} // dup clears cloexec

	case "dup2":
		err := p.Dup2(ev.a, ev.b)
		s, ok := ot.slots[ev.a]
		if !ok {
			if err != ErrBadFD {
				t.Fatalf("dup2(%d,%d): err=%v, want ErrBadFD", ev.a, ev.b, err)
			}
			return
		}
		if err != nil {
			t.Fatalf("dup2(%d,%d): %v", ev.a, ev.b, err)
		}
		if ev.a != ev.b {
			delete(ot.slots, ev.b)
			ot.slots[ev.b] = oslot{file: s.file} // dup2 clears cloexec
		}

	case "cloexec":
		err := p.SetCloExec(ev.a, ev.b == 1)
		s, ok := ot.slots[ev.a]
		if !ok {
			if err != ErrBadFD {
				t.Fatalf("cloexec(%d): err=%v, want ErrBadFD", ev.a, err)
			}
			return
		}
		if err != nil {
			t.Fatalf("cloexec(%d): %v", ev.a, err)
		}
		s.cloexec = ev.b == 1
		ot.slots[ev.a] = s

	case "exec":
		p.Exec()
		for fd, s := range ot.slots {
			if s.cloexec {
				delete(ot.slots, fd)
			}
		}

	case "unshare":
		p.Unshare()
		w.or.tbl[ev.proc] = ot.clone()

	case "reclone":
		w.procs[1-ev.proc].Exit() // replaced process leaves its table
		child := p.Clone(ev.a == 1)
		w.procs[1-ev.proc] = child
		if ev.a == 1 {
			w.or.tbl[1-ev.proc] = w.or.tbl[ev.proc]
		} else {
			w.or.tbl[1-ev.proc] = w.or.tbl[ev.proc].clone()
		}

	default:
		t.Fatalf("unknown op %q", ev.op)
	}
}

// check verifies the package state against the oracle and the reference
// counting invariants.
func (w *world) check(t *testing.T, seq []event) {
	t.Helper()
	at := func() string { return fmt.Sprintf(" after %v", seq) }

	if got, want := w.procs[0].Table() == w.procs[1].Table(),
		w.or.tbl[0] == w.or.tbl[1]; got != want {
		t.Fatalf("table sharing = %v, oracle wants %v%s", got, want, at())
	}

	wantRefs := map[int]int{}
	counted := map[*Table]bool{}
	for i := 0; i < 2; i++ {
		snap := w.procs[i].Table().Snapshot()
		if len(snap) != len(w.or.tbl[i].slots) {
			t.Fatalf("proc %d: %d slots, oracle wants %d%s",
				i, len(snap), len(w.or.tbl[i].slots), at())
		}
		for fd, s := range w.or.tbl[i].slots {
			info, ok := snap[fd]
			if !ok {
				t.Fatalf("proc %d: fd %d missing%s", i, fd, at())
			}
			rec := w.files[s.file]
			if info.File != rec.f {
				t.Fatalf("proc %d fd %d: wrong file, want %s%s",
					i, fd, rec.f.Name(), at())
			}
			if info.CloExec != s.cloexec {
				t.Fatalf("proc %d fd %d: cloexec=%v, oracle wants %v%s",
					i, fd, info.CloExec, s.cloexec, at())
			}
		}
		// A table shared by both processes holds each file reference
		// once; count unique tables only.
		if tbl := w.procs[i].Table(); !counted[tbl] {
			counted[tbl] = true
			for _, s := range w.or.tbl[i].slots {
				wantRefs[s.file]++
			}
		}
	}

	for _, rec := range w.files {
		want := wantRefs[rec.id]
		if rec.f.Refs() != want {
			t.Fatalf("file %s: refs=%d, slots=%d%s",
				rec.f.Name(), rec.f.Refs(), want, at())
		}
		if rec.f.Released() != (want == 0) {
			t.Fatalf("file %s: released=%v, refs=%d%s",
				rec.f.Name(), rec.f.Released(), want, at())
		}
	}
}

// --- exhaustive search ------------------------------------------------------

type node struct {
	or  *oracle
	seq []event
}

func TestExhaustiveSmallTable(t *testing.T) {
	events := allEvents()

	start := &oracle{}
	shared := &otable{slots: map[int]oslot{}}
	start.tbl[0], start.tbl[1] = shared, shared

	seen := map[string]bool{start.canonical(): true}
	queue := []*node{{or: start}}
	expanded := 0

	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		expanded++
		if expanded > exMaxState {
			t.Fatalf("state budget %d exceeded", exMaxState)
		}

		for _, ev := range events {
			// Rebuild the package state by replaying history, then
			// apply the new event on a fresh oracle copy.
			w := newWorld()
			for _, prev := range n.seq {
				w.apply(t, prev)
			}
			w.or = n.or.clone()
			w.apply(t, ev)
			seq := append(append([]event{}, n.seq...), ev)
			w.check(t, seq)

			key := w.or.canonical()
			if !seen[key] {
				seen[key] = true
				queue = append(queue, &node{or: w.or, seq: seq})
			}
		}
	}
	t.Logf("exhausted %d distinct states, %d event applications verified",
		len(seen), expanded*len(events))
}
