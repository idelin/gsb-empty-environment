package dod

import (
	"math/rand"
	"testing"
)

func buildArchive(t *testing.T, policy Policy, ts, vs []int64) (*Archive, *MemStore) {
	t.Helper()
	st := &MemStore{}
	a := NewArchive(st, policy)
	for i := range ts {
		if err := a.Add(ts[i], vs[i]); err != nil {
			t.Fatalf("Add(%d): %v", i, err)
		}
	}
	if err := a.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	return a, st
}

func TestRegressionPolicyReject(t *testing.T) {
	st := &MemStore{}
	a := NewArchive(st, RejectRegression)
	_ = a.Add(10, 1)
	_ = a.Add(20, 2)
	if err := a.Add(5, 3); err != ErrRegression {
		t.Fatalf("want ErrRegression, got %v", err)
	}
}

// Time resets must produce blocks carrying an explicit, incremented epoch.
func TestResetExplicitEpoch(t *testing.T) {
	st := &MemStore{}
	a := NewArchive(st, AllowRegression)
	for i := 0; i < 10; i++ {
		_ = a.Add(int64(i*10), int64(i))
	}
	_ = a.Add(0, 100) // regression -> new epoch
	for i := 1; i < 10; i++ {
		_ = a.Add(int64(i*10), int64(100+i))
	}
	if err := a.Reset(); err != nil { // explicit reset anchor
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		_ = a.Add(int64(i), int64(200+i))
	}
	if err := a.Flush(); err != nil {
		t.Fatal(err)
	}
	var epochs []uint64
	for _, e := range a.Index().Entries {
		epochs = append(epochs, e.Epoch)
	}
	want := []uint64{0, 1, 2}
	if len(epochs) != len(want) {
		t.Fatalf("epochs %v, want %v", epochs, want)
	}
	for i := range want {
		if epochs[i] != want[i] {
			t.Fatalf("epochs %v, want %v", epochs, want)
		}
	}
	// Blocks decode with their epoch attached.
	b1, err := a.ReadBlock(1)
	if err != nil {
		t.Fatal(err)
	}
	if b1.Epoch != 1 {
		t.Fatalf("block epoch %d, want 1", b1.Epoch)
	}
}

func TestSeekAndReadRange(t *testing.T) {
	var ts, vs []int64
	for i := 0; i < 500; i++ {
		ts = append(ts, int64(i*7))
		vs = append(vs, int64(i*i%97)-48)
	}
	a, _ := buildArchive(t, RejectRegression, ts, vs)
	// Seek must land on a block whose range contains or precedes the target.
	pos := a.SeekTime(1000)
	live := a.Index().Live()
	if pos >= len(live) {
		t.Fatalf("seek past end")
	}
	e := a.Index().Entries[live[pos]]
	if e.T1 < 1000 {
		t.Fatalf("seek block ends at %d < 1000", e.T1)
	}
	if pos > 0 {
		prev := a.Index().Entries[live[pos-1]]
		if prev.T1 >= 1000 {
			t.Fatalf("seek not minimal: prev ends %d", prev.T1)
		}
	}
	gotT, gotV, err := a.ReadRange(1000, 2000, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	var wantT, wantV []int64
	for i, tt := range ts {
		if tt >= 1000 && tt <= 2000 {
			wantT = append(wantT, tt)
			wantV = append(wantV, vs[i])
		}
	}
	if !eqI64(gotT, wantT) || !eqI64(gotV, wantV) {
		t.Fatalf("range mismatch")
	}
	// Bounded output.
	if _, _, err := a.ReadRange(0, 100000, 10); err != ErrTooLarge {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

// Local correction re-encodes only the target block; old index versions
// remain visible as superseded entries.
func TestCorrectionKeepsOldIndex(t *testing.T) {
	var ts, vs []int64
	for i := 0; i < 200; i++ {
		ts = append(ts, int64(i*3))
		vs = append(vs, int64(i))
	}
	a, st := buildArchive(t, RejectRegression, ts, vs)
	sizeBefore := st.Len()
	entriesBefore := len(a.Index().Entries)

	blk, err := a.ReadBlock(0)
	if err != nil {
		t.Fatal(err)
	}
	newVals := make([]int64, len(blk.Values))
	copy(newVals, blk.Values)
	newVals[0] = -777 // the correction
	if err := a.Correct(0, blk.Times, newVals); err != nil {
		t.Fatal(err)
	}
	if st.Len() <= sizeBefore {
		t.Fatalf("correction must append, not rewrite")
	}
	if len(a.Index().Entries) <= entriesBefore {
		t.Fatalf("old index version not retained")
	}
	var superseded, versioned int
	for _, e := range a.Index().Entries {
		if e.Superseded {
			superseded++
		}
		if e.Version == 1 {
			versioned++
		}
	}
	if superseded != 1 || versioned < 1 {
		t.Fatalf("superseded=%d versioned=%d", superseded, versioned)
	}
	fixed, err := a.ReadBlock(0)
	if err != nil {
		t.Fatal(err)
	}
	if fixed.Values[0] != -777 {
		t.Fatalf("correction not visible: %d", fixed.Values[0])
	}
	// Untouched blocks still decode identically.
	gotT, gotV, err := a.ReadRange(blk.Times[len(blk.Times)-1]+1, 1<<62, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	off := len(blk.Times)
	if !eqI64(gotT, ts[off:]) || !eqI64(gotV, vs[off:]) {
		t.Fatalf("later history changed by local correction")
	}
}

// Appends must not rewrite existing bytes.
func TestAppendDoesNotRewrite(t *testing.T) {
	st := &MemStore{}
	a := NewArchive(st, RejectRegression)
	for i := 0; i < 100; i++ {
		_ = a.Add(int64(i), int64(i*2))
	}
	if err := a.Flush(); err != nil {
		t.Fatal(err)
	}
	prefix := append([]byte{}, st.Bytes()...)
	for i := 100; i < 300; i++ {
		_ = a.Add(int64(i), int64(i*2))
	}
	if err := a.Flush(); err != nil {
		t.Fatal(err)
	}
	for i, b := range prefix {
		if st.Bytes()[i] != b {
			t.Fatalf("byte %d of history rewritten", i)
		}
	}
	gotT, _, err := a.ReadRange(0, 299, 1<<20)
	if err != nil || len(gotT) != 300 {
		t.Fatalf("after append: %v len=%d", err, len(gotT))
	}
}

func TestIndexSerializationRoundTrip(t *testing.T) {
	var ts, vs []int64
	for i := 0; i < 300; i++ {
		ts = append(ts, int64(i*11))
		vs = append(vs, int64(i%13)-6)
	}
	a, _ := buildArchive(t, RejectRegression, ts, vs)
	blk, _ := a.ReadBlock(0)
	nv := append([]int64{}, blk.Values...)
	nv[0] = 42
	if err := a.Correct(0, blk.Times, nv); err != nil {
		t.Fatal(err)
	}
	enc := a.Index().Encode()
	ix2, err := DecodeIndex(enc)
	if err != nil {
		t.Fatal(err)
	}
	if len(ix2.Entries) != len(a.Index().Entries) {
		t.Fatalf("index round trip lost entries")
	}
	for i, e := range a.Index().Entries {
		if ix2.Entries[i] != e {
			t.Fatalf("entry %d mismatch: %+v vs %+v", i, ix2.Entries[i], e)
		}
	}
	enc[len(enc)/2] ^= 0xFF
	if _, err := DecodeIndex(enc); err != ErrChecksum {
		t.Fatalf("want ErrChecksum, got %v", err)
	}
}

func TestVerifyHeader(t *testing.T) {
	a, st := buildArchive(t, RejectRegression, []int64{1, 2, 3}, []int64{1, 2, 3})
	_ = a
	if err := VerifyHeader(st); err != nil {
		t.Fatal(err)
	}
	st.Bytes()[4] = 77
	if err := VerifyHeader(st); err != ErrVersion {
		t.Fatalf("want ErrVersion, got %v", err)
	}
	st.Bytes()[0] = 'X'
	if err := VerifyHeader(st); err != ErrFormat {
		t.Fatalf("want ErrFormat, got %v", err)
	}
}

func TestRandomRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 20; trial++ {
		st := &MemStore{}
		a := NewArchive(st, AllowRegression)
		a.SetMaxBlock(64)
		var allT, allV []int64
		tm := rng.Int63n(1000)
		for i := 0; i < 2000; i++ {
			if rng.Intn(100) == 0 {
				tm -= rng.Int63n(500) // regression -> new epoch
			} else {
				tm += rng.Int63n(50)
			}
			v := rng.Int63n(1<<30) - 1<<29
			if err := a.Add(tm, v); err != nil {
				t.Fatal(err)
			}
			allT = append(allT, tm)
			allV = append(allV, v)
		}
		if err := a.Flush(); err != nil {
			t.Fatal(err)
		}
		// Verify every block (in insertion order) decodes to the exact
		// input slice it covers.
		pos := 0
		for _, e := range a.Index().Entries {
			blk, err := a.readEntry(e)
			if err != nil {
				t.Fatal(err)
			}
			if blk.Times[0] != e.T0 || blk.Times[len(blk.Times)-1] != e.T1 {
				t.Fatalf("index range mismatch")
			}
			if !eqI64(blk.Times, allT[pos:pos+e.Count]) || !eqI64(blk.Values, allV[pos:pos+e.Count]) {
				t.Fatalf("trial %d: block content mismatch at %d", trial, pos)
			}
			pos += e.Count
		}
		if pos != len(allT) {
			t.Fatalf("covered %d of %d points", pos, len(allT))
		}
	}
}
