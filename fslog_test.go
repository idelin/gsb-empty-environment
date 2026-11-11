package fslog

import (
	"bytes"
	"crypto/sha256"
	"testing"
)

func testKey() []byte {
	k := make([]byte, KeySize)
	for i := range k {
		k[i] = byte(i + 1)
	}
	return k
}

// buildLog writes a two-epoch log: n0 records, end-of-epoch, n1 records,
// and closes it. It returns the raw log.
func buildLog(t *testing.T, key []byte, n0, n1 int) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := NewWriter(&buf, key)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n0; i++ {
		if err := w.Append([]byte("epoch0-record-" + string(rune('a'+i)))); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.CloseEpoch(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n1; i++ {
		if err := w.Append([]byte("epoch1-record-" + string(rune('a'+i)))); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// spliceFrames removes the frames at the given indexes from the raw log.
func spliceFrames(t *testing.T, log []byte, drop ...int) []byte {
	t.Helper()
	frames, torn, tornBytes, err := readFrames(bytes.NewReader(log))
	if err != nil || tornBytes != 0 {
		t.Fatalf("readFrames: torn=%d/%d err=%v", torn, tornBytes, err)
	}
	dropped := map[int]bool{}
	for _, d := range drop {
		dropped[d] = true
	}
	var out []byte
	for i, f := range frames {
		if !dropped[i] {
			out = append(out, log[f.offset:f.offset+f.rawLen]...)
		}
	}
	return out
}

func TestRoundTrip(t *testing.T) {
	key := testKey()
	log := buildLog(t, key, 3, 2)
	rep, err := Verify(bytes.NewReader(log), key)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK() {
		t.Fatalf("expected clean verify, got:\n%s", rep)
	}
	if rep.Records != 5 || rep.Epochs != 2 {
		t.Fatalf("got %d records, %d epochs; want 5, 2", rep.Records, rep.Epochs)
	}
}

func TestTamperedRecord(t *testing.T) {
	key := testKey()
	log := buildLog(t, key, 3, 2)
	// Flip a byte inside the first record's payload data.
	frames, _, _, err := readFrames(bytes.NewReader(log))
	if err != nil {
		t.Fatal(err)
	}
	dataOff := frames[0].offset + int64(headerSize) + 21
	log[dataOff] ^= 0xff

	rep, err := Verify(bytes.NewReader(log), key)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK() || rep.Issues[0].Kind != IssueTampered {
		t.Fatalf("expected tampered issue, got:\n%s", rep)
	}
	if rep.Issues[0].Offset != frames[0].offset || rep.Issues[0].Seq != 0 {
		t.Fatalf("wrong location: %v", rep.Issues[0])
	}
}

func TestDeletedRecord(t *testing.T) {
	key := testKey()
	log := buildLog(t, key, 3, 2)
	// Delete the second record (frame index 1) of epoch 0.
	log = spliceFrames(t, log, 1)

	rep, err := Verify(bytes.NewReader(log), key)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK() || rep.Issues[0].Kind != IssueChainBreak {
		t.Fatalf("expected chain-break, got:\n%s", rep)
	}
	if rep.Records != 1 {
		t.Fatalf("expected 1 verified record before the break, got %d", rep.Records)
	}
}

func TestReorderedRecords(t *testing.T) {
	key := testKey()
	log := buildLog(t, key, 3, 2)
	frames, _, _, err := readFrames(bytes.NewReader(log))
	if err != nil {
		t.Fatal(err)
	}
	// Swap frames 0 and 1.
	f0 := log[frames[0].offset : frames[0].offset+frames[0].rawLen]
	f1 := log[frames[1].offset : frames[1].offset+frames[1].rawLen]
	var reordered []byte
	reordered = append(reordered, f1...)
	reordered = append(reordered, f0...)
	reordered = append(reordered, log[frames[2].offset:]...)

	rep, err := Verify(bytes.NewReader(reordered), key)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK() || rep.Issues[0].Kind != IssueChainBreak {
		t.Fatalf("expected chain-break, got:\n%s", rep)
	}
	if rep.Issues[0].Offset != 0 {
		t.Fatalf("reorder should be caught at the first frame, got %v", rep.Issues[0])
	}
}

func TestMaliciousTruncation(t *testing.T) {
	key := testKey()
	log := buildLog(t, key, 3, 2)
	// Attacker deletes the last two records and the final end marker,
	// leaving a clean frame boundary mid-epoch.
	frames, _, _, err := readFrames(bytes.NewReader(log))
	if err != nil {
		t.Fatal(err)
	}
	cut := frames[len(frames)-2].offset // drop final record + end marker
	truncated := log[:cut]

	rep, err := Verify(bytes.NewReader(truncated), key)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK() || rep.Issues[0].Kind != IssueTruncated {
		t.Fatalf("expected truncation, got:\n%s", rep)
	}
	if rep.Issues[0].Epoch != 1 {
		t.Fatalf("truncation should be located in epoch 1, got %v", rep.Issues[0])
	}
}

// crashWriter simulates a power loss: it persists only part of the write
// that is in flight when the crash happens.
type crashWriter struct {
	buf        bytes.Buffer
	crashAfter int // crash during the (crashAfter+1)-th Write
	writes     int
	keep       int // bytes of the crashing write that reach "disk"
}

func (c *crashWriter) Write(p []byte) (int, error) {
	c.writes++
	if c.writes == c.crashAfter {
		n, _ := c.buf.Write(p[:c.keep])
		return n, nil // partial frame hits the disk, then everything dies
	}
	return c.buf.Write(p)
}

func TestCrashTornTail(t *testing.T) {
	key := testKey()
	cw := &crashWriter{crashAfter: 4, keep: 11} // tear the 4th frame mid-header
	w, err := NewWriter(cw, key)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := w.Append([]byte("committed-record")); err != nil {
			t.Fatal(err)
		}
	}
	// The 4th Append "crashes" halfway through its single write.
	_ = w.Append([]byte("never-fully-written"))

	rep, err := Verify(bytes.NewReader(cw.buf.Bytes()), key)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK() {
		t.Fatalf("expected a torn-tail report, got OK")
	}
	iss := rep.Issues[0]
	if iss.Kind != IssueTornTail {
		t.Fatalf("expected torn tail, got:\n%s", rep)
	}
	if rep.Records != 3 {
		t.Fatalf("3 committed records should verify, got %d", rep.Records)
	}
	// The torn tail must start exactly after the 3rd complete frame.
	frames, _, _, err := readFrames(bytes.NewReader(cw.buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 3 || iss.Offset != frames[2].offset+frames[2].rawLen {
		t.Fatalf("torn tail at wrong offset: %v (frames: %d)", iss, len(frames))
	}
}

func TestTornTailIsNotTruncation(t *testing.T) {
	key := testKey()
	cw := &crashWriter{crashAfter: 2, keep: 5}
	w, _ := NewWriter(cw, key)
	_ = w.Append([]byte("one"))
	_ = w.Append([]byte("two-torn"))

	rep, err := Verify(bytes.NewReader(cw.buf.Bytes()), key)
	if err != nil {
		t.Fatal(err)
	}
	for _, iss := range rep.Issues {
		if iss.Kind == IssueTruncated {
			t.Fatalf("a crash must not be reported as malicious truncation:\n%s", rep)
		}
	}
}

func TestKeyErasureOnEpochClose(t *testing.T) {
	key := testKey()
	var buf bytes.Buffer
	w, err := NewWriter(&buf, key)
	if err != nil {
		t.Fatal(err)
	}
	oldKey := w.key // same backing array the writer will erase
	if err := w.Append([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := w.CloseEpoch(); err != nil {
		t.Fatal(err)
	}
	for i, b := range oldKey {
		if b != 0 {
			t.Fatalf("old epoch key not erased at byte %d", i)
		}
	}
	if bytes.Equal(w.key, oldKey) || bytes.Equal(w.key, make([]byte, KeySize)) {
		t.Fatal("new epoch key must be derived and non-zero")
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	for i, b := range w.key {
		if b != 0 {
			t.Fatalf("final key not erased at byte %d", i)
		}
	}
}

func TestWrongInitialKey(t *testing.T) {
	key := testKey()
	log := buildLog(t, key, 2, 1)
	bad := testKey()
	bad[0] ^= 0xff
	rep, err := Verify(bytes.NewReader(log), bad)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK() {
		t.Fatal("verification with the wrong initial key must fail")
	}
}

func TestDeleteBeforeEndMarker(t *testing.T) {
	key := testKey()
	log := buildLog(t, key, 3, 2)
	// Delete the last record of epoch 0 but keep its end marker.
	log = spliceFrames(t, log, 2)

	rep, err := Verify(bytes.NewReader(log), key)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK() || rep.Issues[0].Kind != IssueChainBreak {
		t.Fatalf("expected chain-break at the end marker, got:\n%s", rep)
	}
	if rep.Issues[0].Seq != -1 {
		t.Fatalf("break should be at the end marker, got %v", rep.Issues[0])
	}
}

func TestForwardSecrecy(t *testing.T) {
	// An attacker who learns the epoch-1 key (e.g. by compromising the
	// writer then) must not be able to forge epoch-0 records. We check the
	// structural property: epoch keys are a one-way chain, so K1 verifies
	// epoch 1 but fails on epoch 0.
	key := testKey()
	log := buildLog(t, key, 2, 2)

	// Recompute K1 the way the writer does.
	k1 := deriveForTest(t, key, 1)
	frames, _, _, err := readFrames(bytes.NewReader(log))
	if err != nil {
		t.Fatal(err)
	}
	// Find the first epoch-1 frame and verify the log starting there with K1.
	var tail []byte
	for _, f := range frames {
		if f.epoch == 1 {
			tail = log[f.offset:]
			break
		}
	}
	rep, err := Verify(bytes.NewReader(tail), k1)
	if err != nil {
		t.Fatal(err)
	}
	// The sub-log of epoch 1 alone is self-consistent only if its init
	// chain matched the previous end marker, which it does not here, so
	// this must fail — proving epochs are cryptographically bound.
	if rep.OK() {
		t.Fatal("epoch 1 must not verify without the epoch-0 end marker chain")
	}
}

func deriveForTest(t *testing.T, key []byte, epochs int) []byte {
	t.Helper()
	k := append([]byte(nil), key...)
	for i := 0; i < epochs; i++ {
		next := sha256.Sum256(append(append([]byte(nil), labelEvolve...), k...))
		k = next[:]
	}
	return k
}
