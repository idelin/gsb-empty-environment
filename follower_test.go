package snapcatchup

import (
	"reflect"
	"testing"
)

// sendChunk fetches chunk seq of generation gen from the leader and delivers
// it to the follower, returning whether the snapshot was installed.
func sendChunk(t *testing.T, l *Leader, f *Follower, gen uint64, seq int) bool {
	t.Helper()
	c, err := l.Chunk(gen, seq)
	if err != nil {
		t.Fatalf("leader chunk gen=%d seq=%d: %v", gen, seq, err)
	}
	done, err := f.ReceiveChunk(c)
	if err != nil {
		t.Fatalf("receive chunk gen=%d seq=%d: %v", gen, seq, err)
	}
	return done
}

func transferAll(t *testing.T, l *Leader, f *Follower, meta SnapshotMeta) {
	t.Helper()
	for seq := 0; seq < meta.NumChunks; seq++ {
		done := sendChunk(t, l, f, meta.Generation, seq)
		wantDone := seq == meta.NumChunks-1
		if done != wantDone {
			t.Fatalf("chunk %d: done=%v, want %v", seq, done, wantDone)
		}
	}
}

// The log keeps appending while chunks are in flight; after install the
// follower replays only entries past the snapshot index.
func TestCatchUpWhileLogAppends(t *testing.T) {
	l := NewLeader(16)
	var history []Entry
	put := func(cmd string) { history = append(history, l.Append(cmd)) }

	for i := 0; i < 5; i++ {
		put("set k" + string(rune('a'+i)) + " v")
	}
	meta := l.TakeSnapshot() // gen 1 covers indexes 1..5
	if meta.LastIndex != 5 {
		t.Fatalf("snapshot last index = %d, want 5", meta.LastIndex)
	}
	if meta.NumChunks < 2 {
		t.Fatalf("test needs multiple chunks, got %d", meta.NumChunks)
	}

	f := NewFollower(NewMemStore())

	// Transfer interleaved with new appends on the leader.
	for seq := 0; seq < meta.NumChunks; seq++ {
		sendChunk(t, l, f, meta.Generation, seq)
		put("set during" + string(rune('0'+seq)) + " x")
	}
	if f.InstalledGeneration() != 1 {
		t.Fatalf("installed generation = %d, want 1", f.InstalledGeneration())
	}
	if f.LastIndex() != 5 {
		t.Fatalf("last index after install = %d, want 5", f.LastIndex())
	}

	// Replay the full history: entries at or below index 5 must be skipped.
	f.Replay(history)
	leaderSM := l.State()
	if f.LastIndex() != leaderSM.Applied {
		t.Fatalf("last index = %d, want %d", f.LastIndex(), leaderSM.Applied)
	}
	if f.StateMachine().Applied != leaderSM.Applied {
		t.Fatalf("applied = %d, want %d (entries <= snapshot index replayed?)",
			f.StateMachine().Applied, leaderSM.Applied)
	}
	if !reflect.DeepEqual(f.StateMachine().Data, leaderSM.Data) {
		t.Fatalf("state mismatch: follower %v, leader %v", f.StateMachine().Data, leaderSM.Data)
	}
}

// A newer snapshot generation supersedes a partial transfer; leftover chunks
// of the old generation are ignored.
func TestGenerationChangeMidTransfer(t *testing.T) {
	l := NewLeader(16)
	for i := 0; i < 3; i++ {
		l.Append("set a" + string(rune('0'+i)) + " v")
	}
	gen1 := l.TakeSnapshot()
	if gen1.NumChunks < 2 {
		t.Fatalf("test needs multiple chunks, got %d", gen1.NumChunks)
	}

	store := NewMemStore()
	f := NewFollower(store)
	sendChunk(t, l, f, gen1.Generation, 0) // partial gen-1 transfer
	// A gen-1 chunk still in flight when the leader moves to generation 2.
	late, err := l.Chunk(gen1.Generation, 1)
	if err != nil {
		t.Fatal(err)
	}

	// Leader snapshots again: generation 2 begins.
	l.Append("set b0 v")
	l.Append("set b1 v")
	gen2 := l.TakeSnapshot()
	if gen2.Generation != 2 || gen2.LastIndex != 5 {
		t.Fatalf("gen2 = %+v, want generation 2 at index 5", gen2)
	}

	// First gen-2 chunk discards the gen-1 staging area.
	sendChunk(t, l, f, gen2.Generation, 0)

	// The late gen-1 chunk is stale and must be ignored.
	done, err := f.ReceiveChunk(late)
	if err != nil || done {
		t.Fatalf("stale chunk: done=%v err=%v, want ignored", done, err)
	}

	// Complete generation 2.
	for seq := 1; seq < gen2.NumChunks; seq++ {
		sendChunk(t, l, f, gen2.Generation, seq)
	}
	if f.InstalledGeneration() != 2 {
		t.Fatalf("installed generation = %d, want 2", f.InstalledGeneration())
	}
	if f.LastIndex() != 5 {
		t.Fatalf("last index = %d, want 5", f.LastIndex())
	}
	if !reflect.DeepEqual(f.StateMachine().Data, l.State().Data) {
		t.Fatalf("state mismatch: follower %v, leader %v", f.StateMachine().Data, l.State().Data)
	}
	if keys, _ := store.List(stagingPrefix); len(keys) != 0 {
		t.Fatalf("staging not cleaned after install: %v", keys)
	}
}

// A retransmitted last chunk (before or after install) is an idempotent
// no-op and must not install twice or corrupt state.
func TestDuplicateLastChunk(t *testing.T) {
	l := NewLeader(16)
	for i := 0; i < 4; i++ {
		l.Append("set k" + string(rune('0'+i)) + " v")
	}
	meta := l.TakeSnapshot()
	if meta.NumChunks < 2 {
		t.Fatalf("test needs multiple chunks, got %d", meta.NumChunks)
	}

	f := NewFollower(NewMemStore())
	last := meta.NumChunks - 1

	// Duplicate a chunk mid-transfer: idempotent.
	sendChunk(t, l, f, meta.Generation, 0)
	if done := sendChunk(t, l, f, meta.Generation, 0); done {
		t.Fatal("duplicate chunk 0 reported install")
	}
	for seq := 1; seq < last; seq++ {
		sendChunk(t, l, f, meta.Generation, seq)
	}

	// Last chunk installs; its retransmission afterwards is ignored.
	if done := sendChunk(t, l, f, meta.Generation, last); !done {
		t.Fatal("last chunk did not complete the install")
	}
	want := *f.StateMachine()
	if done := sendChunk(t, l, f, meta.Generation, last); done {
		t.Fatal("duplicate last chunk reported a second install")
	}
	if !reflect.DeepEqual(*f.StateMachine(), want) {
		t.Fatalf("state changed after duplicate last chunk: %v vs %v", *f.StateMachine(), want)
	}
	if f.InstalledGeneration() != 1 || f.LastIndex() != 4 {
		t.Fatalf("gen=%d lastIndex=%d, want 1/4", f.InstalledGeneration(), f.LastIndex())
	}
}

// A chunk whose payload does not match its checksum is rejected and never
// staged; the transfer can still complete with a clean retransmission.
func TestCorruptChunkRejected(t *testing.T) {
	l := NewLeader(16)
	l.Append("set k v")
	meta := l.TakeSnapshot()

	store := NewMemStore()
	f := NewFollower(store)

	c, err := l.Chunk(meta.Generation, 0)
	if err != nil {
		t.Fatal(err)
	}
	c.Data[0] ^= 0xff // corrupt the payload, keep the checksum
	if _, err := f.ReceiveChunk(c); err == nil {
		t.Fatal("corrupt chunk accepted")
	}
	if keys, _ := store.List(stagingChunkPrefix); len(keys) != 0 {
		t.Fatalf("corrupt chunk was staged: %v", keys)
	}

	transferAll(t, l, f, meta)
	if f.StateMachine().Data["k"] != "v" {
		t.Fatalf("state = %v, want k=v", f.StateMachine().Data)
	}
}

// Replay skips everything at or below the snapshot boundary.
func TestReplaySkipsSnapshotCoveredEntries(t *testing.T) {
	l := NewLeader(64)
	var history []Entry
	for i := 0; i < 5; i++ {
		history = append(history, l.Append("set k v"+string(rune('0'+i))))
	}
	meta := l.TakeSnapshot()
	for i := 5; i < 8; i++ {
		history = append(history, l.Append("set k v"+string(rune('0'+i))))
	}

	f := NewFollower(NewMemStore())
	transferAll(t, l, f, meta)
	f.Replay(history)

	if got := f.StateMachine().Applied; got != 8 {
		t.Fatalf("applied = %d, want 8", got)
	}
	if f.StateMachine().Data["k"] != "v7" {
		t.Fatalf("k = %q, want v7", f.StateMachine().Data["k"])
	}
	if f.LastIndex() != 8 {
		t.Fatalf("last index = %d, want 8", f.LastIndex())
	}
}
