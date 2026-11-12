package fdtab

import (
	"sync"
	"testing"
)

func mustOpen(t *testing.T, p *Process, f *File) int {
	t.Helper()
	fd, err := p.Open(f)
	if err != nil {
		t.Fatalf("Open(%s): %v", f.Name(), err)
	}
	return fd
}

func mustClose(t *testing.T, p *Process, fd int) {
	t.Helper()
	if err := p.Close(fd); err != nil {
		t.Fatalf("Close(%d): %v", fd, err)
	}
}

// A plugin closing its handle must not release a file the host still
// holds through another slot of the shared table.
func TestSharedTableCloseKeepsFileAlive(t *testing.T) {
	host := NewTable(8).Process()
	plugin := host.Clone(true)

	f := NewFile("sock")
	fd := mustOpen(t, host, f)
	keep, err := host.Dup(fd) // host keeps a second handle on the same file
	if err != nil {
		t.Fatal(err)
	}

	mustClose(t, plugin, fd) // plugin cleans up its handle
	if f.Released() {
		t.Fatal("plugin close released a file the host still references")
	}
	if f.Refs() != 1 {
		t.Fatalf("Refs = %d, want 1 (host's remaining slot)", f.Refs())
	}
	if _, ok := plugin.Lookup(keep); !ok {
		t.Fatal("host's surviving slot not visible through shared table")
	}
	mustClose(t, host, keep)
	if !f.Released() {
		t.Fatal("file not released after last slot closed")
	}
}

// Clone with share=false copies the table: both sides keep references to
// the same files, but slot changes no longer propagate.
func TestCloneCopyIndependence(t *testing.T) {
	host := NewTable(8).Process()
	f := NewFile("pipe")
	fd := mustOpen(t, host, f)

	plugin := host.Clone(false)
	if f.Refs() != 2 {
		t.Fatalf("Refs after copy-clone = %d, want 2", f.Refs())
	}

	mustClose(t, plugin, fd)
	if f.Released() || f.Refs() != 1 {
		t.Fatalf("after plugin close: refs=%d released=%v, want 1/false", f.Refs(), f.Released())
	}
	if _, ok := host.Lookup(fd); !ok {
		t.Fatal("host lost its slot after plugin closed its copy")
	}

	g := NewFile("tls")
	mustOpen(t, plugin, g)
	if fds := host.Table().FDs(); len(fds) != 1 {
		t.Fatalf("host table changed by plugin open: fds=%v", fds)
	}
}

// Unshare must produce a private snapshot: later mutations on either side
// are invisible to the other, and references are balanced.
func TestUnshareDetachesAtomically(t *testing.T) {
	host := NewTable(8).Process()
	a, b := NewFile("a"), NewFile("b")
	fda := mustOpen(t, host, a)
	fdb := mustOpen(t, host, b)
	if err := host.SetCloExec(fdb, true); err != nil {
		t.Fatal(err)
	}

	plugin := host.Clone(true)
	plugin.Unshare()

	if host.Table() == plugin.Table() {
		t.Fatal("Unshare did not detach the table")
	}
	if a.Refs() != 2 || b.Refs() != 2 {
		t.Fatalf("refs after unshare: a=%d b=%d, want 2/2", a.Refs(), b.Refs())
	}
	// The snapshot preserved flags.
	snap := plugin.Table().Snapshot()
	if !snap[fdb].CloExec || snap[fda].CloExec {
		t.Fatalf("snapshot flags wrong: %+v", snap)
	}

	// Mutations after unshare are private.
	mustClose(t, plugin, fda)
	if _, ok := host.Lookup(fda); !ok {
		t.Fatal("host lost slot after plugin closed its detached copy")
	}
	if a.Refs() != 1 {
		t.Fatalf("a refs = %d, want 1", a.Refs())
	}
	plugin.Exec() // closes only the cloexec slot in the plugin's table
	if _, ok := host.Lookup(fdb); !ok {
		t.Fatal("plugin exec closed host's slot")
	}
	if b.Refs() != 1 {
		t.Fatalf("b refs = %d, want 1", b.Refs())
	}
}

// The close-on-exec flag belongs to the slot, not the file: dup clears it,
// and setting it on one slot leaves sibling slots alone.
func TestCloExecIsPerSlot(t *testing.T) {
	p := NewTable(8).Process()
	f := NewFile("db")
	fd0 := mustOpen(t, p, f)
	if err := p.SetCloExec(fd0, true); err != nil {
		t.Fatal(err)
	}

	fd1, err := p.Dup(fd0)
	if err != nil {
		t.Fatal(err)
	}
	snap := p.Table().Snapshot()
	if !snap[fd0].CloExec {
		t.Fatal("original slot lost its cloexec flag")
	}
	if snap[fd1].CloExec {
		t.Fatal("dup inherited cloexec flag")
	}

	if err := p.SetCloExec(fd1, true); err != nil {
		t.Fatal(err)
	}
	if err := p.SetCloExec(fd0, false); err != nil {
		t.Fatal(err)
	}
	snap = p.Table().Snapshot()
	if snap[fd0].CloExec || !snap[fd1].CloExec {
		t.Fatalf("flag change leaked across slots: %+v", snap)
	}

	p.Exec()
	if _, ok := p.Lookup(fd0); !ok {
		t.Fatal("exec closed a slot whose flag was cleared")
	}
	if _, ok := p.Lookup(fd1); ok {
		t.Fatal("exec kept a slot whose flag was set")
	}
	if f.Refs() != 1 {
		t.Fatalf("refs after exec = %d, want 1", f.Refs())
	}
}

// Descriptor numbers of closed slots are reused, lowest first.
func TestFDNumberReuse(t *testing.T) {
	p := NewTable(4).Process()
	files := []*File{NewFile("f0"), NewFile("f1"), NewFile("f2"), NewFile("f3")}
	var fds []int
	for _, f := range files {
		fds = append(fds, mustOpen(t, p, f))
	}
	for i, fd := range fds {
		if fd != i {
			t.Fatalf("sequential open: got fd %d, want %d", fd, i)
		}
	}
	if _, err := p.Open(NewFile("overflow")); err != ErrTooManyFiles {
		t.Fatalf("full table open err = %v, want ErrTooManyFiles", err)
	}

	mustClose(t, p, 1)
	mustClose(t, p, 2)
	if got := mustOpen(t, p, NewFile("g0")); got != 1 {
		t.Fatalf("reuse: got %d, want 1 (lowest free)", got)
	}
	if got := mustOpen(t, p, NewFile("g1")); got != 2 {
		t.Fatalf("reuse: got %d, want 2", got)
	}

	// Dup also allocates the lowest free number.
	mustClose(t, p, 0)
	if got, err := p.Dup(1); err != nil || got != 0 {
		t.Fatalf("Dup = (%d, %v), want (0, nil)", got, err)
	}
}

// Dup2 replaces the target slot, dropping its file reference, and clears
// the target's cloexec flag.
func TestDup2ReplacesTarget(t *testing.T) {
	p := NewTable(4).Process()
	src, victim := NewFile("src"), NewFile("victim")
	fdSrc := mustOpen(t, p, src)
	fdVictim := mustOpen(t, p, victim)
	if err := p.SetCloExec(fdVictim, true); err != nil {
		t.Fatal(err)
	}

	if err := p.Dup2(fdSrc, fdVictim); err != nil {
		t.Fatal(err)
	}
	if !victim.Released() {
		t.Fatal("dup2 did not release the evicted file")
	}
	if src.Refs() != 2 {
		t.Fatalf("src refs = %d, want 2", src.Refs())
	}
	snap := p.Table().Snapshot()
	if snap[fdVictim].File != src || snap[fdVictim].CloExec {
		t.Fatalf("dup2 target wrong: %+v", snap[fdVictim])
	}
	if err := p.Dup2(fdSrc, 9); err != ErrBadFD {
		t.Fatalf("dup2 out of range err = %v, want ErrBadFD", err)
	}
	if err := p.Dup2(3, fdSrc); err != ErrBadFD {
		t.Fatalf("dup2 from empty slot err = %v, want ErrBadFD", err)
	}
}

// Concurrent unshare and mutation must be race-free and leave reference
// counts consistent with the final slot occupancy.
func TestConcurrentUnshareConsistency(t *testing.T) {
	host := NewTable(16).Process()
	seed := NewFile("seed")
	mustOpen(t, host, seed)

	const workers = 8
	procs := make([]*Process, workers)
	for i := range procs {
		procs[i] = host.Clone(true)
	}

	var wg sync.WaitGroup
	for i, p := range procs {
		wg.Add(1)
		go func(i int, p *Process) {
			defer wg.Done()
			for n := 0; n < 200; n++ {
				switch n % 5 {
				case 0:
					p.Unshare()
				case 1:
					fd, err := p.Open(NewFile("tmp"))
					if err == nil {
						_ = p.SetCloExec(fd, true)
					}
				case 2:
					p.Exec()
				case 3:
					if fd, err := p.Dup(0); err == nil {
						_ = p.Close(fd)
					}
				default:
					p.Table().Snapshot()
				}
			}
		}(i, p)
	}
	wg.Wait()

	// Global invariant: every live file's refcount equals the number of
	// slots pointing at it across all tables.
	counts := map[*File]int{}
	for _, p := range append(procs, host) {
		for _, info := range p.Table().Snapshot() {
			counts[info.File]++
		}
	}
	for f, want := range counts {
		if f.Refs() != want {
			t.Errorf("file %q: refs=%d, slots=%d", f.Name(), f.Refs(), want)
		}
		if f.Released() {
			t.Errorf("file %q released while still referenced", f.Name())
		}
	}
}
