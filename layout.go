package netsec

import (
	"errors"
	"fmt"
)

// Region describes a contiguous memory region measured in pages.
type Region struct {
	// Size is the region length in pages; it must be at least one.
	Size int
	// Alignment is the required start-page alignment and must be a
	// positive power of two (1 means any page).
	Alignment int
}

// Spec defines a layout problem: placing labeled, non-overlapping regions
// inside a small page-address domain while avoiding reserved pages.
type Spec struct {
	// Pages is the domain length in pages; it must be between 1 and 32.
	Pages int
	// Reserved lists page indices that no region may occupy. Duplicate
	// entries are harmless.
	Reserved []int
	// Regions lists the regions to place, in label order. It must contain
	// between one and six entries.
	Regions []Region
}

// Layout is one legal placement: Layout[i] is the start page of Spec.Regions[i].
type Layout []int

var (
	// ErrNoLayout indicates that the spec admits no legal placement.
	ErrNoLayout = errors.New("netsec: no legal layout exists")
	// ErrInvalidLayout indicates that a layout passed to Rank is not legal.
	ErrInvalidLayout = errors.New("netsec: layout is not legal")
	// ErrInvalidRank indicates that a rank passed to Unrank is out of range.
	ErrInvalidRank = errors.New("netsec: rank out of range")
)

const (
	maxPages   = 32
	maxRegions = 6
)

type candidate struct {
	start int
	mask  uint64
}

type solverKey struct {
	i    int
	mask uint64
}

type solver struct {
	spec       *Spec
	reserved   uint64
	candidates [][]candidate
	memo       map[solverKey]int64
}

// Validate checks the domain, reserved-page and region constraints.
func (s *Spec) Validate() error {
	if s == nil {
		return errors.New("netsec: nil spec")
	}
	if s.Pages < 1 || s.Pages > maxPages {
		return fmt.Errorf("netsec: Pages must be in [1,%d], got %d", maxPages, s.Pages)
	}
	if len(s.Regions) < 1 || len(s.Regions) > maxRegions {
		return fmt.Errorf("netsec: number of regions must be in [1,%d], got %d", maxRegions, len(s.Regions))
	}
	for idx, region := range s.Regions {
		if region.Size < 1 {
			return fmt.Errorf("netsec: region %d has non-positive size %d", idx, region.Size)
		}
		if region.Size > s.Pages {
			return fmt.Errorf("netsec: region %d size %d exceeds domain of %d pages", idx, region.Size, s.Pages)
		}
		if region.Alignment < 1 || region.Alignment&(region.Alignment-1) != 0 {
			return fmt.Errorf("netsec: region %d alignment %d is not a positive power of two", idx, region.Alignment)
		}
	}
	for _, page := range s.Reserved {
		if page < 0 || page >= s.Pages {
			return fmt.Errorf("netsec: reserved page %d outside domain [0,%d)", page, s.Pages)
		}
	}
	return nil
}

func newSolver(s *Spec) *solver {
	sv := &solver{
		spec:       s,
		candidates: make([][]candidate, len(s.Regions)),
		memo:       make(map[solverKey]int64),
	}
	for _, page := range s.Reserved {
		sv.reserved |= uint64(1) << uint(page)
	}
	for idx, region := range s.Regions {
		last := s.Pages - region.Size
		for start := 0; start <= last; start += region.Alignment {
			mask := (uint64(1)<<uint(region.Size) - 1) << uint(start)
			if mask&sv.reserved != 0 {
				continue
			}
			sv.candidates[idx] = append(sv.candidates[idx], candidate{start: start, mask: mask})
		}
	}
	return sv
}

// count returns the number of legal ways to place regions i..n-1 given the
// pages already occupied by earlier regions. The result is memoized.
func (sv *solver) count(i int, occupied uint64) int64 {
	if i == len(sv.spec.Regions) {
		return 1
	}
	key := solverKey{i: i, mask: occupied}
	if total, ok := sv.memo[key]; ok {
		return total
	}
	var total int64
	for _, choice := range sv.candidates[i] {
		if choice.mask&occupied == 0 {
			total += sv.count(i+1, occupied|choice.mask)
		}
	}
	sv.memo[key] = total
	return total
}

// Count returns the exact number of distinct legal layouts. Regions are
// labeled, so permuting region starts generally yields distinct layouts.
func (s *Spec) Count() (int64, error) {
	if err := s.Validate(); err != nil {
		return 0, err
	}
	return newSolver(s).count(0, 0), nil
}

func (s *Spec) Rank(layout Layout) (int64, error) {
	if err := s.Validate(); err != nil {
		return 0, err
	}
	if len(layout) != len(s.Regions) {
		return 0, ErrInvalidLayout
	}
	sv := newSolver(s)
	var rank int64
	var occupied uint64
	for i, want := range layout {
		placed := false
		for _, choice := range sv.candidates[i] {
			if choice.mask&occupied != 0 {
				continue
			}
			if choice.start < want {
				rank += sv.count(i+1, occupied|choice.mask)
				continue
			}
			if choice.start == want {
				occupied |= choice.mask
				placed = true
			}
			break
		}
		if !placed {
			return 0, ErrInvalidLayout
		}
	}
	return rank, nil
}

// Unrank returns the legal layout with the given zero-based lexicographic
// rank. It is the exact inverse of Rank.
func (s *Spec) Unrank(rank int64) (Layout, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	if rank < 0 {
		return nil, ErrInvalidRank
	}
	sv := newSolver(s)
	total := sv.count(0, 0)
	if total == 0 {
		return nil, ErrNoLayout
	}
	if rank >= total {
		return nil, ErrInvalidRank
	}
	layout := make(Layout, len(s.Regions))
	occupied := uint64(0)
	remaining := rank
	for i := range s.Regions {
		placed := false
		for _, choice := range sv.candidates[i] {
			if choice.mask&occupied != 0 {
				continue
			}
			weight := sv.count(i+1, occupied|choice.mask)
			if remaining < weight {
				layout[i] = choice.start
				occupied |= choice.mask
				placed = true
				break
			}
			remaining -= weight
		}
		if !placed {
			return nil, ErrInvalidRank
		}
	}
	return layout, nil
}
