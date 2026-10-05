package hexgrid

import (
	"errors"
	"fmt"
)

var (
	// ErrMissingPage is returned by a partition while a page is still
	// being loaded asynchronously. Queries retry it.
	ErrMissingPage = errors.New("hexgrid: page not loaded yet")
	// ErrStaleVersion is returned by a partition that has not synced the
	// requested version yet. Queries retry it.
	ErrStaleVersion = errors.New("hexgrid: partition has not synced the requested version")
	// ErrVersionGone is returned when the requested version was garbage
	// collected. Queries reject immediately.
	ErrVersionGone = errors.New("hexgrid: requested version was garbage collected")
	// ErrUnknownVersion is returned for versions that never existed.
	ErrUnknownVersion = errors.New("hexgrid: unknown version")
)

// PageID identifies a fixed-size square of cells in axial space.
type PageID struct{ X, Y int }

// PartitionID identifies a storage partition: a fixed-size square of pages.
// Partitions are storage boundaries only; they never affect adjacency.
type PartitionID struct{ X, Y int }

// Layout describes how the plane is split into pages and partitions.
type Layout struct {
	PageSize      int // cells per page side
	PartitionSize int // pages per partition side
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// Locate maps a cell to its partition and page. Works for negative
// coordinates via floored division.
func (l Layout) Locate(h Hex) (PartitionID, PageID) {
	if l.PageSize <= 0 || l.PartitionSize <= 0 {
		panic("hexgrid: invalid layout")
	}
	px := floorDiv(h.Q, l.PageSize)
	py := floorDiv(h.R, l.PageSize)
	return PartitionID{floorDiv(px, l.PartitionSize), floorDiv(py, l.PartitionSize)}, PageID{px, py}
}

// pageSnapshot is the content of a page at one version: cells maps every
// cell of the finite map stored in this page to its walkability. Cells
// absent from the map are outside the finite map and not walkable.
type pageSnapshot struct {
	version uint64
	cells   map[Hex]bool
}

// page keeps its full snapshot history so old-version queries work.
type page struct {
	snapshots []pageSnapshot // ascending by version
}

// at returns the page content as of version, i.e. the newest snapshot with
// snapshot.version <= version. nil means the page did not exist yet.
func (p *page) at(version uint64) map[Hex]bool {
	var cells map[Hex]bool
	for _, s := range p.snapshots {
		if s.version > version {
			break
		}
		cells = s.cells
	}
	return cells
}

func (p *page) latest() map[Hex]bool {
	if len(p.snapshots) == 0 {
		return nil
	}
	return p.snapshots[len(p.snapshots)-1].cells
}

// pendingPage simulates a page that is still being fetched asynchronously:
// the next misses fetches fail with ErrMissingPage before it arrives.
type pendingPage struct {
	page   *page
	misses int
}

// Partition is one asynchronous storage unit. Version is the store version
// this partition has synced to.
type Partition struct {
	ID       PartitionID
	Version  uint64
	pages    map[PageID]*page
	incoming map[PageID]*pendingPage
}

// Store is the versioned, partitioned cell store.
type Store struct {
	layout     Layout
	version    uint64
	horizon    uint64 // versions below this were garbage collected
	partitions map[PartitionID]*Partition
	// MaxRetries bounds how often a query retries a missing page or a
	// stale partition before rejecting the query.
	MaxRetries int
}

// NewStore builds a store at version 1 from the finite map: walkable cells
// plus obstacle cells. Everything else is outside the map.
func NewStore(layout Layout, walkable, obstacles []Hex) *Store {
	s := &Store{
		layout:     layout,
		version:    1,
		partitions: map[PartitionID]*Partition{},
		MaxRetries: 3,
	}
	cells := map[Hex]bool{}
	for _, h := range walkable {
		cells[h] = true
	}
	for _, h := range obstacles {
		cells[h] = false
	}
	s.addCells(cells, 1)
	return s
}

func (s *Store) getPartition(pid PartitionID) *Partition {
	part := s.partitions[pid]
	if part == nil {
		part = &Partition{
			ID:       pid,
			Version:  s.version,
			pages:    map[PageID]*page{},
			incoming: map[PageID]*pendingPage{},
		}
		s.partitions[pid] = part
	}
	return part
}

func (s *Store) addCells(cells map[Hex]bool, version uint64) {
	type key struct {
		pid PartitionID
		gid PageID
	}
	groups := map[key]map[Hex]bool{}
	for h, w := range cells {
		pid, gid := s.layout.Locate(h)
		k := key{pid, gid}
		if groups[k] == nil {
			groups[k] = map[Hex]bool{}
		}
		groups[k][h] = w
	}
	for k, cs := range groups {
		part := s.getPartition(k.pid)
		part.Version = version
		part.pages[k.gid] = &page{snapshots: []pageSnapshot{{version: version, cells: cs}}}
	}
}

// Version returns the current store version.
func (s *Store) Version() uint64 { return s.version }

// Layout returns the store layout.
func (s *Store) Layout() Layout { return s.layout }

// Update applies a batch of walkability changes (true = walkable,
// false = obstacle) atomically: it bumps the store version, appends a new
// snapshot to every affected page, and marks every partition synced.
// It returns the new version.
func (s *Store) Update(changes map[Hex]bool) uint64 {
	s.version++
	type key struct {
		pid PartitionID
		gid PageID
	}
	grouped := map[key]map[Hex]bool{}
	for h, w := range changes {
		pid, gid := s.layout.Locate(h)
		k := key{pid, gid}
		if grouped[k] == nil {
			grouped[k] = map[Hex]bool{}
		}
		grouped[k][h] = w
	}
	for k, cs := range grouped {
		part := s.getPartition(k.pid)
		pg := part.pages[k.gid]
		merged := map[Hex]bool{}
		if pg != nil {
			for h, w := range pg.latest() {
				merged[h] = w
			}
		}
		for h, w := range cs {
			merged[h] = w
		}
		if pg == nil {
			pg = &page{}
			part.pages[k.gid] = pg
		}
		pg.snapshots = append(pg.snapshots, pageSnapshot{version: s.version, cells: merged})
	}
	for _, part := range s.partitions {
		part.Version = s.version
	}
	return s.version
}

// DropHistoryBefore garbage-collects snapshots strictly older than the
// given version. Queries for collected versions are rejected with
// ErrVersionGone instead of silently reading newer data.
func (s *Store) DropHistoryBefore(version uint64) {
	if version > s.version {
		version = s.version
	}
	if version <= s.horizon {
		return
	}
	s.horizon = version
	for _, part := range s.partitions {
		for _, pg := range part.pages {
			keep := 0
			for keep < len(pg.snapshots) && pg.snapshots[keep].version < version {
				keep++
			}
			if keep > 0 {
				keep-- // retain the newest snapshot below the horizon as base
				pg.snapshots = append([]pageSnapshot{}, pg.snapshots[keep:]...)
			}
		}
	}
}

// DeferPageLoad simulates asynchronous page loading: the page is removed
// from its partition and the next misses fetches fail with ErrMissingPage
// before the page arrives. Reports whether the page existed.
func (s *Store) DeferPageLoad(gid PageID, misses int) bool {
	for _, part := range s.partitions {
		if pg := part.pages[gid]; pg != nil {
			delete(part.pages, gid)
			part.incoming[gid] = &pendingPage{page: pg, misses: misses}
			return true
		}
	}
	return false
}

// DesyncPartition forces a partition's synced version, simulating a
// partition that lags behind (or catches up with) a config change.
func (s *Store) DesyncPartition(pid PartitionID, version uint64) bool {
	part := s.partitions[pid]
	if part == nil {
		return false
	}
	part.Version = version
	return true
}

// fetchPage reads one page at exactly the requested version. It never
// mixes versions: stale partitions and missing pages are errors, and it
// is the caller's (the query's) job to retry or reject.
func (s *Store) fetchPage(version uint64, pid PartitionID, gid PageID) (map[Hex]bool, error) {
	if version == 0 || version > s.version {
		return nil, fmt.Errorf("%w: %d", ErrUnknownVersion, version)
	}
	if version < s.horizon {
		return nil, fmt.Errorf("%w: %d", ErrVersionGone, version)
	}
	part := s.partitions[pid]
	if part == nil {
		return nil, nil // no content stored in this partition
	}
	if part.Version < version {
		return nil, fmt.Errorf("%w: partition %v at %d, want %d", ErrStaleVersion, pid, part.Version, version)
	}
	if pg := part.pages[gid]; pg != nil {
		return pg.at(version), nil
	}
	if pend := part.incoming[gid]; pend != nil {
		if pend.misses > 0 {
			pend.misses--
			return nil, fmt.Errorf("%w: %v", ErrMissingPage, gid)
		}
		delete(part.incoming, gid)
		part.pages[gid] = pend.page
		return pend.page.at(version), nil
	}
	return nil, nil // page has no content: empty region of the map
}
