package descriptor

import (
	"fmt"
	"math"
	"sort"
)

// PageID identifies a physical page in the caller's page store.
type PageID uint64

// PageStore supplies immutable page snapshots to the validator. ReadPage may
// return a shared slice; the validator copies it before use.
type PageStore interface {
	PageSize() uint64
	ReadPage(id PageID) ([]byte, error)
}

// PageFreezer pins, snapshots, or otherwise stabilizes a page against changes
// by the untrusted plugin. Submit calls FreezePage for all referenced pages
// before it calls Submitter. Freezing is about the validation view: an
// implementation must still allow the committed device operation to update a
// page that is also a write target.
type PageFreezer interface {
	FreezePage(id PageID) error
}

// Translator maps a byte address to a page and an offset within that page.
type Translator interface {
	Translate(addr Address) (PageID, uint64, error)
}

type contiguousTranslator struct {
	pageSize uint64
}

func (t contiguousTranslator) Translate(addr Address) (PageID, uint64, error) {
	if t.pageSize == 0 {
		return 0, 0, fmt.Errorf("%w: zero page size", ErrPage)
	}
	return PageID(uint64(addr) / t.pageSize), uint64(addr) % t.pageSize, nil
}

type snapshot struct {
	store    PageStore
	tr       Translator
	pageSize uint64
	limits   Limits

	pages map[PageID][]byte
	refs  map[PageID]struct{}
}

func newSnapshot(store PageStore, tr Translator, pageSize uint64, limits Limits) *snapshot {
	return &snapshot{
		store:    store,
		tr:       tr,
		pageSize: pageSize,
		limits:   limits,
		pages:    make(map[PageID][]byte),
		refs:     make(map[PageID]struct{}),
	}
}

func (s *snapshot) page(id PageID) ([]byte, error) {
	if data, ok := s.pages[id]; ok {
		return data, nil
	}
	if uint64(len(s.pages)) >= s.limits.MaxPages {
		return nil, fmt.Errorf("%w: more than %d referenced pages", ErrLimit, s.limits.MaxPages)
	}
	if _, ok := s.refs[id]; !ok && uint64(len(s.refs)) >= s.limits.MaxPages {
		return nil, fmt.Errorf("%w: more than %d referenced pages", ErrLimit, s.limits.MaxPages)
	}
	data, err := s.store.ReadPage(id)
	if err != nil {
		return nil, fmt.Errorf("%w: page %d: %v", ErrPage, id, err)
	}
	if uint64(len(data)) != s.pageSize {
		return nil, fmt.Errorf("%w: page %d has %d bytes, want %d", ErrPage, id, len(data), s.pageSize)
	}
	copied := append([]byte(nil), data...)
	s.pages[id] = copied
	s.refs[id] = struct{}{}
	return copied, nil
}

func (s *snapshot) addReference(addr Address, size uint64) error {
	if size == 0 {
		return nil
	}
	if size-1 > math.MaxUint64-uint64(addr) {
		return fmt.Errorf("%w: reference at %#x size %d", ErrOverflow, uint64(addr), size)
	}
	for off := uint64(0); off < size; {
		id, pageOff, err := s.tr.Translate(addr + Address(off))
		if err != nil {
			return fmt.Errorf("%w: translate %#x: %v", ErrPage, uint64(addr)+off, err)
		}
		if pageOff >= s.pageSize {
			return fmt.Errorf("%w: page %d offset %d exceeds page size %d",
				ErrPage, id, pageOff, s.pageSize)
		}
		if _, ok := s.refs[id]; !ok {
			if uint64(len(s.refs)) >= s.limits.MaxPages {
				return fmt.Errorf("%w: more than %d referenced pages", ErrLimit, s.limits.MaxPages)
			}
			s.refs[id] = struct{}{}
		}
		chunk := s.pageSize - pageOff
		if remaining := size - off; chunk > remaining {
			chunk = remaining
		}
		off += chunk
	}
	return nil
}

func (s *snapshot) readAt(addr Address, size uint64) ([]byte, error) {
	if size == 0 {
		return []byte{}, nil
	}
	if size-1 > math.MaxUint64-uint64(addr) {
		return nil, fmt.Errorf("%w: read at %#x size %d", ErrOverflow, uint64(addr), size)
	}
	if size > uint64(int(^uint(0)>>1)) {
		return nil, fmt.Errorf("%w: read of %d bytes is too large", ErrLimit, size)
	}
	out := make([]byte, int(size))
	for off := uint64(0); off < size; {
		id, pageOff, err := s.tr.Translate(addr + Address(off))
		if err != nil {
			return nil, fmt.Errorf("%w: translate %#x: %v", ErrPage, uint64(addr)+off, err)
		}
		page, err := s.page(id)
		if err != nil {
			return nil, err
		}
		if pageOff >= uint64(len(page)) {
			return nil, fmt.Errorf("%w: page %d offset %d exceeds %d-byte page",
				ErrPage, id, pageOff, len(page))
		}
		chunk := uint64(len(page)) - pageOff
		if remaining := size - off; chunk > remaining {
			chunk = remaining
		}
		copy(out[off:off+chunk], page[pageOff:pageOff+chunk])
		off += chunk
	}
	return out, nil
}

func (s *snapshot) refList() []PageID {
	out := make([]PageID, 0, len(s.refs))
	for id := range s.refs {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
