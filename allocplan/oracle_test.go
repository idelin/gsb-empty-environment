package allocplan

// oracleSegment is an explicit allocation boundary used by the reference
// model. Planner splits create boundaries even though address ownership does
// not change, so the oracle free-run view must track them too.
type oracleSegment struct {
	start int
	end   int
	used  bool
	tag   Tag
}

// oracleModel is an independent, deliberately simple reference that tracks
// quarantine per (address unit, tag), live tag counts, per-unit ownership and
// explicit segment boundaries. The planner is only allowed to be as strict as
// this model; it must never hand out a tag that the oracle considers blocked.
type oracleModel struct {
	space    int
	tagCount int
	quar     int64
	until    [][]int64 // until[address][tag], -1 means never quarantined
	owner    []Tag     // owner[address] is the live tag or NoTag
	bounds   []int     // sorted segment boundary offsets, always incl. 0,space
	live     []int
}

func newOracle(cfg Config) *oracleModel {
	until := make([][]int64, cfg.AddressSpace)
	for addr := range until {
		row := make([]int64, cfg.TagDomainSize+1)
		for tag := range row {
			row[tag] = -1
		}
		until[addr] = row
	}
	return &oracleModel{
		space:    cfg.AddressSpace,
		tagCount: cfg.TagDomainSize,
		quar:     cfg.Quarantine,
		until:    until,
		owner:    make([]Tag, cfg.AddressSpace),
		bounds:   []int{0, cfg.AddressSpace},
		live:     make([]int, cfg.TagDomainSize+1),
	}
}

func (m *oracleModel) clone() *oracleModel {
	live := append([]int(nil), m.live...)
	owner := append([]Tag(nil), m.owner...)
	bounds := append([]int(nil), m.bounds...)
	until := make([][]int64, len(m.until))
	for addr := range m.until {
		until[addr] = append([]int64(nil), m.until[addr]...)
	}
	return &oracleModel{
		space:    m.space,
		tagCount: m.tagCount,
		quar:     m.quar,
		until:    until,
		owner:    owner,
		bounds:   bounds,
		live:     live,
	}
}

// segments derives the current segment table from boundaries and ownership,
// guaranteeing it always agrees with the planner view.
func (m *oracleModel) segments() []oracleSegment {
	out := make([]oracleSegment, 0, len(m.bounds)-1)
	for i := 0; i+1 < len(m.bounds); i++ {
		start, end := m.bounds[i], m.bounds[i+1]
		tag := m.owner[start]
		out = append(out, oracleSegment{
			start: start,
			end:   end,
			used:  tag != NoTag,
			tag:   tag,
		})
	}
	return out
}

// unitUsed reports oracle allocation ownership for one address unit.
func (m *oracleModel) unitUsed(addr int) bool { return m.owner[addr] != NoTag }

// firstFit returns the first free segment with at least size units, mirroring
// the planner's first-fit placement.
func (m *oracleModel) firstFit(size int) (start, end int) {
	for _, seg := range m.segments() {
		if !seg.used && seg.end-seg.start >= size {
			return seg.start, seg.end
		}
	}
	return -1, -1
}

// decision mirrors the planner's deterministic first-fit/lowest-tag
// choice. The third return is the earliest retry tick when rejection is due
// purely to open quarantine.
func (m *oracleModel) decision(size int, now int64) (start int, tag Tag, waitTick int64, ok bool) {
	segStart, _ := m.firstFit(size)
	if segStart < 0 {
		return 0, NoTag, 0, false
	}
	earliest := int64(0)
	anyQuarantined := false
	for candidate := Tag(1); int(candidate) <= m.tagCount; candidate++ {
		if m.live[candidate] > 0 {
			continue
		}
		blocking := false
		var expiry int64
		for addr := segStart; addr < segStart+size; addr++ {
			if m.until[addr][candidate] > now {
				blocking = true
				if expiry == 0 || m.until[addr][candidate] < expiry {
					expiry = m.until[addr][candidate]
				}
			}
		}
		if !blocking {
			return segStart, candidate, 0, true
		}
		if !anyQuarantined || expiry < earliest {
			earliest = expiry
			anyQuarantined = true
		}
	}
	return segStart, NoTag, earliest, false
}

// insertBoundary keeps the sorted boundary list free of duplicates.
func (m *oracleModel) insertBoundary(offset int) {
	for i, existing := range m.bounds {
		if existing == offset {
			return
		}
		if existing > offset {
			m.bounds = append(m.bounds, 0)
			copy(m.bounds[i+1:], m.bounds[i:len(m.bounds)-1])
			m.bounds[i] = offset
			return
		}
	}
	m.bounds = append(m.bounds, offset)
}

func (m *oracleModel) alloc(start, size int, tag Tag) {
	end := start + size
	for addr := start; addr < end; addr++ {
		if m.owner[addr] != NoTag {
			panic("oracle: alloc over live unit")
		}
		m.owner[addr] = tag
	}
	if end < m.space {
		m.insertBoundary(end)
	}
	m.live[tag]++
}

func (m *oracleModel) free(start int, tag Tag, now int64) {
	end := -1
	for i := 0; i+1 < len(m.bounds); i++ {
		if m.bounds[i] == start {
			end = m.bounds[i+1]
			break
		}
	}
	if end < 0 || m.owner[start] != tag {
		panic("oracle: free on missing or mismatched segment")
	}
	expiry := saturatingAdd(now, m.quar)
	for addr := start; addr < end; addr++ {
		m.until[addr][tag] = expiry
		m.owner[addr] = NoTag
	}
	m.live[tag]--
	m.compactFreeBoundaries()
}

// compactFreeBoundaries drops boundaries that no longer separate different
// ownership states, merging adjacent free runs exactly like the planner.
// compactFreeBoundaries removes only boundaries between adjacent free units,
// matching the planner which coalesces free segments but keeps distinct
// allocated segments even when they carry the same tag.
func (m *oracleModel) compactFreeBoundaries() {
	kept := []int{0}
	for i := 1; i+1 < len(m.bounds); i++ {
		offset := m.bounds[i]
		if m.owner[offset] != NoTag || m.owner[offset-1] != NoTag {
			kept = append(kept, offset)
		}
	}
	kept = append(kept, m.space)
	m.bounds = kept
}

func (m *oracleModel) split(start, left int) {
	tag := m.owner[start]
	if tag == NoTag {
		panic("oracle: split on free unit")
	}
	m.insertBoundary(start + left)
	m.live[tag]++
}
