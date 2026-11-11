package pagetable

import "encoding/binary"

// Mapping is the result of a successful page-table walk.
type Mapping struct {
	VA      uint64
	Phys    uint64
	Size    uint64 // 4 KiB, 2 MiB or 1 GiB
	Perm    Perm
	RawLeaf uint64 // raw leaf entry, for byte-level preservation checks
}

// Snapshot is an immutable view rooted at the PML4 physical page root.
// Snapshots are safe for concurrent use, and remain valid forever even after
// the service has published newer roots.
type Snapshot struct {
	store PageStore
	root  uint64
}

// Root returns the PML4 physical page address of this snapshot.
func (s *Snapshot) Root() uint64 { return s.root }

func readEntry(store PageStore, tableAddr uint64, index uint) (uint64, error) {
	var buf [8]byte
	if _, err := store.ReadAt(buf[:], int64(tableAddr+uint64(index)*8)); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(buf[:]), nil
}

func writeEntry(store PageStore, tableAddr uint64, index uint, entry uint64) error {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], entry)
	_, err := store.WriteAt(buf[:], int64(tableAddr+uint64(index)*8))
	return err
}

// Lookup walks the four levels for va and returns the mapping covering it.
func (s *Snapshot) Lookup(va uint64) (Mapping, error) {
	if !Canonical(va) {
		return Mapping{}, ErrNotCanonical
	}
	table := s.root
	effective := Perm{Write: true, User: true, Execute: true}
	for level := 0; level < pageTableLevels; level++ {
		entry, err := readEntry(s.store, table, entryIndex(va, level))
		if err != nil {
			return Mapping{}, err
		}
		if !entryPresent(entry) {
			return Mapping{}, ErrNotMapped
		}
		effective = combinePerm(effective, entry)
		if level < LevelPT && entry&flagPS != 0 {
			size := entrySpan(level)
			mask := largeFrameMask
			if level == LevelPDPT {
				mask = gibFrameMask
			}
			return Mapping{
				VA:      va &^ (size - 1),
				Phys:    entry & mask,
				Size:    size,
				Perm:    effective,
				RawLeaf: entry,
			}, nil
		}
		if level == LevelPT {
			return Mapping{
				VA:      va &^ uint64(PageSize-1),
				Phys:    entry & leafFrameMask,
				Size:    PageSize,
				Perm:    effective,
				RawLeaf: entry,
			}, nil
		}
		table = entry & tableFrameMask
	}
	return Mapping{}, ErrNotMapped
}

// LeafEntry performs a walk like Lookup but also returns the raw 4 KiB leaf
// entry, useful for verifying that untouched pages keep identical entry bits.
func (s *Snapshot) LeafEntry(va uint64) (raw uint64, tableAddr uint64, index uint, m Mapping, err error) {
	if !Canonical(va) {
		return 0, 0, 0, Mapping{}, ErrNotCanonical
	}
	table := s.root
	effective := Perm{Write: true, User: true, Execute: true}
	for level := 0; level < pageTableLevels; level++ {
		idx := entryIndex(va, level)
		entry, rerr := readEntry(s.store, table, idx)
		if rerr != nil {
			return 0, 0, 0, Mapping{}, rerr
		}
		if !entryPresent(entry) {
			return 0, 0, 0, Mapping{}, ErrNotMapped
		}
		effective = combinePerm(effective, entry)
		if entry&flagPS != 0 && level < LevelPT {
			size := entrySpan(level)
			mask := largeFrameMask
			if level == LevelPDPT {
				mask = gibFrameMask
			}
			m = Mapping{VA: va &^ (size - 1), Phys: entry & mask, Size: size, Perm: effective, RawLeaf: entry}
			return entry, table, idx, m, nil
		}
		if level == LevelPT {
			m = Mapping{VA: va &^ uint64(PageSize-1), Phys: entry & leafFrameMask, Size: PageSize, Perm: effective, RawLeaf: entry}
			return entry, table, idx, m, nil
		}
		table = entry & tableFrameMask
	}
	return 0, 0, 0, Mapping{}, ErrNotMapped
}

// TreeInfo describes the set of pages reachable from a root.
type TreeInfo struct {
	// TablePages is the set of physical pages used as page-table pages.
	TablePages map[uint64]struct{}
	// LeafPages is the set of physical frames referenced by present leaf entries.
	LeafPages map[uint64]struct{}
	// TableRefs counts present references between table pages:
	// child table address -> number of entries pointing at it.
	TableRefs map[uint64]int
	// LeafRefs counts present references to leaf data frames.
	LeafRefs map[uint64]int
}

func newTreeInfo() *TreeInfo {
	return &TreeInfo{
		TablePages: map[uint64]struct{}{},
		LeafPages:  map[uint64]struct{}{},
		TableRefs:  map[uint64]int{},
		LeafRefs:   map[uint64]int{},
	}
}

// Analyze walks every reachable entry from the snapshot root. Cycles and
// aliased subtrees are visited once; reference counts still count every edge.
func (s *Snapshot) Analyze() (*TreeInfo, error) {
	info := newTreeInfo()
	visited := map[uint64]bool{}
	return info, s.analyzeTable(s.root, 0, info, visited)
}

func (s *Snapshot) analyzeTable(table uint64, level int, info *TreeInfo, visited map[uint64]bool) error {
	info.TablePages[table] = struct{}{}
	if visited[table] {
		return nil
	}
	visited[table] = true
	for i := uint(0); i < entriesPerTab; i++ {
		entry, err := readEntry(s.store, table, i)
		if err != nil {
			return err
		}
		if !entryPresent(entry) {
			continue
		}
		if level < LevelPT && entry&flagPS != 0 {
			frame := entry & largeFrameMask
			if level == LevelPDPT {
				frame = entry & gibFrameMask
			}
			info.LeafPages[frame] = struct{}{}
			info.LeafRefs[frame]++
			continue
		}
		if level == LevelPT {
			frame := entry & leafFrameMask
			info.LeafPages[frame] = struct{}{}
			info.LeafRefs[frame]++
			continue
		}
		child := entry & tableFrameMask
		info.TableRefs[child]++
		if err := s.analyzeTable(child, level+1, info, visited); err != nil {
			return err
		}
	}
	return nil
}

// Mappings enumerates every present mapping reachable from the root.
func (s *Snapshot) Mappings() ([]Mapping, error) {
	var out []Mapping
	err := s.walkMappings(s.root, 0, 0, Perm{Write: true, User: true, Execute: true}, &out)
	return out, err
}

func (s *Snapshot) walkMappings(table uint64, level int, va uint64, effective Perm, out *[]Mapping) error {
	span := entrySpan(level)
	for i := uint(0); i < entriesPerTab; i++ {
		entry, err := readEntry(s.store, table, i)
		if err != nil {
			return err
		}
		if !entryPresent(entry) {
			continue
		}
		entryVA := va + uint64(i)*span
		perm := combinePerm(effective, entry)
		if level < LevelPT && entry&flagPS != 0 {
			mask := largeFrameMask
			if level == LevelPDPT {
				mask = gibFrameMask
			}
			*out = append(*out, Mapping{VA: entryVA, Phys: entry & mask, Size: span, Perm: perm, RawLeaf: entry})
			continue
		}
		if level == LevelPT {
			*out = append(*out, Mapping{VA: entryVA, Phys: entry & leafFrameMask, Size: PageSize, Perm: perm, RawLeaf: entry})
			continue
		}
		if err := s.walkMappings(entry&tableFrameMask, level+1, entryVA, perm, out); err != nil {
			return err
		}
	}
	return nil
}
