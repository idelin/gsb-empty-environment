package netsec

import (
	"fmt"
	"sort"
)

type locKey struct {
	channel int
	bank    int
	row     int
}

func (k locKey) below() locKey {
	return locKey{channel: k.channel, bank: k.bank, row: k.row - 1}
}

func (k locKey) above() locKey {
	return locKey{channel: k.channel, bank: k.bank, row: k.row + 1}
}

type scenario struct {
	layout Layout

	pages      []Page
	pageByID   map[PageID]int
	pageByPhys map[PhysicalPage]int
	initial    []PhysicalPage

	location []Location
	mapped   []bool

	free []PhysicalPage

	witnesses []Witness
}

func buildScenario(layout Layout, forRepair bool) (*scenario, error) {
	if layout.Limits.Channels <= 0 || layout.Limits.Banks <= 0 || layout.Limits.Rows <= 0 {
		return nil, fmt.Errorf("%w: limits must all be positive", ErrInvalidLayout)
	}
	if layout.Locate == nil {
		return nil, fmt.Errorf("%w: nil Locate function", ErrInvalidLayout)
	}

	s := &scenario{
		layout:     layout,
		pageByID:   make(map[PageID]int, len(layout.Pages)),
		pageByPhys: make(map[PhysicalPage]int, len(layout.Pages)),
		location:   make([]Location, PhysicalPageCount),
		mapped:     make([]bool, PhysicalPageCount),
	}
	s.pages = append([]Page(nil), layout.Pages...)
	s.initial = make([]PhysicalPage, len(s.pages))

	for i, p := range layout.Pages {
		if p.ID < 0 {
			return nil, fmt.Errorf("%w: negative page ID %d", ErrInvalidLayout, p.ID)
		}
		if p.Physical < 0 || p.Physical >= PhysicalPageCount {
			return nil, fmt.Errorf("%w: page %d physical frame %d outside [0,%d)",
				ErrInvalidLayout, p.ID, p.Physical, PhysicalPageCount)
		}
		if p.Role > RoleSensitive {
			return nil, fmt.Errorf("%w: page %d has unknown role %d", ErrInvalidLayout, p.ID, p.Role)
		}
		if _, exists := s.pageByID[p.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate page ID %d", ErrInvalidLayout, p.ID)
		}
		if other, exists := s.pageByPhys[p.Physical]; exists {
			return nil, fmt.Errorf("%w: pages %d and %d share physical frame %d",
				ErrInvalidLayout, s.pages[other].ID, p.ID, p.Physical)
		}
		s.pageByID[p.ID] = i
		s.pageByPhys[p.Physical] = i
		s.initial[i] = p.Physical
	}

	if len(layout.FreePages) > 0 {
		if forRepair && len(layout.FreePages) > MaxFreePages {
			return nil, fmt.Errorf("%w: got %d, maximum %d",
				ErrTooManyFreePages, len(layout.FreePages), MaxFreePages)
		}
		seen := make(map[PhysicalPage]bool, len(layout.FreePages))
		for _, physical := range layout.FreePages {
			if physical < 0 || physical >= PhysicalPageCount {
				return nil, fmt.Errorf("%w: free frame %d outside [0,%d)",
					ErrInvalidLayout, physical, PhysicalPageCount)
			}
			if seen[physical] {
				return nil, fmt.Errorf("%w: duplicate free frame %d", ErrInvalidLayout, physical)
			}
			if _, occupied := s.pageByPhys[physical]; occupied {
				return nil, fmt.Errorf("%w: free frame %d is occupied", ErrInvalidLayout, physical)
			}
			seen[physical] = true
			s.free = append(s.free, physical)
		}
		sort.Ints(s.free)
	}

	for physical := 0; physical < PhysicalPageCount; physical++ {
		loc, ok := layout.Locate(physical)
		if !ok {
			continue
		}
		if err := validateLocation(loc, layout.Limits); err != nil {
			return nil, fmt.Errorf("%w: physical page %d mapped to (%d,%d,%d): %v",
				ErrMappingOutOfRange, physical, loc.Channel, loc.Bank, loc.Row, err)
		}
		s.location[physical] = loc
		s.mapped[physical] = true
	}

	s.witnesses = enumerateWitnesses(s)
	return s, nil
}

func validateLocation(loc Location, limits Limits) error {
	if loc.Channel < 0 || loc.Channel >= limits.Channels {
		return fmt.Errorf("channel %d outside [0,%d)", loc.Channel, limits.Channels)
	}
	if loc.Bank < 0 || loc.Bank >= limits.Banks {
		return fmt.Errorf("bank %d outside [0,%d)", loc.Bank, limits.Banks)
	}
	if loc.Row < 0 || loc.Row >= limits.Rows {
		return fmt.Errorf("row %d outside [0,%d)", loc.Row, limits.Rows)
	}
	return nil
}

func (s *scenario) pageIndex(id PageID) (int, bool) {
	i, ok := s.pageByID[id]
	return i, ok
}

// enumerateWitnesses performs the complete cross product at every sensitive
// centre.  Collisions in (channel,bank,row), including multiple sensitive pages
// at one centre or multiple attackers on one side, all produce distinct
// three-page witnesses.
func enumerateWitnesses(s *scenario) []Witness {
	sens := make(map[locKey][]PageID)
	att := make(map[locKey][]PageID)

	for _, p := range s.pages {
		if !s.mapped[p.Physical] {
			continue
		}
		loc := s.location[p.Physical]
		k := keyFromLocation(loc)
		switch p.Role {
		case RoleSensitive:
			sens[k] = append(sens[k], p.ID)
		case RoleAttacker:
			att[k] = append(att[k], p.ID)
		}
	}

	for _, ids := range sens {
		sort.Ints(ids)
	}
	for _, ids := range att {
		sort.Ints(ids)
	}

	out := make([]Witness, 0)
	for centre, sensIDs := range sens {
		belowIDs, okBelow := att[centre.below()]
		aboveIDs, okAbove := att[centre.above()]
		if !okBelow || !okAbove {
			continue
		}
		for _, sensitiveID := range sensIDs {
			for _, belowID := range belowIDs {
				for _, aboveID := range aboveIDs {
					if sensitiveID == belowID || sensitiveID == aboveID || belowID == aboveID {
						continue
					}
					out = append(out, Witness{
						Sensitive:             sensitiveID,
						AttackerBelow:         belowID,
						AttackerAbove:         aboveID,
						Channel:               centre.channel,
						Bank:                  centre.bank,
						Row:                   centre.row,
						SensitivePhysical:     s.pages[s.pageByID[sensitiveID]].Physical,
						AttackerBelowPhysical: s.pages[s.pageByID[belowID]].Physical,
						AttackerAbovePhysical: s.pages[s.pageByID[aboveID]].Physical,
					})
				}
			}
		}
	}
	sortWitnesses(out)
	return out
}

func sortWitnesses(w []Witness) {
	sort.Slice(w, func(i, j int) bool {
		a, b := w[i], w[j]
		if a.Sensitive != b.Sensitive {
			return a.Sensitive < b.Sensitive
		}
		if a.AttackerBelow != b.AttackerBelow {
			return a.AttackerBelow < b.AttackerBelow
		}
		if a.AttackerAbove != b.AttackerAbove {
			return a.AttackerAbove < b.AttackerAbove
		}
		if a.Channel != b.Channel {
			return a.Channel < b.Channel
		}
		if a.Bank != b.Bank {
			return a.Bank < b.Bank
		}
		return a.Row < b.Row
	})
}

func keyFromLocation(loc Location) locKey {
	return locKey{channel: loc.Channel, bank: loc.Bank, row: loc.Row}
}

// Audit returns every minimal three-page witness in the initial layout.
func Audit(layout Layout) (Report, error) {
	s, err := buildScenario(layout, false)
	if err != nil {
		return Report{}, err
	}
	mappedPages := 0
	for _, p := range s.pages {
		if s.mapped[p.Physical] {
			mappedPages++
		}
	}
	return Report{
		Witnesses:     append([]Witness(nil), s.witnesses...),
		WitnessCount:  int64(len(s.witnesses)),
		MappedPages:   mappedPages,
		UnmappedPages: len(s.pages) - mappedPages,
	}, nil
}

// FindWitnesses is the witness-only form of Audit.
func FindWitnesses(layout Layout) ([]Witness, error) {
	report, err := Audit(layout)
	if err != nil {
		return nil, err
	}
	return report.Witnesses, nil
}
