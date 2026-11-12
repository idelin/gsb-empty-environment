package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
)

// Entry is one audit record in a window.
//
// Seq is the global, monotonically increasing audit sequence number. Payload is
// the audited record. Data is accepted as an alias for Payload; Payload wins
// when both are set. Hash is the stored chain hash for the entry. It is
// optional: when present, verification recomputes the hash and requires it to
// match.
type Entry struct {
	Seq     uint64
	Payload []byte
	Data    []byte
	Hash    []byte
}

// Bytes returns the audited payload represented by the entry.
func (e Entry) Bytes() []byte {
	if len(e.Payload) != 0 {
		return e.Payload
	}
	return e.Data
}

// WindowHeader is the durable header of one time window. It binds the window
// number and sequence range to the previous window's head hash.
type WindowHeader struct {
	Number   uint64
	StartSeq uint64
	Count    uint64
	PrevHash []byte
}

// Window is a contiguous segment of an audit hash chain.
type Window struct {
	Header  WindowHeader
	Entries []Entry
}

// Segment is an alias for Window, matching archive terminology.
type Segment = Window

// ChainWindow is an alias for Window.
type ChainWindow = Window

// Anchor is an externally published chain head. Seq identifies the audit
// sequence number of the head and Hash is its published hash. Anchors are
// supplied by the caller and are treated as immutable.
type Anchor struct {
	Seq  uint64
	Hash []byte
}

// Checkpoint is an alias for Anchor.
type Checkpoint = Anchor

// Record is a convenience input for building sealed windows.
type Record struct {
	Seq  uint64
	Data []byte
}

var entryHashDomain = []byte("example.org/netsec/q0277/audit-entry/v1\x00")

// ComputeEntryHash computes the chain hash for one entry.
//
// The hash commits to the previous chain head, the entry sequence number, the
// payload length, and the payload. The length delimiter prevents ambiguous
// concatenations.
func ComputeEntryHash(prevHash []byte, seq uint64, payload []byte) []byte {
	h := sha256.New()
	h.Write(entryHashDomain)
	h.Write(prevHash)

	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], seq)
	h.Write(buf[:])
	binary.BigEndian.PutUint64(buf[:], uint64(len(payload)))
	h.Write(buf[:])
	h.Write(payload)

	return h.Sum(nil)
}

// HashEntry is an alias for ComputeEntryHash.
func HashEntry(prevHash []byte, seq uint64, payload []byte) []byte {
	return ComputeEntryHash(prevHash, seq, payload)
}

// WindowHead computes the head hash of a window by replaying its entry chain.
// It validates the declared count, entry sequence numbers, and any stored
// entry hashes.
func WindowHead(w Window) ([]byte, error) {
	if w.Header.Count != uint64(len(w.Entries)) {
		return nil, &ChainError{
			Kind:   KindCountMismatch,
			Window: w.Header.Number,
			Detail: fmt.Sprintf("header count %d, actual entries %d", w.Header.Count, len(w.Entries)),
		}
	}

	head := cloneBytes(w.Header.PrevHash)
	for i, entry := range w.Entries {
		wantSeq := w.Header.StartSeq + uint64(i)
		if entry.Seq != wantSeq {
			return nil, &ChainError{
				Kind:   KindSequenceGap,
				Window: w.Header.Number,
				Seq:    entry.Seq,
				Detail: fmt.Sprintf("expected sequence %d", wantSeq),
			}
		}

		computed := ComputeEntryHash(head, entry.Seq, entry.Bytes())
		if len(entry.Hash) != 0 && !bytes.Equal(entry.Hash, computed) {
			return nil, &ChainError{
				Kind:   KindEntryHashMismatch,
				Window: w.Header.Number,
				Seq:    entry.Seq,
				Want:   cloneBytes(entry.Hash),
				Got:    computed,
			}
		}
		head = computed
	}

	return head, nil
}

// SealWindow fills in Count and every entry Hash from the window's PrevHash.
// Entries must already carry consecutive Seq values starting at StartSeq.
func SealWindow(w *Window) error {
	if w == nil {
		return &ChainError{Kind: KindInvalid, Detail: "nil window"}
	}

	w.Header.Count = uint64(len(w.Entries))
	head := cloneBytes(w.Header.PrevHash)
	for i := range w.Entries {
		entry := &w.Entries[i]
		wantSeq := w.Header.StartSeq + uint64(i)
		if entry.Seq != wantSeq {
			return &ChainError{
				Kind:   KindSequenceGap,
				Window: w.Header.Number,
				Seq:    entry.Seq,
				Detail: fmt.Sprintf("expected sequence %d", wantSeq),
			}
		}
		entry.Hash = ComputeEntryHash(head, entry.Seq, entry.Bytes())
		head = entry.Hash
	}
	return nil
}

// NewWindow constructs and seals a window from consecutive records.
func NewWindow(number uint64, prevHash []byte, records []Record) (Window, error) {
	if len(records) == 0 {
		return Window{}, &ChainError{
			Kind:   KindInvalid,
			Window: number,
			Detail: "cannot infer start sequence for an empty window",
		}
	}

	entries := make([]Entry, len(records))
	for i, record := range records {
		entries[i] = Entry{Seq: record.Seq, Payload: cloneBytes(record.Data)}
	}
	w := Window{
		Header: WindowHeader{
			Number:   number,
			StartSeq: records[0].Seq,
			Count:    uint64(len(entries)),
			PrevHash: cloneBytes(prevHash),
		},
		Entries: entries,
	}
	if err := SealWindow(&w); err != nil {
		return Window{}, err
	}
	return w, nil
}

// BuildWindows splits consecutive records into sealed windows. Window numbers
// start at firstWindow and increment by one. Each window's PrevHash is the
// preceding window's head hash.
func BuildWindows(records []Record, windowSize int, firstWindow uint64, prevHash []byte) ([]Window, error) {
	if windowSize <= 0 {
		return nil, &ChainError{Kind: KindInvalid, Detail: "window size must be positive"}
	}
	if len(records) == 0 {
		return nil, &ChainError{Kind: KindInvalid, Detail: "no records"}
	}
	for i := 1; i < len(records); i++ {
		if records[i].Seq != records[i-1].Seq+1 {
			return nil, &ChainError{
				Kind:   KindSequenceGap,
				Seq:    records[i].Seq,
				Detail: fmt.Sprintf("records are not consecutive after sequence %d", records[i-1].Seq),
			}
		}
	}

	windowCount := (len(records) + windowSize - 1) / windowSize
	windows := make([]Window, 0, windowCount)
	head := cloneBytes(prevHash)
	for offset := 0; offset < len(records); offset += windowSize {
		end := offset + windowSize
		if end > len(records) {
			end = len(records)
		}
		number := firstWindow + uint64(len(windows))
		window, err := NewWindow(number, head, records[offset:end])
		if err != nil {
			return nil, err
		}
		windows = append(windows, window)
		head, err = WindowHead(window)
		if err != nil {
			return nil, err
		}
	}
	return windows, nil
}

// WindowAnchor returns the immutable anchor for the head of w.
func WindowAnchor(w Window) (Anchor, error) {
	if len(w.Entries) == 0 {
		return Anchor{}, &ChainError{
			Kind:   KindInvalid,
			Window: w.Header.Number,
			Detail: "empty window has no entry head",
		}
	}
	head, err := WindowHead(w)
	if err != nil {
		return Anchor{}, err
	}
	return Anchor{Seq: w.Entries[len(w.Entries)-1].Seq, Hash: head}, nil
}

// AnchorsForWindows returns anchors for the heads of the requested window
// numbers. Duplicate requests produce one anchor.
func AnchorsForWindows(windows []Window, numbers ...uint64) ([]Anchor, error) {
	wanted := make(map[uint64]struct{}, len(numbers))
	for _, number := range numbers {
		wanted[number] = struct{}{}
	}

	anchors := make([]Anchor, 0, len(numbers))
	found := make(map[uint64]struct{}, len(numbers))
	for _, window := range windows {
		if _, ok := wanted[window.Header.Number]; !ok {
			continue
		}
		if _, ok := found[window.Header.Number]; ok {
			continue
		}
		anchor, err := WindowAnchor(window)
		if err != nil {
			return nil, err
		}
		anchors = append(anchors, anchor)
		found[window.Header.Number] = struct{}{}
	}

	for _, number := range numbers {
		if _, ok := found[number]; !ok {
			return nil, &ChainError{
				Kind:   KindAnchorMissing,
				Window: number,
				Detail: "requested anchor window is absent",
			}
		}
	}

	sort.Slice(anchors, func(i, j int) bool { return anchors[i].Seq < anchors[j].Seq })
	return anchors, nil
}

func cloneBytes(src []byte) []byte {
	if src == nil {
		return nil
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}
