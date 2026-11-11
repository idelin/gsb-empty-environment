package netsec

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func heldLocks(f *File) []fileLock {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fileLock(nil), f.locks...)
}

func lockStrings(f *File) []string {
	var out []string
	for _, l := range heldLocks(f) {
		out = append(out, l.String())
	}
	return out
}

func queuedLen(f *File) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.queue)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 2000; i++ {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func TestSplitMerge(t *testing.T) {
	h := NewHost()
	f := h.File("f")
	p := h.Spawn()
	fd := p.Open(f)
	owner := fmt.Sprintf("ofd#%d", 0)

	p.Lock(fd, OFD, Read, 0, 2)
	p.Lock(fd, OFD, Read, 2, 4) // adjacent same type: merge
	got := lockStrings(f)
	want := []string{owner + " R [0,4)"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("merge: got %v want %v", got, want)
	}

	p.Lock(fd, OFD, Write, 1, 3) // upgrade middle: split into three
	got = lockStrings(f)
	want = []string{owner + " R [0,1)", owner + " W [1,3)", owner + " R [3,4)"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("split: got %v want %v", got, want)
	}

	p.Unlock(fd, OFD, 1, 3) // hole in the middle
	got = lockStrings(f)
	want = []string{owner + " R [0,1)", owner + " R [3,4)"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("unlock middle: got %v want %v", got, want)
	}

	p.Lock(fd, OFD, Read, 1, 3) // fill the hole: merge all
	got = lockStrings(f)
	want = []string{owner + " R [0,4)"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("re-merge: got %v want %v", got, want)
	}
}

func TestPOSIXCloseAnyFDReleasesProcessLocks(t *testing.T) {
	h := NewHost()
	f := h.File("f")
	p := h.Spawn()
	fd1 := p.Open(f)
	fd2 := p.Open(f) // second open: independent description, same file

	p.Lock(fd1, POSIX, Write, 0, 4)
	p.Lock(fd1, OFD, Write, 4, 8)

	p.Close(fd2) // POSIX quirk: any close on the file drops process locks
	if n := len(heldLocks(f)); n != 1 {
		t.Fatalf("expected only the OFD lock to survive, got %v", lockStrings(f))
	}

	q := h.Spawn()
	fq := q.Open(f)
	if !q.TryLock(fq, POSIX, Write, 0, 4) {
		t.Fatal("POSIX lock should be gone after closing any fd on the file")
	}
	if q.TryLock(fq, OFD, Write, 4, 8) {
		t.Fatal("OFD lock must survive closing an unrelated descriptor")
	}

	q.Unlock(fq, POSIX, 0, 4)
	p.Close(fd1) // last reference to fd1's description
	if n := len(heldLocks(f)); n != 0 {
		t.Fatalf("expected empty lock table, got %v", lockStrings(f))
	}
}

func TestPOSIXDupCloseQuirk(t *testing.T) {
	h := NewHost()
	f := h.File("f")
	p := h.Spawn()
	fd1 := p.Open(f)
	p.Lock(fd1, POSIX, Write, 0, 4)

	fd2 := p.Dup(fd1)
	p.Close(fd2) // closing the duplicate still drops POSIX locks

	q := h.Spawn()
	fq := q.Open(f)
	if !q.TryLock(fq, POSIX, Write, 0, 4) {
		t.Fatal("POSIX locks must be dropped when any fd on the file closes")
	}
}

func TestOFDDupSemantics(t *testing.T) {
	h := NewHost()
	f := h.File("f")
	p := h.Spawn()
	fd1 := p.Open(f)
	p.Lock(fd1, OFD, Write, 0, 4)

	fd2 := p.Dup(fd1)
	p.Close(fd1) // one reference remains: lock stays

	q := h.Spawn()
	fq := q.Open(f)
	if q.TryLock(fq, OFD, Write, 0, 4) {
		t.Fatal("OFD lock must survive closing one of several duplicates")
	}

	p.Unlock(fd2, OFD, 0, 4) // duplicate shares the same owner
	if !q.TryLock(fq, OFD, Write, 0, 4) {
		t.Fatal("unlock via duplicate descriptor should release the OFD lock")
	}
}

func TestForkInheritance(t *testing.T) {
	t.Run("POSIX not inherited", func(t *testing.T) {
		h := NewHost()
		f := h.File("f")
		parent := h.Spawn()
		fd := parent.Open(f)
		parent.Lock(fd, POSIX, Write, 0, 4)

		child := parent.Fork()
		cfd := child.Open(f)
		if child.TryLock(cfd, POSIX, Write, 0, 4) {
			t.Fatal("child must conflict with the parent's POSIX lock")
		}
		parent.Exit() // lock dies with the parent, child never owned it
		if !child.TryLock(cfd, POSIX, Write, 0, 4) {
			t.Fatal("POSIX lock must be released when the owner exits")
		}
	})

	t.Run("OFD shared with child", func(t *testing.T) {
		h := NewHost()
		f := h.File("f")
		parent := h.Spawn()
		fd := parent.Open(f)
		parent.Lock(fd, OFD, Write, 0, 4)

		child := parent.Fork() // inherits a descriptor on the same OFD
		third := h.Spawn()
		ft := third.Open(f)

		parent.Exit() // child still references the description
		if third.TryLock(ft, OFD, Write, 0, 4) {
			t.Fatal("OFD lock must survive the parent's exit via the child")
		}
		if n := len(heldLocks(f)); n != 1 {
			t.Fatalf("expected the OFD lock to persist, got %v", lockStrings(f))
		}

		child.Exit() // last reference gone
		if !third.TryLock(ft, OFD, Write, 0, 4) {
			t.Fatal("OFD lock must be released after the last reference closes")
		}
	})

	t.Run("child close does not release", func(t *testing.T) {
		h := NewHost()
		f := h.File("f")
		parent := h.Spawn()
		fd := parent.Open(f)
		parent.Lock(fd, OFD, Write, 0, 4)

		child := parent.Fork()
		child.Exit() // child closes its inherited descriptor

		third := h.Spawn()
		ft := third.Open(f)
		if third.TryLock(ft, OFD, Write, 0, 4) {
			t.Fatal("child closing its copy must not release the parent's OFD lock")
		}
	})

	t.Run("child shares OFD owner", func(t *testing.T) {
		h := NewHost()
		f := h.File("f")
		parent := h.Spawn()
		fd := parent.Open(f)
		parent.Lock(fd, OFD, Write, 0, 4)

		child := parent.Fork()
		// The inherited descriptor is the same owner: no self-conflict.
		if !child.TryLock(fd, OFD, Write, 0, 4) {
			t.Fatal("child must share the parent's OFD lock ownership")
		}
	})
}

func TestUpgradeIsAtomic(t *testing.T) {
	h := NewHost()
	f := h.File("f")
	a, b := h.Spawn(), h.Spawn()
	fa, fb := a.Open(f), b.Open(f)
	a.Lock(fa, OFD, Read, 0, 4)
	b.Lock(fb, OFD, Read, 0, 4)

	done := make(chan struct{})
	go func() {
		a.Lock(fa, OFD, Write, 0, 4) // upgrade; blocks on b's read
		close(done)
	}()
	waitFor(t, "a to queue its upgrade", func() bool { return queuedLen(f) == 1 })

	// While the upgrade waits, a's read lock must still be in force.
	c := h.Spawn()
	fc := c.Open(f)
	if !c.TryLock(fc, OFD, Read, 0, 4) {
		t.Fatal("a's read lock vanished while its upgrade was blocked")
	}
	if c.TryLock(fc, OFD, Write, 0, 4) {
		t.Fatal("write must not be granted while readers hold the range")
	}

	b.Unlock(fb, OFD, 0, 4)
	// c still holds a read lock, so the upgrade must keep waiting.
	if n := queuedLen(f); n != 1 {
		t.Fatalf("upgrade released early while c holds a read lock")
	}
	for _, l := range heldLocks(f) {
		if l.owner.ofd == a.fds[fa] && l.typ == Write {
			t.Fatal("upgrade granted while c still holds a read lock")
		}
	}

	c.Unlock(fc, OFD, 0, 4)
	<-done
	got := lockStrings(f)
	want := []string{"ofd#0 W [0,4)"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("after upgrade: got %v want %v", got, want)
	}
}

func TestBlockingFIFO(t *testing.T) {
	h := NewHost()
	f := h.File("f")
	a := h.Spawn()
	fa := a.Open(f)
	a.Lock(fa, OFD, Write, 0, 10)

	order := make(chan int, 3)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		i := i
		p := h.Spawn()
		fd := p.Open(f)
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.Lock(fd, OFD, Write, 0, 10)
			order <- i
			p.Unlock(fd, OFD, 0, 10)
		}()
		waitFor(t, fmt.Sprintf("waiter %d to queue", i), func() bool {
			return queuedLen(f) == i+1
		})
	}

	a.Unlock(fa, OFD, 0, 10)
	wg.Wait()
	for i := 0; i < 3; i++ {
		if got := <-order; got != i {
			t.Fatalf("wake order violated: position %d went to waiter %d", i, got)
		}
	}
}

func TestPartialUnlockKeepsWaiterBlocked(t *testing.T) {
	h := NewHost()
	f := h.File("f")
	a := h.Spawn()
	fa := a.Open(f)
	a.Lock(fa, OFD, Write, 0, 4)

	b := h.Spawn()
	fb := b.Open(f)
	granted := make(chan struct{})
	go func() {
		b.Lock(fb, OFD, Write, 1, 3)
		close(granted)
	}()
	waitFor(t, "b to queue", func() bool { return queuedLen(f) == 1 })

	a.Unlock(fa, OFD, 0, 1) // frees [0,1); b wants [1,3), still covered
	if n := queuedLen(f); n != 1 {
		t.Fatal("waiter released early by a partial unlock")
	}
	a.Unlock(fa, OFD, 3, 4) // frees [3,4); [1,3) still held
	if n := queuedLen(f); n != 1 {
		t.Fatal("waiter released early by a partial unlock")
	}
	a.Unlock(fa, OFD, 1, 3)
	<-granted
}

func TestFIFOMixedReadWrite(t *testing.T) {
	h := NewHost()
	f := h.File("f")
	a := h.Spawn()
	fa := a.Open(f)
	a.Lock(fa, OFD, Write, 0, 2)

	b := h.Spawn()
	fb := b.Open(f)
	bDone := make(chan struct{})
	go func() {
		b.Lock(fb, OFD, Write, 0, 2) // head waiter
		close(bDone)
	}()
	waitFor(t, "b to queue", func() bool { return queuedLen(f) == 1 })

	c := h.Spawn()
	fc := c.Open(f)
	cDone := make(chan struct{})
	go func() {
		c.Lock(fc, OFD, Read, 0, 2) // queues behind b
		close(cDone)
	}()
	waitFor(t, "c to queue", func() bool { return queuedLen(f) == 2 })

	a.Unlock(fa, OFD, 0, 2) // wakes only the head: b
	<-bDone
	if n := queuedLen(f); n != 1 {
		t.Fatal("reader overtook the head-of-line writer")
	}
	got := lockStrings(f)
	want := []string{"ofd#1 W [0,2)"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("after handoff: got %v want %v", got, want)
	}

	b.Unlock(fb, OFD, 0, 2)
	<-cDone
}
