package netsec

import (
	"bytes"
	"fmt"
	"sort"
)

// VerifyChain verifies an archived audit chain and its external anchors.
//
// It requires window numbers to be strictly adjacent, sequence numbers to be
// continuous, each window's declared count to match its entries, each window's
// PrevHash to equal the recomputed preceding window head, and every immutable
// anchor to equal the recomputed chain head at that sequence number. The same
// window number observed twice with different contents is reported as a fork.
//
// The returned error is the first issue found. Use CheckChain to collect all
// issues, or DetectForks to inspect forks only.
func VerifyChain(windows []Window, anchors []Anchor) error {
	return CheckChain(windows, anchors).Err()
}

// Verify is an alias for VerifyChain.
func Verify(windows []Window, anchors []Anchor) error {
	return VerifyChain(windows, anchors)
}

// VerifyArchive is an alias for VerifyChain.
func VerifyArchive(windows []Window, anchors []Anchor) error {
	return VerifyChain(windows, anchors)
}

// VerifySegments is an alias for VerifyChain.
func VerifySegments(windows []Window, anchors []Anchor) error {
	return VerifyChain(windows, anchors)
}

// CheckChain performs the full verification and returns a report containing
// every detected issue. It never stops at the first failure.
func CheckChain(windows []Window, anchors []Anchor) *Report {
	report := &Report{Windows: len(windows), Anchors: len(anchors)}
	if len(windows) == 0 {
		report.add(newChainError(KindInvalid, 0, 0, "empty archive"))
		return report
	}

	unique := deduplicateWindows(report, windows)
	sort.Slice(unique, func(i, j int) bool {
		return unique[i].Header.Number < unique[j].Header.Number
	})
	report.Windows = len(unique)

	seqHeads := make(map[uint64][]byte)
	var prev *Window
	var prevHead []byte

	for i := range unique {
		window := &unique[i]

		if prev != nil {
			if window.Header.Number != prev.Header.Number+1 {
				report.add(newChainError(
					KindWindowGap,
					window.Header.Number,
					0,
					fmt.Sprintf("expected window %d after window %d", prev.Header.Number+1, prev.Header.Number),
				))
			}
			if prev.Header.Count <= ^uint64(0)-prev.Header.StartSeq {
				wantStart := prev.Header.StartSeq + prev.Header.Count
				if window.Header.StartSeq != wantStart {
					report.add(newChainError(
						KindSequenceGap,
						window.Header.Number,
						window.Header.StartSeq,
						fmt.Sprintf("expected start sequence %d after %d entries ending before it", wantStart, prev.Header.Count),
					))
				}
			}
		}

		if window.Header.Count != uint64(len(window.Entries)) {
			report.add(newChainError(
				KindCountMismatch,
				window.Header.Number,
				0,
				fmt.Sprintf("header count %d, actual entries %d", window.Header.Count, len(window.Entries)),
			))
		}

		if prev != nil && !bytes.Equal(window.Header.PrevHash, prevHead) {
			report.add(&ChainError{
				Kind:   KindPrevHashMismatch,
				Window: window.Header.Number,
				Detail: "header PrevHash does not equal the preceding window head",
				Want:   cloneBytes(prevHead),
				Got:    cloneBytes(window.Header.PrevHash),
			})
		}

		head := cloneBytes(window.Header.PrevHash)
		for j := range window.Entries {
			entry := &window.Entries[j]
			wantSeq := window.Header.StartSeq + uint64(j)
			if entry.Seq != wantSeq {
				report.add(newChainError(
					KindSequenceGap,
					window.Header.Number,
					entry.Seq,
					fmt.Sprintf("expected sequence %d", wantSeq),
				))
			}

			computed := ComputeEntryHash(head, entry.Seq, entry.Bytes())
			if len(entry.Hash) != 0 && !bytes.Equal(entry.Hash, computed) {
				report.add(&ChainError{
					Kind:   KindEntryHashMismatch,
					Window: window.Header.Number,
					Seq:    entry.Seq,
					Detail: "stored hash does not match recomputed hash",
					Want:   cloneBytes(entry.Hash),
					Got:    computed,
				})
			}

			if prior, ok := seqHeads[entry.Seq]; ok {
				detail := fmt.Sprintf("sequence %d appears in more than one window", entry.Seq)
				if !bytes.Equal(prior, computed) {
					detail = fmt.Sprintf("sequence %d appears twice with different hashes", entry.Seq)
				}
				report.add(newChainError(KindSequenceGap, window.Header.Number, entry.Seq, detail))
			} else {
				seqHeads[entry.Seq] = computed
			}

			head = computed
			report.Entries++
		}

		prev = &unique[i]
		prevHead = head
	}

	verifyAnchors(report, unique, seqHeads, anchors)
	return report
}

// AuditChain is an alias for CheckChain.
func AuditChain(windows []Window, anchors []Anchor) *Report {
	return CheckChain(windows, anchors)
}

// DetectForks reports every window number that was supplied with more than one
// distinct head. Copies that are byte-for-byte identical are not forks and are
// treated as duplicate input.
func DetectForks(windows []Window) []*ForkError {
	forks := forkErrors(windows)
	sort.Slice(forks, func(i, j int) bool {
		if forks[i].Window != forks[j].Window {
			return forks[i].Window < forks[j].Window
		}
		return bytes.Compare(forks[i].ExistingHead, forks[j].ExistingHead) < 0
	})
	return forks
}

func deduplicateWindows(report *Report, windows []Window) []Window {
	order := make([]uint64, 0, len(windows))
	groups := make(map[uint64][]Window, len(windows))
	for _, window := range windows {
		number := window.Header.Number
		if _, ok := groups[number]; !ok {
			order = append(order, number)
		}
		groups[number] = append(groups[number], window)
	}

	unique := make([]Window, 0, len(groups))
	for _, number := range order {
		group := groups[number]
		base := group[0]
		unique = append(unique, base)
		for _, other := range group[1:] {
			if windowsEqual(base, other) {
				continue
			}
			report.add(&ForkError{
				Window:          number,
				Existing:        cloneHeader(base.Header),
				Conflicting:     cloneHeader(other.Header),
				ExistingHead:    computeHeadUnchecked(base),
				ConflictingHead: computeHeadUnchecked(other),
			})
		}
	}
	return unique
}

func forkErrors(windows []Window) []*ForkError {
	groups := make(map[uint64][]Window)
	order := make([]uint64, 0, len(windows))
	for _, window := range windows {
		number := window.Header.Number
		if _, ok := groups[number]; !ok {
			order = append(order, number)
		}
		groups[number] = append(groups[number], window)
	}

	var forks []*ForkError
	for _, number := range order {
		group := groups[number]
		base := group[0]
		for _, other := range group[1:] {
			if windowsEqual(base, other) {
				continue
			}
			forks = append(forks, &ForkError{
				Window:          number,
				Existing:        cloneHeader(base.Header),
				Conflicting:     cloneHeader(other.Header),
				ExistingHead:    computeHeadUnchecked(base),
				ConflictingHead: computeHeadUnchecked(other),
			})
		}
	}
	return forks
}

func verifyAnchors(report *Report, windows []Window, seqHeads map[uint64][]byte, anchors []Anchor) {
	bySeq := make(map[uint64]Anchor, len(anchors))
	for _, anchor := range anchors {
		if len(anchor.Hash) == 0 {
			report.add(newChainError(KindInvalid, 0, anchor.Seq, "anchor has empty hash"))
			continue
		}
		if prior, ok := bySeq[anchor.Seq]; ok {
			if !bytes.Equal(prior.Hash, anchor.Hash) {
				report.add(&ChainError{
					Kind:   KindAnchorConflict,
					Seq:    anchor.Seq,
					Detail: "two immutable anchors disagree at the same sequence",
					Want:   cloneBytes(prior.Hash),
					Got:    cloneBytes(anchor.Hash),
				})
			}
			continue
		}
		bySeq[anchor.Seq] = anchor
	}

	for _, anchor := range anchors {
		if len(anchor.Hash) == 0 {
			continue
		}

		if computed, ok := seqHeads[anchor.Seq]; ok {
			if !bytes.Equal(computed, anchor.Hash) {
				report.add(&ChainError{
					Kind:   KindAnchorMismatch,
					Seq:    anchor.Seq,
					Detail: "recomputed chain head does not match immutable anchor",
					Want:   cloneBytes(anchor.Hash),
					Got:    computed,
				})
			}
			continue
		}

		if len(windows) != 0 {
			first := windows[0]
			if first.Header.StartSeq > 0 && anchor.Seq == first.Header.StartSeq-1 {
				if !bytes.Equal(first.Header.PrevHash, anchor.Hash) {
					report.add(&ChainError{
						Kind:   KindAnchorMismatch,
						Seq:    anchor.Seq,
						Detail: "anchor before the archive range does not match the first window PrevHash",
						Want:   cloneBytes(anchor.Hash),
						Got:    cloneBytes(first.Header.PrevHash),
					})
				}
				continue
			}
		}

		report.add(newChainError(
			KindAnchorMissing,
			0,
			anchor.Seq,
			"anchor sequence is not present in the supplied archive",
		))
	}
}

func windowsEqual(a, b Window) bool {
	if !headersEqual(a.Header, b.Header) {
		return false
	}
	if len(a.Entries) != len(b.Entries) {
		return false
	}
	for i := range a.Entries {
		x, y := a.Entries[i], b.Entries[i]
		if x.Seq != y.Seq ||
			!bytesEqual(x.Bytes(), y.Bytes()) ||
			!bytesEqual(x.Hash, y.Hash) {
			return false
		}
	}
	return true
}

func computeHeadUnchecked(w Window) []byte {
	head := cloneBytes(w.Header.PrevHash)
	for _, entry := range w.Entries {
		head = ComputeEntryHash(head, entry.Seq, entry.Bytes())
	}
	return head
}

func cloneHeader(header WindowHeader) WindowHeader {
	header.PrevHash = cloneBytes(header.PrevHash)
	return header
}
