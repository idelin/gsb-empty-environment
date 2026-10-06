package replica

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func openAt(t *testing.T, dir string, hook Hook) *Replica {
	t.Helper()
	st, err := OpenStore(dir, hook)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	r, err := OpenReplica(st)
	if err != nil {
		t.Fatalf("open replica: %v", err)
	}
	return r
}

func mustAppend(t *testing.T, r *Replica, req AppendRequest) AppendReply {
	t.Helper()
	rep, err := r.HandleAppend(req)
	if err != nil {
		t.Fatalf("append %+v: %v", req, err)
	}
	return rep
}

func wantLog(t *testing.T, r *Replica, commit uint64, cmds ...string) {
	t.Helper()
	_, gotCommit, last, entries := r.State()
	if gotCommit != commit {
		t.Fatalf("commit: want %d got %d", commit, gotCommit)
	}
	if last != uint64(len(cmds)) || len(entries) != len(cmds) {
		t.Fatalf("log length: want %d got %d", len(cmds), last)
	}
	for i, c := range cmds {
		if entries[i].Command != c {
			t.Fatalf("entry %d: want %q got %q", i+1, c, entries[i].Command)
		}
	}
}

func TestAppendAndCommitAdvance(t *testing.T) {
	r := openAt(t, t.TempDir(), nil)
	rep := mustAppend(t, r, AppendRequest{Term: 1, PrevIndex: 0, PrevTerm: 0,
		Entries: []Entry{{1, "a"}, {1, "b"}}, LeaderCommit: 0})
	if !rep.Success || rep.LastIndex != 2 || rep.CommitIndex != 0 {
		t.Fatalf("unexpected reply %+v", rep)
	}
	rep = mustAppend(t, r, AppendRequest{Term: 1, PrevIndex: 2, PrevTerm: 1,
		Entries: []Entry{{1, "c"}}, LeaderCommit: 3})
	if !rep.Success || rep.CommitIndex != 3 {
		t.Fatalf("unexpected reply %+v", rep)
	}
	wantLog(t, r, 3, "a", "b", "c")
}

func TestStaleTermRejected(t *testing.T) {
	r := openAt(t, t.TempDir(), nil)
	mustAppend(t, r, AppendRequest{Term: 5, Entries: []Entry{{5, "a"}}})
	rep := mustAppend(t, r, AppendRequest{Term: 4, Entries: []Entry{{4, "z"}}})
	if rep.Success || rep.Reason != ReasonStaleTerm {
		t.Fatalf("stale term not rejected: %+v", rep)
	}
	wantLog(t, r, 0, "a")
}

func TestMissingPredecessorThenGapFilled(t *testing.T) {
	r := openAt(t, t.TempDir(), nil)
	mustAppend(t, r, AppendRequest{Term: 1, Entries: []Entry{{1, "a"}}})
	// Leader jumps ahead: prev index 3 does not exist locally.
	rep := mustAppend(t, r, AppendRequest{Term: 1, PrevIndex: 3, PrevTerm: 1,
		Entries: []Entry{{1, "d"}}})
	if rep.Success || rep.Reason != ReasonMissingPredecessor {
		t.Fatalf("missing predecessor not rejected: %+v", rep)
	}
	// Leader backs up and fills the gap.
	rep = mustAppend(t, r, AppendRequest{Term: 1, PrevIndex: 1, PrevTerm: 1,
		Entries: []Entry{{1, "b"}, {1, "c"}, {1, "d"}}})
	if !rep.Success {
		t.Fatalf("gap fill rejected: %+v", rep)
	}
	wantLog(t, r, 0, "a", "b", "c", "d")
}

func TestTruncateConflictingSuffix(t *testing.T) {
	r := openAt(t, t.TempDir(), nil)
	mustAppend(t, r, AppendRequest{Term: 1, Entries: []Entry{{1, "a"}, {1, "b"}, {1, "c"}},
		LeaderCommit: 1})
	// New term replaces uncommitted suffix from index 2.
	rep := mustAppend(t, r, AppendRequest{Term: 2, PrevIndex: 1, PrevTerm: 1,
		Entries: []Entry{{2, "B"}, {2, "C"}, {2, "D"}}, LeaderCommit: 3})
	if !rep.Success {
		t.Fatalf("truncate append rejected: %+v", rep)
	}
	wantLog(t, r, 3, "a", "B", "C", "D")
	_, _, _, entries := r.State()
	for i, want := range []uint64{1, 2, 2, 2} {
		if entries[i].Term != want {
			t.Fatalf("entry %d term: want %d got %d", i+1, want, entries[i].Term)
		}
	}
}

func TestCommittedPrefixCannotBeTruncated(t *testing.T) {
	r := openAt(t, t.TempDir(), nil)
	mustAppend(t, r, AppendRequest{Term: 1, Entries: []Entry{{1, "a"}, {1, "b"}},
		LeaderCommit: 2})
	// New term tries to overwrite committed index 1.
	_, err := r.HandleAppend(AppendRequest{Term: 2, PrevIndex: 0, PrevTerm: 0,
		Entries: []Entry{{2, "X"}}})
	if !errors.Is(err, ErrCorruption) {
		t.Fatalf("want corruption, got %v", err)
	}
	// Committed predecessor contradicted by a different prevTerm.
	_, err = r.HandleAppend(AppendRequest{Term: 2, PrevIndex: 2, PrevTerm: 9,
		Entries: []Entry{{2, "X"}}})
	if !errors.Is(err, ErrCorruption) {
		t.Fatalf("want corruption, got %v", err)
	}
	wantLog(t, r, 2, "a", "b")
}

func TestSameIndexSameTermDifferentContent(t *testing.T) {
	r := openAt(t, t.TempDir(), nil)
	mustAppend(t, r, AppendRequest{Term: 1, Entries: []Entry{{1, "a"}}})
	_, err := r.HandleAppend(AppendRequest{Term: 1, PrevIndex: 0, PrevTerm: 0,
		Entries: []Entry{{1, "b"}}})
	if !errors.Is(err, ErrCorruption) {
		t.Fatalf("want corruption, got %v", err)
	}
	wantLog(t, r, 0, "a")
}

func TestDuplicateDeliveryIdempotent(t *testing.T) {
	r := openAt(t, t.TempDir(), nil)
	req := AppendRequest{Term: 1, Entries: []Entry{{1, "a"}, {1, "b"}}, LeaderCommit: 2}
	rep1 := mustAppend(t, r, req)
	rep2 := mustAppend(t, r, req) // full duplicate
	rep3 := mustAppend(t, r, AppendRequest{Term: 1, PrevIndex: 1, PrevTerm: 1,
		Entries: []Entry{{1, "b"}}, LeaderCommit: 2}) // partial overlap
	for i, rep := range []AppendReply{rep1, rep2, rep3} {
		if !rep.Success {
			t.Fatalf("delivery %d rejected: %+v", i, rep)
		}
	}
	wantLog(t, r, 2, "a", "b")
}

func TestNoAckBeforePersistence(t *testing.T) {
	dir := t.TempDir()
	failSave := errors.New("disk on fire")
	hook := func(point string) error {
		if point == PointSaveBegin {
			return failSave
		}
		return nil
	}
	r := openAt(t, dir, hook)
	rep, err := r.HandleAppend(AppendRequest{Term: 1, Entries: []Entry{{1, "a"}},
		LeaderCommit: 1})
	if err == nil || rep.Success || rep.Reason != ReasonPersistFailed {
		t.Fatalf("append must not be acknowledged: rep=%+v err=%v", rep, err)
	}
	// In-memory state untouched.
	wantLog(t, r, 0)
	// Nothing durable either.
	r2 := openAt(t, dir, nil)
	wantLog(t, r2, 0)
}

// crashInjector panics (simulating power loss) the first time the armed
// cut point is reached.
type crashInjector struct{ point string }

type crashPanic struct{ point string }

func (c *crashInjector) hook(point string) error {
	if point == c.point {
		c.point = "" // fire once
		panic(crashPanic{point: point})
	}
	return nil
}

func TestCrashCutPoints(t *testing.T) {
	points := []string{PointSaveBegin, PointLogPersisted, PointMetaPersisted, PointSaveDone}
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			dir := t.TempDir()
			r := openAt(t, dir, nil)
			mustAppend(t, r, AppendRequest{Term: 1, Entries: []Entry{{1, "a"}, {1, "b"}},
				LeaderCommit: 2})

			cj := &crashInjector{point: point}
			r = openAt(t, dir, cj.hook)
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						if _, ok := rec.(crashPanic); !ok {
							panic(rec)
						}
					}
				}()
				// Never acknowledged: the crash interrupts before reply.
				r.HandleAppend(AppendRequest{Term: 1, PrevIndex: 2, PrevTerm: 1,
					Entries: []Entry{{1, "c"}}, LeaderCommit: 3})
			}()

			// Restart from durable state.
			r = openAt(t, dir, nil)
			_, commit, last, entries := r.State()
			// Previously committed prefix survives every cut point.
			if commit < 2 || last < 2 || entries[0].Command != "a" || entries[1].Command != "b" {
				t.Fatalf("committed prefix lost after crash at %s: commit=%d log=%v",
					point, commit, entries)
			}
			// Commit never points beyond the log.
			if commit > last {
				t.Fatalf("commit %d beyond last index %d", commit, last)
			}
			// Duplicate redelivery after crash must succeed and converge.
			rep := mustAppend(t, r, AppendRequest{Term: 1, PrevIndex: 2, PrevTerm: 1,
				Entries: []Entry{{1, "c"}}, LeaderCommit: 3})
			if !rep.Success {
				t.Fatalf("redelivery rejected: %+v", rep)
			}
			wantLog(t, r, 3, "a", "b", "c")
		})
	}
}

func TestCommitAdvancementAcrossCrash(t *testing.T) {
	dir := t.TempDir()
	r := openAt(t, dir, nil)
	mustAppend(t, r, AppendRequest{Term: 1, Entries: []Entry{{1, "a"}}, LeaderCommit: 1})

	// Crash after the log is persisted but before the commit meta is.
	cj := &crashInjector{point: PointLogPersisted}
	r = openAt(t, dir, cj.hook)
	func() {
		defer func() { recover() }()
		r.HandleAppend(AppendRequest{Term: 1, PrevIndex: 1, PrevTerm: 1,
			Entries: []Entry{{1, "b"}}, LeaderCommit: 2})
	}()

	r = openAt(t, dir, nil)
	_, commit, _, _ := r.State()
	if commit != 1 {
		t.Fatalf("commit must not advance without durable meta: got %d", commit)
	}
	// Entry "b" is durable; a heartbeat re-advances the commit index.
	rep := mustAppend(t, r, AppendRequest{Term: 1, PrevIndex: 2, PrevTerm: 1,
		LeaderCommit: 2})
	if !rep.Success || rep.CommitIndex != 2 {
		t.Fatalf("commit did not re-advance: %+v", rep)
	}
	wantLog(t, r, 2, "a", "b")
}

// TestEventReplay records a trace of a run that includes duplicate
// deliveries, a missing predecessor, crashes and commit advancement, then
// verifies that replaying the trace on a fresh replica reproduces every
// observed outcome exactly.
func TestEventReplay(t *testing.T) {
	dir := t.TempDir()
	rec := &Recorder{}
	openRec := func() *Replica {
		r := openAt(t, dir, nil)
		r.SetRecorder(rec)
		return r
	}

	r := openRec()
	mustAppend(t, r, AppendRequest{Term: 1, Entries: []Entry{{1, "a"}, {1, "b"}},
		LeaderCommit: 1})
	// Duplicate delivery.
	mustAppend(t, r, AppendRequest{Term: 1, Entries: []Entry{{1, "a"}, {1, "b"}},
		LeaderCommit: 1})
	// Missing predecessor.
	mustAppend(t, r, AppendRequest{Term: 1, PrevIndex: 5, PrevTerm: 1,
		Entries: []Entry{{1, "f"}}})

	// Crash and recover.
	rec.RecordCrash()
	r = openRec()

	// Gap fill + commit advance after restart.
	mustAppend(t, r, AppendRequest{Term: 2, PrevIndex: 2, PrevTerm: 1,
		Entries: []Entry{{2, "c"}}, LeaderCommit: 3})
	// Stale term message from the old leader.
	mustAppend(t, r, AppendRequest{Term: 1, PrevIndex: 2, PrevTerm: 1,
		Entries: []Entry{{1, "z"}}})

	// Serialize the trace (as a durable event log would be) and replay it.
	blob, err := json.Marshal(rec.Steps)
	if err != nil {
		t.Fatalf("marshal trace: %v", err)
	}
	var steps []RecordedStep
	if err := json.Unmarshal(blob, &steps); err != nil {
		t.Fatalf("unmarshal trace: %v", err)
	}
	got, err := Replay(filepath.Join(t.TempDir(), "replay"), steps)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if diffs := DiffSteps(rec.Steps, got); len(diffs) > 0 {
		for _, d := range diffs {
			t.Errorf("replay mismatch: %s", d)
		}
	}
}
