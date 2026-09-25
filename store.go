package rootstore

import (
	"errors"
	"fmt"
	"math"
)

// File names created inside the caller-specified directory.
const (
	MetaFileName = "root.meta" // two metadata slots at offsets 0 and SlotSize
	PageFileName = "pages.dat" // grow-only array of PageSize pages
)

var (
	// ErrNoValidSlot is returned when both metadata slots are corrupt;
	// the store refuses to open rather than guess.
	ErrNoValidSlot = errors.New("no valid metadata slot")
	// ErrSlotConflict is returned when both slots are valid, carry the
	// same sequence number, but different content.
	ErrSlotConflict = errors.New("conflicting slots with identical sequence")
	// ErrSeqExhausted is the explicit sequence-overflow policy: the
	// writer never wraps the sequence; at math.MaxUint64 it refuses to
	// commit and the repository must be re-initialized.
	ErrSeqExhausted = errors.New("sequence number exhausted")
	// ErrNoValidRoot is returned when valid slots exist but no slot's
	// root has a fully readable reachable closure.
	ErrNoValidRoot = errors.New("no slot with a readable root closure")
)

// Store publishes root pointers atomically through two metadata slots.
type Store struct {
	meta   File
	pages  *PageFile
	cur    Slot
	curIdx int
	hasCur bool
}

// Open opens (or initializes) the repository in the given FS and recovers
// the latest complete root:
//
//   - both slots valid, different seq -> newest by modular comparison;
//   - both valid, same seq, same content -> idempotent publish, use it;
//   - both valid, same seq, different content -> ErrSlotConflict;
//   - exactly one valid -> use it (a half-written new slot is ignored);
//   - none valid -> ErrNoValidSlot, refuse to open.
//
// The chosen slot's root must have a fully readable reachable closure;
// otherwise Open falls back to the older valid slot, and if none works,
// fails with ErrNoValidRoot. Recovery therefore yields only an old or a
// new complete root.
func Open(fsys FS) (*Store, error) {
	meta, err := fsys.Open(MetaFileName)
	if err != nil {
		return nil, fmt.Errorf("open meta: %w", err)
	}
	pageF, err := fsys.Open(PageFileName)
	if err != nil {
		return nil, fmt.Errorf("open pages: %w", err)
	}
	pf, err := openPageFile(pageF)
	if err != nil {
		return nil, err
	}
	s := &Store{meta: meta, pages: pf, curIdx: -1}
	size, err := meta.Size()
	if err != nil {
		return nil, err
	}
	if size == 0 {
		return s, nil // fresh repository; the first Commit publishes slot 0
	}
	slots, valid := readSlots(meta)
	if !valid[0] && !valid[1] {
		return nil, fmt.Errorf("%w: both slots corrupt, refusing to open", ErrNoValidSlot)
	}
	var order []int // candidate slots, newest first
	switch {
	case valid[0] && valid[1]:
		a, b := slots[0], slots[1]
		if a.Seq == b.Seq {
			if a != b {
				return nil, fmt.Errorf("%w: slot0=(%s) slot1=(%s)", ErrSlotConflict, a, b)
			}
			order = []int{0}
		} else if seqNewer(a.Seq, b.Seq) {
			order = []int{0, 1}
		} else {
			order = []int{1, 0}
		}
	case valid[0]:
		order = []int{0}
	default:
		order = []int{1}
	}
	for _, idx := range order {
		if VerifyClosure(pf, slots[idx].RootID) == nil {
			s.cur, s.curIdx, s.hasCur = slots[idx], idx, true
			return s, nil
		}
	}
	return nil, fmt.Errorf("%w: valid slots exist but no reachable root closure", ErrNoValidRoot)
}

func readSlots(meta File) (slots [2]Slot, valid [2]bool) {
	for i := 0; i < 2; i++ {
		buf := make([]byte, SlotSize)
		if _, err := meta.ReadAt(buf, int64(i)*SlotSize); err != nil {
			continue
		}
		sl, err := decodeSlot(buf)
		if err != nil {
			continue
		}
		slots[i], valid[i] = sl, true
	}
	return slots, valid
}

// PageWriter allocates and writes pages during a Commit.
type PageWriter struct{ pf *PageFile }

// New allocates a page id, writes the page and returns its id.
func (w *PageWriter) New(children []uint64, data []byte) (uint64, error) {
	id := w.pf.count
	w.pf.count++
	pg := &Page{ID: id, Children: children, Data: data}
	if err := w.pf.WritePage(pg); err != nil {
		return 0, err
	}
	return id, nil
}

// Commit builds a new page tree and atomically publishes its root.
//
// Write boundaries, in order:
//
//  1. pages: all new pages are written, then the page file is synced;
//  2. verify: the root and its whole reachable closure are re-read from
//     storage and must be complete before anything is published;
//  3. publish: the other slot is written in a single SlotSize WriteAt
//     (the atomic unit) with seq = current + 1;
//  4. durable: the meta file is synced.
//
// A crash anywhere leaves either the old or the new complete root. The
// sequence never wraps: at math.MaxUint64 Commit fails with
// ErrSeqExhausted.
func (s *Store) Commit(build func(w *PageWriter) (uint64, error)) error {
	if s.hasCur && s.cur.Seq == math.MaxUint64 {
		return ErrSeqExhausted
	}
	w := &PageWriter{pf: s.pages}
	root, err := build(w)
	if err != nil {
		return fmt.Errorf("build pages: %w", err)
	}
	if err := s.pages.Sync(); err != nil {
		return fmt.Errorf("sync pages: %w", err)
	}
	if err := VerifyClosure(s.pages, root); err != nil {
		return fmt.Errorf("pre-publish verification: %w", err)
	}
	next, idx := uint64(1), 0
	if s.hasCur {
		next, idx = s.cur.Seq+1, 1-s.curIdx
	}
	slot := Slot{Seq: next, RootID: root, Pages: s.pages.Count()}
	if _, err := s.meta.WriteAt(encodeSlot(slot), int64(idx)*SlotSize); err != nil {
		return fmt.Errorf("publish slot: %w", err)
	}
	if err := s.meta.Sync(); err != nil {
		return fmt.Errorf("sync meta: %w", err)
	}
	s.cur, s.curIdx, s.hasCur = slot, idx, true
	return nil
}

// Root returns the currently published slot.
func (s *Store) Root() (Slot, bool) { return s.cur, s.hasCur }

// Pages exposes the page file, e.g. for the independent verifier.
func (s *Store) Pages() *PageFile { return s.pages }

func (s *Store) Close() error {
	err := s.meta.Close()
	if err2 := s.pages.f.Close(); err == nil {
		err = err2
	}
	return err
}
