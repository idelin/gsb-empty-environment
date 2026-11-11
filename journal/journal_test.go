package journal

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

const testJournalID uint64 = 4242

func id(ref, seq uint64) FileID { return FileID{RefNum: ref, Seq: seq} }

func lk(parent uint64, name string) LinkKey { return LinkKey{ParentRef: parent, Name: name} }

func rec(usn uint64, reason Reason, fid FileID, parent uint64, name string) Record {
	r := Record{USN: usn, JournalID: testJournalID, Reason: reason, ID: fid, TimeMicros: int64(usn)}
	switch reason {
	case ReasonRenameNew, ReasonLinkAdd:
		r.NewParent, r.NewName = parent, name
	default:
		r.ParentRef, r.Name = parent, name
	}
	return r
}

func renamePair(usnOld, usnNew uint64, fid FileID, from, to LinkKey) (Record, Record) {
	old := Record{USN: usnOld, JournalID: testJournalID, Reason: ReasonRenameOld, ID: fid,
		ParentRef: from.ParentRef, Name: from.Name, TimeMicros: int64(usnOld)}
	new := Record{USN: usnNew, JournalID: testJournalID, Reason: ReasonRenameNew, ID: fid,
		NewParent: to.ParentRef, NewName: to.Name, TimeMicros: int64(usnNew)}
	return old, new
}

func openTestProcessor(t *testing.T) *Processor {
	t.Helper()
	p, err := OpenProcessor(filepath.Join(t.TempDir(), "checkpoint.json"), testJournalID, 100)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return p
}

func reopen(t *testing.T, p *Processor) *Processor {
	t.Helper()
	p2, err := OpenProcessor(p.CheckpointPath(), testJournalID, 100)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	return p2
}

// TestOutOfOrderRenamePairing delivers rename-new before rename-old, with the
// USN gap filled by an unrelated record later. The rename only materializes
// once both halves are applied, and the unpaired half persists on checkpoint.
func TestOutOfOrderRenamePairing(t *testing.T) {
	p := openTestProcessor(t)
	fid := id(7, 1)
	from, to := lk(1, "a.txt"), lk(1, "b.txt")

	if n, err := p.ApplyBatch([]Record{rec(100, ReasonCreate, fid, 1, "a.txt")}); err != nil || n != 1 {
		t.Fatalf("create: n=%d err=%v", n, err)
	}

	oldHalf, newHalf := renamePair(101, 102, fid, from, to)
	other := id(8, 1)
	// Both halves arrive before the contiguous prefix reaches them: USN 101 is
	// the resume point, so 102/103 are buffered in USN order and nothing is
	// applied yet.
	if _, err := p.ApplyBatch([]Record{newHalf, rec(103, ReasonCreate, other, 1, "c.txt")}); err != nil {
		t.Fatalf("batch1: %v", err)
	}
	if got := p.NextUSN(); got != 101 {
		t.Fatalf("next usn after buffered new-half = %d, want 101", got)
	}
	if p.BufferedCount() != 2 || p.PendingRenames() != 0 {
		t.Fatalf("buffered=%d pending=%d, want 2/0 (records ahead of next USN)",
			p.BufferedCount(), p.PendingRenames())
	}
	if _, ok := p.Snapshot().LookupEntry(to); ok {
		t.Fatal("new name visible while rename is unpaired")
	}

	// Restart while the gap is still open: nothing applied yet, buffered
	// records are re-delivered by the log reader after resume.
	p = reopen(t, p)
	if p.NextUSN() != 101 || p.PendingRenames() != 0 {
		t.Fatalf("after restart next=%d pending=%d, want 101/0", p.NextUSN(), p.PendingRenames())
	}

	// Now close the gap. The old half (101) pairs with the buffered new half.
	if _, err := p.ApplyBatch([]Record{oldHalf, newHalf, rec(103, ReasonCreate, other, 1, "c.txt")}); err != nil {
		t.Fatalf("close gap: %v", err)
	}
	snap := p.Snapshot()
	if _, ok := snap.LookupEntry(from); ok {
		t.Fatal("old name still present after rename completed")
	}
	if owner, ok := snap.LookupEntry(to); !ok || owner != fid {
		t.Fatalf("new name owner = %v ok=%v, want %v", owner, ok, fid)
	}
	links, _ := snap.Links(fid)
	if len(links) != 1 || links[0] != to {
		t.Fatalf("hard-link set after rename = %v, want only %v", links, to)
	}
	if st := p.Stats(); st.Renames != 1 || st.Creates != 2 {
		t.Fatalf("stats = %+v", st)
	}

	// A second scenario: old half applied, crash, then new half after restart.
	// This exercises checkpointing of an unpaired rename half inside the index.
	p2 := openTestProcessor(t)
	f2 := id(77, 1)
	from2, to2 := lk(1, "m.txt"), lk(1, "n.txt")
	p2.ApplyBatch([]Record{rec(100, ReasonCreate, f2, 1, "m.txt")})
	o2, n2 := renamePair(101, 102, f2, from2, to2)
	p2.ApplyBatch([]Record{o2})
	if p2.PendingRenames() != 1 {
		t.Fatalf("pending old half = %d, want 1", p2.PendingRenames())
	}
	p2 = reopen(t, p2)
	if p2.PendingRenames() != 1 {
		t.Fatalf("pending half lost across checkpoint: %d", p2.PendingRenames())
	}
	if _, err := p2.ApplyBatch([]Record{n2}); err != nil {
		t.Fatalf("pair after restart: %v", err)
	}
	if owner, ok := p2.Snapshot().LookupEntry(to2); !ok || owner != f2 {
		t.Fatal("checkpointed rename half did not pair after restart")
	}
}

// TestSnapshotNeverSeesHalfRename runs rename application concurrently with
// snapshot readers and asserts a reader can never observe an impossible
// combination: both old and new entry, neither entry for a live object, or the
// object's link set disagreeing with the entry map.
func TestSnapshotNeverSeesHalfRename(t *testing.T) {
	p := openTestProcessor(t)
	const objs = 64
	setup := make([]Record, 0, objs)
	for i := uint64(0); i < objs; i++ {
		setup = append(setup, rec(100+i, ReasonCreate, id(1000+i, 1), 1, oldName(i)))
	}
	if _, err := p.ApplyBatch(setup); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var readerFailed string
	var failMu sync.Mutex

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			snap := p.Snapshot()
			for i := uint64(0); i < objs; i++ {
				fid := id(1000+i, 1)
				oldKey, newKey := lk(1, oldName(i)), lk(1, newName(i))
				obj, alive := snap.LookupObject(fid)
				if !alive {
					continue
				}
				owned := 0
				if owner, ok := snap.LookupEntry(oldKey); ok && owner == fid {
					owned++
				}
				if owner, ok := snap.LookupEntry(newKey); ok && owner == fid {
					owned++
				}
				if len(obj.Links) != 1 || owned != 1 {
					failMu.Lock()
					readerFailed = "half-applied rename visible in snapshot"
					failMu.Unlock()
					return
				}
			}
		}
	}()

	batch := make([]Record, 0, objs*2)
	usn := uint64(100 + objs)
	for i := uint64(0); i < objs; i++ {
		fid := id(1000+i, 1)
		oldHalf, newHalf := renamePair(usn, usn+1, fid, lk(1, oldName(i)), lk(1, newName(i)))
		batch = append(batch, newHalf, oldHalf) // deliberately reversed delivery
		usn += 2
	}
	if _, err := p.ApplyBatch(batch); err != nil {
		close(stop)
		t.Fatal(err)
	}
	close(stop)
	wg.Wait()

	if readerFailed != "" {
		t.Fatal(readerFailed)
	}
	if st := p.Stats(); st.Renames != objs {
		t.Fatalf("renames = %d, want %d (stats %+v)", st.Renames, objs, st)
	}
}

func oldName(i uint64) string { return "old-" + itoa(i) }
func newName(i uint64) string { return "new-" + itoa(i) }

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// TestTruncatedLogResume simulates a crash between the two rename halves, a
// restart from the atomic checkpoint, and separately a log truncated past the
// resume point, which must be reported as ErrGap rather than silently skipped.
func TestTruncatedLogResume(t *testing.T) {
	p := openTestProcessor(t)
	fid := id(9, 1)
	from, to := lk(2, "x"), lk(3, "y")
	if _, err := p.ApplyBatch([]Record{rec(100, ReasonCreate, fid, 2, "x")}); err != nil {
		t.Fatal(err)
	}
	oldHalf, newHalf := renamePair(101, 102, fid, from, to)
	if _, err := p.ApplyBatch([]Record{oldHalf}); err != nil {
		t.Fatal(err)
	}
	if p.PendingRenames() != 1 {
		t.Fatalf("pending before crash = %d", p.PendingRenames())
	}
	path := p.CheckpointPath()

	// Process restart from disk: the unpaired half and next USN are recovered.
	p2, err := OpenProcessor(path, testJournalID, 101)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if p2.PendingRenames() != 1 || p2.NextUSN() != 102 {
		t.Fatalf("resumed state pending=%d next=%d", p2.PendingRenames(), p2.NextUSN())
	}
	if _, err := p2.ApplyBatch([]Record{newHalf}); err != nil {
		t.Fatalf("complete rename after restart: %v", err)
	}
	if owner, ok := p2.Snapshot().LookupEntry(to); !ok || owner != fid {
		t.Fatal("rename not completed across restart")
	}

	// Truncation: retained range starts beyond the resume point.
	if err := p2.ValidateJournal(testJournalID, p2.NextUSN()+10); !errors.Is(err, ErrGap) {
		t.Fatalf("truncation err = %v, want ErrGap", err)
	}
	// Log incarnation change must be rejected too.
	if err := p2.ValidateJournal(testJournalID+1, 0); !errors.Is(err, ErrJournalID) {
		t.Fatalf("journal id err = %v, want ErrJournalID", err)
	}

	// After a full rescan the caller resets onto the new log lineage.
	if err := p2.Reset(testJournalID+1, 500); err != nil {
		t.Fatal(err)
	}
	if p2.NextUSN() != 500 || p2.Snapshot().ObjectCount() != 0 {
		t.Fatal("reset did not clear lineage state")
	}
}
