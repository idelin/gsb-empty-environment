package publisher

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"
)

var (
	oldGen = []Update{
		{Name: "a.txt", Data: []byte("alpha-v1")},
		{Name: "b.txt", Data: []byte("bravo-v1")},
		{Name: "c.txt", Data: []byte("charlie-v1")},
	}
	newGen = []Update{
		{Name: "a.txt", Data: []byte("alpha-v2")},
		{Name: "b.txt", Data: []byte("bravo-v2")},
		{Name: "c.txt", Data: []byte("charlie-v2")},
	}
)

// setup returns a publisher with oldGen installed as version 1.
func setup(t *testing.T) (*MemFS, *FaultFS, *Publisher) {
	t.Helper()
	mem := NewMemFS()
	ff := NewFaultFS(mem)
	pub := New(ff)
	if err := pub.Publish(oldGen); err != nil {
		t.Fatalf("seed publish: %v", err)
	}
	ff.ResetArming()
	return mem, ff, pub
}

// checkConsistent asserts the visible state is exactly one complete
// generation: either all of wantOld or all of wantNew, with every blob
// referenced by the manifest present and checksum-valid.
func checkConsistent(t *testing.T, pub *Publisher) {
	t.Helper()
	snap, err := pub.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snap.Version() != 1 && snap.Version() != 2 {
		t.Fatalf("unexpected manifest version %d", snap.Version())
	}
	want := oldGen
	if snap.Version() == 2 {
		want = newGen
	}
	if len(snap.Names()) != len(want) {
		t.Fatalf("version %d: got files %v, want %d entries", snap.Version(), snap.Names(), len(want))
	}
	for _, u := range want {
		got, ok, err := snap.Get(u.Name)
		if err != nil {
			t.Fatalf("version %d: get %q: %v", snap.Version(), u.Name, err)
		}
		if !ok {
			t.Fatalf("version %d: missing %q", snap.Version(), u.Name)
		}
		if !bytes.Equal(got, u.Data) {
			t.Fatalf("version %d: %q = %q, want %q (mixed generation!)", snap.Version(), u.Name, got, u.Data)
		}
	}
}

func TestPublishAndRead(t *testing.T) {
	_, _, pub := setup(t)
	if err := pub.Publish(newGen); err != nil {
		t.Fatalf("publish: %v", err)
	}
	checkConsistent(t, pub)
	snap, _ := pub.Snapshot()
	if snap.Version() != 2 {
		t.Fatalf("version = %d, want 2", snap.Version())
	}
}

// TestCrashEnumeration crashes the machine at every mutating-operation cut
// point of a publish, then verifies recovery leaves exactly one consistent
// generation visible and is idempotent.
func TestCrashEnumeration(t *testing.T) {
	// First, count the cut points of an undisturbed publish.
	_, ff, pub := setup(t)
	if err := pub.Publish(newGen); err != nil {
		t.Fatalf("publish: %v", err)
	}
	total := ff.Count()
	t.Logf("publish has %d mutating-op cut points:", total)
	for i, op := range ff.Log {
		t.Logf("  #%d %s", i+1, op)
	}

	for cut := 1; cut <= total; cut++ {
		t.Run(fmt.Sprintf("cut_%02d", cut), func(t *testing.T) {
			_, ff, pub := setup(t)
			ff.CrashAt(cut)
			err := pub.Publish(newGen)
			// A crash during best-effort GC (after checkpoint) is swallowed
			// by design, so publish may still report success.
			if err != nil && !errors.Is(err, ErrCrashed) {
				t.Fatalf("publish err = %v, want nil or ErrCrashed", err)
			}
			ff.Restart()
			if err := pub.Recover(); err != nil {
				t.Fatalf("recover: %v", err)
			}
			checkConsistent(t, pub)
			// Recovery must be idempotent.
			if err := pub.Recover(); err != nil {
				t.Fatalf("recover #2: %v", err)
			}
			checkConsistent(t, pub)
			// The system must accept new publishes afterwards.
			if err := pub.Publish([]Update{{Name: "d.txt", Data: []byte("delta-v3")}}); err != nil {
				t.Fatalf("post-recovery publish: %v", err)
			}
		})
	}
}

// TestFailureEnumeration injects an I/O error (no crash) at every cut point.
// The publish must fail cleanly and recovery must restore a consistent state.
func TestFailureEnumeration(t *testing.T) {
	_, ff, pub := setup(t)
	if err := pub.Publish(newGen); err != nil {
		t.Fatalf("publish: %v", err)
	}
	total := ff.Count()

	for cut := 1; cut <= total; cut++ {
		t.Run(fmt.Sprintf("fail_%02d", cut), func(t *testing.T) {
			_, ff, pub := setup(t)
			ff.FailAt(cut, nil)
			// Failures during best-effort GC are swallowed by design.
			_ = pub.Publish(newGen)
			if err := pub.Recover(); err != nil {
				t.Fatalf("recover: %v", err)
			}
			checkConsistent(t, pub)
		})
	}
}

// TestTornJournalTail verifies recovery ignores a partially flushed commit
// record and rolls back.
func TestTornJournalTail(t *testing.T) {
	mem, _, pub := setup(t)

	// Hand-craft a journal: begin+put durable, commit record torn in half.
	full, err := encodeRecords([]journalRecord{
		{Type: recBegin, Txn: 2},
		{Type: recPut, Txn: 2, Name: "a.txt", Data: []byte("alpha-v2")},
		{Type: recCommit, Txn: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	torn := full[:len(full)-5] // cut the commit frame mid-payload
	if err := writeFileSync(mem, journalPath, torn); err != nil {
		t.Fatal(err)
	}
	if err := mem.SyncDir("."); err != nil {
		t.Fatal(err)
	}

	if err := pub.Recover(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	snap, _ := pub.Snapshot()
	if snap.Version() != 1 {
		t.Fatalf("version = %d, want rollback to 1", snap.Version())
	}
	checkConsistent(t, pub)
}

// TestReaderSeesConsistentGeneration snapshots the state after every single
// filesystem operation of a concurrent publish; a reader must always observe
// one complete generation.
func TestReaderSeesConsistentGeneration(t *testing.T) {
	mem := NewMemFS()
	ff := NewFaultFS(mem)
	pub := New(ff)
	if err := pub.Publish(oldGen); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ff.ResetArming()

	// Interleave: run the publish one op at a time by arming a crash after
	// each op is too destructive; instead hook via a wrapping FS that
	// snapshots after each mutating op.
	obs := &observeFS{inner: ff, afterEach: func() {
		snap, err := pub.Snapshot()
		if err != nil {
			t.Errorf("snapshot: %v", err)
			return
		}
		want := oldGen
		if snap.Version() == 2 {
			want = newGen
		} else if snap.Version() != 1 {
			t.Errorf("unexpected version %d", snap.Version())
			return
		}
		for _, u := range want {
			got, ok, err := snap.Get(u.Name)
			if err != nil || !ok || !bytes.Equal(got, u.Data) {
				t.Errorf("mid-publish reader saw mixed generation at version %d", snap.Version())
				return
			}
		}
	}}
	pub2 := New(obs)
	if err := pub2.Publish(newGen); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

// observeFS invokes afterEach after every mutating operation.
type observeFS struct {
	inner     FS
	afterEach func()
}

func (o *observeFS) MkdirAll(dir string) error { return o.inner.MkdirAll(dir) }
func (o *observeFS) Create(p string) (File, error) {
	f, err := o.inner.Create(p)
	o.afterEach()
	if err != nil {
		return nil, err
	}
	return &observeFile{inner: f, afterEach: o.afterEach}, nil
}

type observeFile struct {
	inner     File
	afterEach func()
}

func (f *observeFile) Write(b []byte) (int, error) {
	n, err := f.inner.Write(b)
	f.afterEach()
	return n, err
}

func (f *observeFile) Sync() error {
	err := f.inner.Sync()
	f.afterEach()
	return err
}

func (f *observeFile) Close() error                       { return f.inner.Close() }
func (o *observeFS) Open(p string) (io.ReadCloser, error) { return o.inner.Open(p) }
func (o *observeFS) Rename(a, b string) error {
	err := o.inner.Rename(a, b)
	o.afterEach()
	return err
}
func (o *observeFS) Remove(p string) error {
	err := o.inner.Remove(p)
	o.afterEach()
	return err
}
func (o *observeFS) SyncDir(dir string) error {
	err := o.inner.SyncDir(dir)
	o.afterEach()
	return err
}
func (o *observeFS) Exists(p string) bool                 { return o.inner.Exists(p) }
func (o *observeFS) ReadDir(dir string) ([]string, error) { return o.inner.ReadDir(dir) }
