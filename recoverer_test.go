package netsec

import (
	"errors"
	"math/rand"
	"reflect"
	"strconv"
	"testing"
)

func upd(index uint16, block BlockID, data string) Entry {
	return Entry{Kind: KindUpdate, Index: index, Block: block, Data: []byte(data)}
}

func und(index uint16, block BlockID) Entry {
	return Entry{Kind: KindUndo, Index: index, Block: block}
}

type txnSpec struct {
	id     uint64
	seq    SeqNum
	commit bool
	badSum bool
	ops    []Entry
}

func specEntries(s txnSpec) []Entry {
	ops := make([]Entry, len(s.ops))
	for i, op := range s.ops {
		op.TxnID = s.id
		ops[i] = op
	}
	sum := ChecksumEntries(s.id, s.seq, ops)
	if s.badSum {
		sum ^= 0x5a5a
	}
	entries := []Entry{{Kind: KindDescriptor, TxnID: s.id, Seq: s.seq, Sum: sum}}
	entries = append(entries, ops...)
	if s.commit {
		entries = append(entries, Entry{Kind: KindCommit, TxnID: s.id, Seq: s.seq})
	}
	return entries
}

func buildLog(specs ...txnSpec) []Entry {
	var entries []Entry
	for _, s := range specs {
		entries = append(entries, specEntries(s)...)
	}
	return entries
}

func mustSnapshot(t *testing.T, entries []Entry) map[BlockID][]byte {
	t.Helper()
	r := NewRecoverer()
	if err := r.IngestAll(entries); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	return r.Snapshot()
}

func mustReference(t *testing.T, entries []Entry) map[BlockID][]byte {
	t.Helper()
	snap, err := ReferenceSnapshot(entries)
	if err != nil {
		t.Fatalf("ReferenceSnapshot: %v", err)
	}
	return snap
}

func TestUndoInvalidatesEarlierUpdatesOnly(t *testing.T) {
	entries := buildLog(
		txnSpec{id: 1, seq: 10, commit: true, ops: []Entry{upd(0, 100, "v1"), upd(1, 200, "keep")}},
		txnSpec{id: 2, seq: 11, commit: true, ops: []Entry{und(0, 100)}},
		txnSpec{id: 3, seq: 12, commit: true, ops: []Entry{upd(0, 100, "v3")}},
	)
	want := map[BlockID][]byte{100: []byte("v3"), 200: []byte("keep")}
	if got := mustSnapshot(t, entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %v, want %v", got, want)
	}
	if got := mustReference(t, entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("reference = %v, want %v", got, want)
	}

	// Without the later update, the undo leaves the block absent.
	entries = buildLog(
		txnSpec{id: 1, seq: 10, commit: true, ops: []Entry{upd(0, 100, "v1")}},
		txnSpec{id: 2, seq: 11, commit: true, ops: []Entry{und(0, 100)}},
	)
	if got := mustSnapshot(t, entries); len(got) != 0 {
		t.Fatalf("snapshot = %v, want empty", got)
	}
}

func TestUndoWithinOneTransaction(t *testing.T) {
	entries := buildLog(
		txnSpec{id: 1, seq: 1, commit: true, ops: []Entry{
			upd(0, 100, "early"),
			und(1, 100),
			upd(2, 100, "late"),
		}},
	)
	want := map[BlockID][]byte{100: []byte("late")}
	if got := mustSnapshot(t, entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %v, want %v", got, want)
	}
}

func TestUncommittedUndoHasNoEffect(t *testing.T) {
	entries := buildLog(
		txnSpec{id: 1, seq: 1, commit: true, ops: []Entry{upd(0, 100, "live")}},
		txnSpec{id: 2, seq: 2, commit: false, ops: []Entry{und(0, 100)}},
	)
	want := map[BlockID][]byte{100: []byte("live")}
	if got := mustSnapshot(t, entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %v, want %v", got, want)
	}
	if got := mustReference(t, entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("reference = %v, want %v", got, want)
	}
}

func TestInvalidChecksumNotReplayed(t *testing.T) {
	entries := buildLog(
		txnSpec{id: 1, seq: 1, commit: true, badSum: true, ops: []Entry{upd(0, 100, "bad")}},
		txnSpec{id: 2, seq: 2, commit: true, ops: []Entry{upd(0, 200, "good")}},
	)
	r := NewRecoverer()
	if err := r.IngestAll(entries); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	want := map[BlockID][]byte{200: []byte("good")}
	if got := r.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %v, want %v", got, want)
	}
	if r.Invalid() != 1 || r.Committed() != 1 {
		t.Fatalf("Invalid=%d Committed=%d, want 1/1", r.Invalid(), r.Committed())
	}
}

func TestDuplicateEntriesAreIdempotent(t *testing.T) {
	base := buildLog(
		txnSpec{id: 1, seq: 1, commit: true, ops: []Entry{upd(0, 100, "v1"), upd(1, 200, "x")}},
		txnSpec{id: 2, seq: 2, commit: true, ops: []Entry{und(0, 100)}},
	)
	// Deliver every entry three times, interleaved.
	var dup []Entry
	for round := 0; round < 3; round++ {
		dup = append(dup, base...)
	}
	got := mustSnapshot(t, dup)
	want := mustSnapshot(t, base)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("duplicated log snapshot = %v, want %v", got, want)
	}
	if ref := mustReference(t, dup); !reflect.DeepEqual(ref, want) {
		t.Fatalf("duplicated log reference = %v, want %v", ref, want)
	}
}

func TestConflictingDuplicatePoisonsTransaction(t *testing.T) {
	good := buildLog(txnSpec{id: 1, seq: 1, commit: true, ops: []Entry{upd(0, 100, "a")}})
	evil := specEntries(txnSpec{id: 1, seq: 1, commit: true, ops: []Entry{upd(0, 100, "b")}})
	entries := append(good[:len(good)-1], evil...) // same txn, conflicting op before commit
	r := NewRecoverer()
	if err := r.IngestAll(entries); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	if got := r.Snapshot(); len(got) != 0 {
		t.Fatalf("snapshot = %v, want empty (poisoned transaction)", got)
	}
	if r.Invalid() != 1 {
		t.Fatalf("Invalid = %d, want 1", r.Invalid())
	}
}

func TestWraparoundOrdering(t *testing.T) {
	entries := buildLog(
		txnSpec{id: 1, seq: 0xFFFFFFFE, commit: true, ops: []Entry{upd(0, 100, "oldest")}},
		txnSpec{id: 2, seq: 0xFFFFFFFF, commit: true, ops: []Entry{und(0, 100)}},
		txnSpec{id: 3, seq: 0, commit: true, ops: []Entry{upd(0, 100, "reused")}},
		txnSpec{id: 4, seq: 1, commit: true, ops: []Entry{upd(0, 100, "newest")}},
	)
	want := map[BlockID][]byte{100: []byte("newest")}
	if got := mustSnapshot(t, entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %v, want %v", got, want)
	}
	if got := mustReference(t, entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("reference = %v, want %v", got, want)
	}

	// The undo just before the wrap must erase the pre-wrap update, and
	// the old update must not overwrite the block reused after the wrap.
	entries = buildLog(
		txnSpec{id: 1, seq: 0xFFFFFFFE, commit: true, ops: []Entry{upd(0, 100, "oldest")}},
		txnSpec{id: 2, seq: 0xFFFFFFFF, commit: true, ops: []Entry{und(0, 100)}},
		txnSpec{id: 3, seq: 0, commit: true, ops: []Entry{upd(0, 100, "reused")}},
	)
	want = map[BlockID][]byte{100: []byte("reused")}
	if got := mustSnapshot(t, entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %v, want %v", got, want)
	}
}

func TestOutOfOrderDelivery(t *testing.T) {
	specs := []txnSpec{
		{id: 1, seq: 0xFFFFFFFF, commit: true, ops: []Entry{upd(0, 100, "a")}},
		{id: 2, seq: 0, commit: true, ops: []Entry{und(0, 100)}},
		{id: 3, seq: 1, commit: true, ops: []Entry{upd(0, 100, "c")}},
		{id: 4, seq: 2, commit: true, ops: []Entry{upd(0, 200, "d")}},
	}
	// Deliver transactions in reverse sequence order.
	var entries []Entry
	for i := len(specs) - 1; i >= 0; i-- {
		entries = append(entries, specEntries(specs[i])...)
	}
	want := map[BlockID][]byte{100: []byte("c"), 200: []byte("d")}
	if got := mustSnapshot(t, entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %v, want %v", got, want)
	}
}

func TestWindowExceeded(t *testing.T) {
	r := NewRecoverer()
	ok := buildLog(txnSpec{id: 1, seq: 0, commit: true, ops: []Entry{upd(0, 100, "a")}})
	if err := r.IngestAll(ok); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	// Exactly HalfCircle - 1 away is still a valid window.
	edge := buildLog(txnSpec{id: 2, seq: SeqNum(HalfCircle - 1), commit: true, ops: []Entry{upd(0, 101, "b")}})
	if err := r.IngestAll(edge); err != nil {
		t.Fatalf("IngestAll at HalfCircle-1: %v", err)
	}
	// Reaching HalfCircle must be rejected.
	over := buildLog(txnSpec{id: 3, seq: SeqNum(HalfCircle), commit: true, ops: []Entry{upd(0, 102, "c")}})
	if err := r.IngestAll(over); !errors.Is(err, ErrWindowExceeded) {
		t.Fatalf("IngestAll at HalfCircle = %v, want ErrWindowExceeded", err)
	}
	if r.Committed() != 2 {
		t.Fatalf("Committed = %d, want 2", r.Committed())
	}
	want := map[BlockID][]byte{100: []byte("a"), 101: []byte("b")}
	if got := r.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %v, want %v", got, want)
	}
}

func TestRandomizedAgainstReference(t *testing.T) {
	for seed := int64(0); seed < 300; seed++ {
		rng := rand.New(rand.NewSource(seed))
		// Cluster sequence numbers around the wrap point so logs cross
		// zero while staying well inside a half-circle window.
		base := uint32(0xFFFFFFF8)
		numTxns := 1 + rng.Intn(8)
		var entries []Entry
		for i := 0; i < numTxns; i++ {
			seq := SeqNum(base + uint32(rng.Intn(32)))
			numOps := rng.Intn(5)
			ops := make([]Entry, numOps)
			for j := range ops {
				block := BlockID(rng.Intn(6))
				if rng.Intn(3) == 0 {
					ops[j] = und(uint16(j), block)
				} else {
					ops[j] = upd(uint16(j), block, strconv.Itoa(rng.Intn(1000)))
				}
			}
			spec := txnSpec{
				id:     uint64(i + 1),
				seq:    seq,
				commit: rng.Intn(10) < 7,
				badSum: rng.Intn(10) == 0,
				ops:    ops,
			}
			entries = append(entries, specEntries(spec)...)
		}
		// Duplicate a random subset of entries.
		var withDup []Entry
		for _, e := range entries {
			withDup = append(withDup, e)
			if rng.Intn(4) == 0 {
				withDup = append(withDup, e)
			}
		}
		// Shuffle delivery order freely: assembly, dedup, validation and
		// replay ordering must be order-independent.
		rng.Shuffle(len(withDup), func(i, j int) {
			withDup[i], withDup[j] = withDup[j], withDup[i]
		})

		r := NewRecoverer()
		rerr := r.IngestAll(withDup)
		ref, referr := ReferenceSnapshot(withDup)
		if (rerr == nil) != (referr == nil) {
			t.Fatalf("seed %d: recoverer err %v, reference err %v", seed, rerr, referr)
		}
		if rerr != nil {
			continue
		}
		if got := r.Snapshot(); !reflect.DeepEqual(got, ref) {
			t.Fatalf("seed %d: snapshot = %v, reference = %v", seed, got, ref)
		}
	}
}
