package netsec

import (
	"errors"
	"fmt"
	"testing"
)

const windowSize = 2

func testRecords(count int) []Record {
	records := make([]Record, count)
	for i := range records {
		records[i] = Record{
			Seq:  uint64(i),
			Data: []byte(fmt.Sprintf("record-%02d", i)),
		}
	}
	return records
}

func buildTestChain(t *testing.T, recordCount int, anchorWindows ...uint64) ([]Window, []Anchor) {
	t.Helper()
	windows, err := BuildWindows(testRecords(recordCount), windowSize, 0, nil)
	if err != nil {
		t.Fatalf("BuildWindows: %v", err)
	}
	anchors, err := AnchorsForWindows(windows, anchorWindows...)
	if err != nil {
		t.Fatalf("AnchorsForWindows: %v", err)
	}
	return windows, anchors
}

func cloneWindows(windows []Window) []Window {
	cloned := make([]Window, len(windows))
	for i, window := range windows {
		cloned[i] = Window{
			Header: WindowHeader{
				Number:   window.Header.Number,
				StartSeq: window.Header.StartSeq,
				Count:    window.Header.Count,
				PrevHash: cloneBytes(window.Header.PrevHash),
			},
			Entries: make([]Entry, len(window.Entries)),
		}
		for j, entry := range window.Entries {
			cloned[i].Entries[j] = Entry{
				Seq:     entry.Seq,
				Payload: cloneBytes(entry.Payload),
				Data:    cloneBytes(entry.Data),
				Hash:    cloneBytes(entry.Hash),
			}
		}
	}
	return cloned
}

func copyAnchors(anchors []Anchor) []Anchor {
	cloned := make([]Anchor, len(anchors))
	for i, anchor := range anchors {
		cloned[i] = Anchor{Seq: anchor.Seq, Hash: cloneBytes(anchor.Hash)}
	}
	return cloned
}

func windowIndex(windows []Window, number uint64) int {
	for i := range windows {
		if windows[i].Header.Number == number {
			return i
		}
	}
	return -1
}

// resealFrom simulates an attacker who has write access and recomputes every
// hash and PrevHash from the named window through the tail.
func resealFrom(t *testing.T, windows []Window, from uint64) {
	t.Helper()
	start := windowIndex(windows, from)
	if start < 0 {
		t.Fatalf("window %d not found", from)
	}
	for i := start; i < len(windows); i++ {
		if i > 0 {
			head, err := WindowHead(windows[i-1])
			if err != nil {
				t.Fatalf("WindowHead(%d): %v", windows[i-1].Header.Number, err)
			}
			windows[i].Header.PrevHash = head
		}
		if err := SealWindow(&windows[i]); err != nil {
			t.Fatalf("SealWindow(%d): %v", windows[i].Header.Number, err)
		}
	}
}

func TestVerifyValidChain(t *testing.T) {
	windows, anchors := buildTestChain(t, 12, 1, 3, 5)
	if err := VerifyChain(windows, anchors); err != nil {
		t.Fatalf("valid chain rejected: %v", err)
	}

	// Verification is order-independent: window numbers define chain order.
	shuffled := append([]Window{}, windows[3:]...)
	shuffled = append(shuffled, windows[:3]...)
	if err := VerifyChain(shuffled, anchors); err != nil {
		t.Fatalf("shuffled valid chain rejected: %v", err)
	}

	// Byte-identical duplicate input (for example, a repeated replica) is not a
	// fork.
	duplicated := append(cloneWindows(windows), cloneWindows(windows)...)
	if err := VerifyChain(duplicated, anchors); err != nil {
		t.Fatalf("identical duplicate copies rejected: %v", err)
	}

	report := CheckChain(windows, anchors)
	if !report.OK() {
		t.Fatalf("report not OK: %v", report.Issues)
	}
}

func TestVerifyValidSuffixWithBoundaryAnchor(t *testing.T) {
	windows, _ := buildTestChain(t, 12, 1, 3, 5)

	// Verify a suffix starting at window 2. The caller supplies the published
	// head of window 1 as the immutable boundary anchor.
	anchor, err := WindowAnchor(windows[1])
	if err != nil {
		t.Fatalf("WindowAnchor: %v", err)
	}
	if err := VerifyChain(windows[2:], []Anchor{anchor}); err != nil {
		t.Fatalf("valid suffix rejected: %v", err)
	}

	bad := Anchor{Seq: anchor.Seq, Hash: cloneBytes(anchor.Hash)}
	bad.Hash[0] ^= 0xff
	if err := VerifyChain(windows[2:], []Anchor{bad}); !errors.Is(err, ErrAnchorMismatch) {
		t.Fatalf("bad boundary anchor: got %v, want ErrAnchorMismatch", err)
	}
}

func TestForkReportedForConflictingWindowHeads(t *testing.T) {
	windows, _ := buildTestChain(t, 12, 1, 3, 5)
	forked := cloneWindows(windows)

	// Same window number, different header metadata.
	forked[2].Header.StartSeq += 1
	forked[2].Header.PrevHash = ComputeEntryHash(nil, 99, []byte("fork"))

	report := CheckChain(append(cloneWindows(windows), forked...), nil)
	forks := report.Forks()
	if len(forks) != 1 {
		t.Fatalf("got %d forks, want 1 (%v)", len(forks), report.Issues)
	}
	if forks[0].Window != 2 {
		t.Fatalf("fork reported for window %d, want 2", forks[0].Window)
	}
	if err := VerifyChain(append(cloneWindows(windows), forked...), nil); !errors.Is(err, ErrFork) {
		t.Fatalf("got %v, want ErrFork", err)
	}
}

func TestForkReportedForSameHeaderDifferentContent(t *testing.T) {
	windows, _ := buildTestChain(t, 12, 1, 3, 5)
	forked := cloneWindows(windows)
	forked[2].Entries[0].Payload = []byte("replica-b-record")
	resealFrom(t, forked, 2)

	if err := VerifyChain(append(cloneWindows(windows), forked...), nil); !errors.Is(err, ErrFork) {
		t.Fatalf("got %v, want ErrFork", err)
	}
}

func TestCrossReplicaSpliceByAnchors(t *testing.T) {
	windows, anchors := buildTestChain(t, 12, 1, 3, 5)

	// A second replica shares the prefix but forks at window 2 and then
	// recomputes its entire tail.
	replicaB := cloneWindows(windows)
	replicaB[2].Entries[0].Payload = []byte("replica-b-record")
	resealFrom(t, replicaB, 2)

	// Splicing windows 0-1 from A with windows 2+ from B is internally hash
	// consistent, because B was resealed from A's window 1 head. Only the
	// immutable anchor published for A's tail exposes the splice.
	spliced := append(cloneWindows(windows[:2]), cloneWindows(replicaB[2:])...)
	if err := VerifyChain(spliced, anchors); !errors.Is(err, ErrAnchorMismatch) {
		t.Fatalf("got %v, want ErrAnchorMismatch", err)
	}
}

func TestCrossReplicaSpliceBreaksBackLink(t *testing.T) {
	windows, anchors := buildTestChain(t, 12, 1, 3, 5)

	replicaB := cloneWindows(windows)
	replicaB[2].Entries[0].Payload = []byte("replica-b-record")
	resealFrom(t, replicaB, 2)

	// Keep A's window 2 and take B's window 2 instead, then reattach A's
	// already-linked tail window 3. Window 2 sits on the shared prefix, but
	// window 3 still points at A's original window 2 head.
	spliced := append(cloneWindows(windows[:2]), cloneWindows(replicaB[2:3])...)
	spliced = append(spliced, cloneWindows(windows[3:])...)
	err := VerifyChain(spliced, anchors)
	if !errors.Is(err, ErrPrevHashMismatch) && !errors.Is(err, ErrAnchorMismatch) {
		t.Fatalf("got %v, want ErrPrevHashMismatch or ErrAnchorMismatch", err)
	}
}

func TestDeletedMiddleWindowRejected(t *testing.T) {
	windows, anchors := buildTestChain(t, 12, 1, 3, 5)

	// Delete window 2 but leave its neighbours untouched.
	deleted := append(cloneWindows(windows[:2]), cloneWindows(windows[3:])...)
	if err := VerifyChain(deleted, anchors); !errors.Is(err, ErrWindowGap) {
		t.Fatalf("got %v, want ErrWindowGap", err)
	}

	// Renumber the surviving windows to hide the gap. The sequence range and
	// back link now disagree with the shifted window numbers.
	renumbered := cloneWindows(deleted)
	for i := range renumbered {
		renumbered[i].Header.Number = uint64(i)
	}
	if err := VerifyChain(renumbered, anchors); err == nil {
		t.Fatal("renumbered deleted chain accepted")
	}
}

func TestDeletedWindowAndForgedReplacementRejectedByAnchor(t *testing.T) {
	_, anchors := buildTestChain(t, 12, 1, 3, 5)

	// Remove window 2's records, close the sequence gap, renumber the surviving
	// windows, and recompute everything. This yields a shorter but internally
	// consistent chain.
	records := testRecords(12)
	kept := append(append([]Record{}, records[:4]...), records[6:]...)
	for i := range kept {
		kept[i].Seq = uint64(i)
	}
	rebuilt, err := BuildWindows(kept, windowSize, 0, nil)
	if err != nil {
		t.Fatalf("BuildWindows after deletion: %v", err)
	}

	// Forge a new tail window so the archive still reaches the anchored
	// sequence number published before the deletion.
	prevHead, err := WindowHead(rebuilt[len(rebuilt)-1])
	if err != nil {
		t.Fatalf("WindowHead: %v", err)
	}
	forged := Window{
		Header: WindowHeader{
			Number:   uint64(len(rebuilt)),
			StartSeq: 10,
			PrevHash: prevHead,
		},
		Entries: []Entry{
			{Seq: 10, Payload: []byte("forged-10")},
			{Seq: 11, Payload: []byte("forged-11")},
		},
	}
	if err := SealWindow(&forged); err != nil {
		t.Fatalf("SealWindow: %v", err)
	}

	input := append(cloneWindows(rebuilt), forged)
	if err := VerifyChain(input, anchors); !errors.Is(err, ErrAnchorMismatch) {
		t.Fatalf("got %v, want ErrAnchorMismatch", err)
	}
}

func TestDeletedTailLeavesAnchorMissing(t *testing.T) {
	windows, anchors := buildTestChain(t, 12, 1, 3, 5)
	deleteFrom := windowIndex(windows, 4)
	truncated := cloneWindows(windows[:deleteFrom])

	// The immutable anchor for window 5 cannot simply disappear with the
	// deleted tail.
	if err := VerifyChain(truncated, anchors); !errors.Is(err, ErrAnchorMissing) {
		t.Fatalf("got %v, want ErrAnchorMissing", err)
	}
}

func TestRecomputedTailRejectedByAnchor(t *testing.T) {
	windows, anchors := buildTestChain(t, 12, 1, 3, 5)
	forged := cloneWindows(windows)

	// Alter an audited record in window 3, then recompute every hash and
	// PrevHash through the tail. Internal hashes are now self-consistent.
	forged[3].Entries[1].Payload = []byte("forged-record")
	resealFrom(t, forged, 3)

	err := VerifyChain(forged, anchors)
	if !errors.Is(err, ErrAnchorMismatch) {
		t.Fatalf("got %v, want ErrAnchorMismatch", err)
	}

	// The report must still show anchor 1 (before the forged region) as valid
	// while anchor 5 (inside the recomputed tail) fails.
	report := CheckChain(forged, anchors)
	if len(report.Issues) == 0 {
		t.Fatal("no issues in forged report")
	}
}

func TestTamperBeforeAnchorRejected(t *testing.T) {
	windows, anchors := buildTestChain(t, 12, 1, 3, 5)
	tampered := cloneWindows(windows)
	tampered[0].Entries[0].Payload = []byte("tampered-before-anchor")
	resealFrom(t, tampered, 0)

	err := VerifyChain(tampered, anchors)
	if !errors.Is(err, ErrAnchorMismatch) {
		t.Fatalf("got %v, want ErrAnchorMismatch", err)
	}
}

func TestTamperAfterAnchorRejectedByNextAnchor(t *testing.T) {
	windows, anchors := buildTestChain(t, 12, 1, 3, 5)
	tampered := cloneWindows(windows)

	// Window 2 is after anchor 1 and before anchor 3.
	tampered[2].Entries[1].Payload = []byte("tampered-between-anchors")
	resealFrom(t, tampered, 2)

	err := VerifyChain(tampered, anchors)
	if !errors.Is(err, ErrAnchorMismatch) {
		t.Fatalf("got %v, want ErrAnchorMismatch", err)
	}
}

func TestStoredEntryHashTamperRejected(t *testing.T) {
	windows, anchors := buildTestChain(t, 12, 1, 3, 5)
	tampered := cloneWindows(windows)
	tampered[1].Entries[0].Hash[0] ^= 0xff

	if err := VerifyChain(tampered, anchors); !errors.Is(err, ErrEntryHashMismatch) {
		t.Fatalf("got %v, want ErrEntryHashMismatch", err)
	}
}

func TestWindowCountMismatchRejected(t *testing.T) {
	windows, anchors := buildTestChain(t, 12, 1, 3, 5)
	tampered := cloneWindows(windows)
	tampered[1].Header.Count++

	if err := VerifyChain(tampered, anchors); !errors.Is(err, ErrCountMismatch) {
		t.Fatalf("got %v, want ErrCountMismatch", err)
	}
}

func TestAnchorConflictRejected(t *testing.T) {
	windows, anchors := buildTestChain(t, 12, 1, 3, 5)
	conflicting := copyAnchors(anchors)
	conflicting[0].Hash = ComputeEntryHash(nil, conflicting[0].Seq, []byte("different anchor"))

	input := append(anchors, conflicting...)
	if err := VerifyChain(windows, input); !errors.Is(err, ErrAnchorConflict) {
		t.Fatalf("got %v, want ErrAnchorConflict", err)
	}
}

func TestSequenceGapRejected(t *testing.T) {
	windows, anchors := buildTestChain(t, 12, 1, 3, 5)
	tampered := cloneWindows(windows)
	tampered[1].Header.StartSeq++

	if err := VerifyChain(tampered, anchors); !errors.Is(err, ErrSequenceGap) {
		t.Fatalf("got %v, want ErrSequenceGap", err)
	}
}

func TestWindowHeadAndSeal(t *testing.T) {
	windows, _ := buildTestChain(t, 12, 1, 3, 5)
	for _, window := range windows {
		head, err := WindowHead(window)
		if err != nil {
			t.Fatalf("WindowHead: %v", err)
		}
		anchor, err := WindowAnchor(window)
		if err != nil {
			t.Fatalf("WindowAnchor: %v", err)
		}
		if !bytesEqual(head, anchor.Hash) {
			t.Fatalf("window %d head differs from anchor", window.Header.Number)
		}
	}
}
