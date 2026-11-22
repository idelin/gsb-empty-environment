package layout

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"sync"
)

// Planner is an immutable, validated layout problem. Its Count method is
// lazy; all other operations may be called concurrently as long as the
// caller does not mutate the Config passed to New.
type Planner struct {
	cfg  Config
	hash [32]byte

	pages int
	n     int
	full  uint8

	// domains[i] is the sorted list of starts that satisfy all unary
	// constraints (bounds, alignment, reservations, leases, unary modular
	// constraints, and pinned/stable placement).
	domains  [][]int
	hasStart [MaxRegions][MaxPages]bool
	span     [MaxRegions][MaxPages]uint64

	fixedMask   uint8
	fixedStarts [MaxRegions]int

	// modAdj[i] is the set of regions that participate in a modular
	// constraint with i. Non-overlap is handled by the page DP and is not
	// included here.
	modAdj  [MaxRegions]uint8
	modOK   [MaxRegions][MaxRegions][MaxPages][MaxPages]bool
	unaryOK [MaxRegions][MaxPages]bool

	countOnce sync.Once
	total     *big.Int
}

// New validates cfg and prepares a planner. It returns an error for
// malformed configurations and for explicitly pinned/stable placements that
// are impossible. A valid configuration with no legal layout is allowed;
// Count then returns zero.
func New(cfg Config) (*Planner, error) {
	norm, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}

	p := &Planner{
		cfg:   norm,
		pages: norm.Pages,
		n:     len(norm.Regions),
		full:  uint8((1 << uint(len(norm.Regions))) - 1),
	}
	for i := 0; i < MaxRegions; i++ {
		for s := 0; s < MaxPages; s++ {
			p.unaryOK[i][s] = true
		}
	}
	for i := 0; i < MaxRegions; i++ {
		for j := 0; j < MaxRegions; j++ {
			for a := 0; a < MaxPages; a++ {
				for b := 0; b < MaxPages; b++ {
					p.modOK[i][j][a][b] = true
				}
			}
		}
	}

	p.applyConstraints()
	if err := p.buildDomains(); err != nil {
		return nil, err
	}
	p.pruneVacuousModularEdges()

	hash, err := fingerprintConfig(norm)
	if err != nil {
		return nil, err
	}
	p.hash = hash
	return p, nil
}

// pruneVacuousModularEdges removes constraint-graph edges that cannot reject
// any pair of starts in the two domains. Keeping them would only enlarge the
// sampler's frontier state without changing the answer.
func (p *Planner) pruneVacuousModularEdges() {
	for i := 0; i < p.n; i++ {
		for j := i + 1; j < p.n; j++ {
			if p.modAdj[i]&(1<<uint(j)) == 0 {
				continue
			}
			useful := false
			for _, a := range p.domains[i] {
				for _, b := range p.domains[j] {
					if !disjointIntervals(a, p.cfg.Regions[i].Length, b, p.cfg.Regions[j].Length) {
						continue
					}
					if !p.modOK[i][j][a][b] || !p.modOK[j][i][b][a] {
						useful = true
						break
					}
				}
				if useful {
					break
				}
			}
			if !useful {
				p.modAdj[i] &^= 1 << uint(j)
				p.modAdj[j] &^= 1 << uint(i)
			}
		}
	}
}

func disjointIntervals(a, lengthA, b, lengthB int) bool {
	return a+lengthA <= b || b+lengthB <= a
}

// Config returns a deep copy of the normalized configuration.
func (p *Planner) Config() Config {
	return cloneConfig(p.cfg)
}

// Fingerprint returns the canonical configuration hash used by checkpoints.
func (p *Planner) Fingerprint() [32]byte {
	return p.hash
}

// Pages returns the page-domain size.
func (p *Planner) Pages() int { return p.pages }

// RegionCount returns the number of regions.
func (p *Planner) RegionCount() int { return p.n }

func (p *Planner) applyConstraints() {
	for _, c := range p.cfg.Constraints {
		if c.A == c.B {
			if !containsUint64(c.Allowed, 0) {
				for s := 0; s < MaxPages; s++ {
					p.unaryOK[c.A][s] = false
				}
			}
			continue
		}
		p.modAdj[c.A] |= 1 << uint(c.B)
		p.modAdj[c.B] |= 1 << uint(c.A)
		for a := 0; a < MaxPages; a++ {
			for b := 0; b < MaxPages; b++ {
				if !p.modOK[c.A][c.B][a][b] {
					continue
				}
				if !constraintHolds(c, a, b) {
					p.modOK[c.A][c.B][a][b] = false
				}
			}
		}
	}
}

func (p *Planner) buildDomains() error {
	var reserved uint64
	for _, page := range p.cfg.Reserved {
		reserved |= 1 << uint(page)
	}

	var leaseBits uint64
	for _, lease := range p.cfg.Leases {
		leaseBits |= leaseMask(lease)
	}
	for _, page := range p.cfg.OldReaderPages {
		leaseBits |= uint64(1) << uint(page)
	}

	stable := make(map[mappingKey]StableMapping, len(p.cfg.Stable))
	for _, m := range p.cfg.Stable {
		stable[mappingKey{name: m.Name, generation: m.Generation}] = m
	}

	p.domains = make([][]int, p.n)
	for i, r := range p.cfg.Regions {
		var allowedLease uint64
		fixed := false
		fixedStart := 0

		switch {
		case r.Stable:
			m, ok := stable[mappingKey{name: r.Name, generation: r.Generation}]
			if !ok {
				return fmt.Errorf("layout: stable region %q generation %d has no prior mapping", r.Name, r.Generation)
			}
			if m.Length != r.Length {
				return fmt.Errorf("layout: stable region %q length %d does not match prior mapping length %d", r.Name, r.Length, m.Length)
			}
			fixed = true
			fixedStart = m.Start
		case r.Pinned:
			fixed = true
			fixedStart = r.Start
		}

		if fixed && (r.Stable || r.Pinned) {
			regionMask := spanMask(fixedStart, r.Length)
			for _, lease := range p.cfg.Leases {
				if lease.Name == r.Name && lease.Generation == r.Generation &&
					leaseMask(lease)&regionMask == regionMask {
					allowedLease |= regionMask
				}
			}
		}

		blocked := reserved | (leaseBits &^ allowedLease)
		if fixed {
			if err := p.checkFixedStart(i, r, fixedStart, blocked); err != nil {
				return err
			}
			p.domains[i] = []int{fixedStart}
			p.hasStart[i][fixedStart] = true
			p.span[i][fixedStart] = spanMask(fixedStart, r.Length)
			p.fixedMask |= 1 << uint(i)
			p.fixedStarts[i] = fixedStart
			continue
		}

		for s := 0; s+r.Length <= p.pages; s++ {
			if s%r.Alignment != 0 {
				continue
			}
			if !p.unaryOK[i][s] {
				continue
			}
			mask := spanMask(s, r.Length)
			if mask&blocked != 0 {
				continue
			}
			p.domains[i] = append(p.domains[i], s)
			p.hasStart[i][s] = true
			p.span[i][s] = mask
		}
	}
	return nil
}

func (p *Planner) checkFixedStart(i int, r Region, start int, blocked uint64) error {
	if start < 0 || start+r.Length > p.pages {
		return fmt.Errorf("layout: fixed region %q start %d does not fit in %d pages", r.Name, start, p.pages)
	}
	if start%r.Alignment != 0 {
		return fmt.Errorf("layout: fixed region %q start %d is not %d-aligned", r.Name, start, r.Alignment)
	}
	if !p.unaryOK[i][start] {
		return fmt.Errorf("layout: fixed region %q violates a unary constraint", r.Name)
	}
	if spanMask(start, r.Length)&blocked != 0 {
		return fmt.Errorf("layout: fixed region %q at start %d is blocked by a reserved page or old-reader lease", r.Name, start)
	}
	return nil
}

func (p *Planner) countPrefix(prefix []int) *big.Int {
	assignedMask := p.fixedMask
	var assignedStart [MaxRegions]int
	for i := 0; i < MaxRegions; i++ {
		assignedStart[i] = -1
	}
	for i := 0; i < p.n; i++ {
		if p.fixedMask&(1<<uint(i)) != 0 {
			assignedStart[i] = p.fixedStarts[i]
		}
	}

	for i := 0; i < p.n; i++ {
		if prefix == nil || i >= len(prefix) || prefix[i] < 0 {
			continue
		}
		s := prefix[i]
		if p.fixedMask&(1<<uint(i)) != 0 {
			if s != p.fixedStarts[i] {
				return new(big.Int)
			}
			continue
		}
		if s < 0 || s >= MaxPages || !p.hasStart[i][s] {
			return new(big.Int)
		}
		assignedMask |= 1 << uint(i)
		assignedStart[i] = s
	}

	var occupied uint64
	for i := 0; i < p.n; i++ {
		if assignedMask&(1<<uint(i)) == 0 {
			continue
		}
		s := assignedStart[i]
		mask := p.span[i][s]
		if mask == 0 || occupied&mask != 0 {
			return new(big.Int)
		}
		occupied |= mask
	}

	for i := 0; i < p.n; i++ {
		if assignedMask&(1<<uint(i)) == 0 {
			continue
		}
		for j := i + 1; j < p.n; j++ {
			if assignedMask&(1<<uint(j)) == 0 || p.modAdj[i]&(1<<uint(j)) == 0 {
				continue
			}
			a, b := assignedStart[i], assignedStart[j]
			if !p.modOK[i][j][a][b] || !p.modOK[j][i][b][a] {
				return new(big.Int)
			}
		}
	}

	remaining := p.full &^ assignedMask
	if remaining == 0 {
		return big.NewInt(1)
	}

	c := &prefixCounter{
		p:             p,
		assignedMask:  assignedMask,
		assignedStart: assignedStart,
		prefixBits:    occupied,
		remainingMask: remaining,
	}
	c.build()
	return c.count()
}

// Count returns the exact number of legal layouts. The returned integer is
// independent of the planner's internal cache.
func (p *Planner) Count() *big.Int {
	p.countOnce.Do(func() {
		p.total = p.countPrefix(nil)
		if p.total == nil {
			p.total = new(big.Int)
		}
	})
	return cloneBig(p.total)
}

// Validate checks that l is a legal layout for this planner.
func (p *Planner) Validate(l Layout) error {
	if len(l.Starts) != p.n {
		return fmt.Errorf("%w: got %d starts, want %d", ErrInvalidLayout, len(l.Starts), p.n)
	}
	var occupied uint64
	for i, s := range l.Starts {
		if s < 0 || s >= MaxPages || !p.hasStart[i][s] {
			return fmt.Errorf("%w: region %d start %d is outside its domain", ErrInvalidLayout, i, s)
		}
		mask := p.span[i][s]
		if mask == 0 || occupied&mask != 0 {
			return fmt.Errorf("%w: region %d overlaps another region", ErrInvalidLayout, i)
		}
		occupied |= mask
	}
	for i := 0; i < p.n; i++ {
		for j := i + 1; j < p.n; j++ {
			if p.modAdj[i]&(1<<uint(j)) == 0 {
				continue
			}
			a, b := l.Starts[i], l.Starts[j]
			if !p.modOK[i][j][a][b] || !p.modOK[j][i][b][a] {
				return fmt.Errorf("%w: regions %d and %d violate a modular constraint", ErrInvalidLayout, i, j)
			}
		}
	}
	return nil
}

func normalizeConfig(cfg Config) (Config, error) {
	if cfg.Pages < 0 || cfg.Pages > MaxPages {
		return Config{}, fmt.Errorf("layout: pages must be in [0,%d], got %d", MaxPages, cfg.Pages)
	}
	if len(cfg.Regions) > MaxRegions {
		return Config{}, fmt.Errorf("layout: at most %d regions are supported, got %d", MaxRegions, len(cfg.Regions))
	}

	out := cloneConfig(cfg)
	out.Reserved = append([]int(nil), cfg.Reserved...)
	sort.Ints(out.Reserved)
	out.Reserved = dedupInts(out.Reserved)
	for _, page := range out.Reserved {
		if page < 0 || page >= cfg.Pages {
			return Config{}, fmt.Errorf("layout: reserved page %d is outside [0,%d)", page, cfg.Pages)
		}
	}

	names := make(map[string]struct{}, len(out.Regions))
	for i := range out.Regions {
		r := &out.Regions[i]
		if strings.TrimSpace(r.Name) == "" {
			return Config{}, fmt.Errorf("layout: region %d has an empty name", i)
		}
		if _, ok := names[r.Name]; ok {
			return Config{}, fmt.Errorf("layout: duplicate region name %q", r.Name)
		}
		names[r.Name] = struct{}{}
		if r.Length < 1 {
			return Config{}, fmt.Errorf("layout: region %q length must be positive", r.Name)
		}
		if r.Alignment < 1 || r.Alignment&(r.Alignment-1) != 0 {
			return Config{}, fmt.Errorf("layout: region %q alignment %d is not a power of two", r.Name, r.Alignment)
		}
		if r.Pinned && r.Stable {
			return Config{}, fmt.Errorf("layout: region %q cannot be both pinned and stable", r.Name)
		}
	}

	out.Stable = append([]StableMapping(nil), cfg.Stable...)
	stableSeen := make(map[mappingKey]struct{}, len(out.Stable))
	for _, m := range out.Stable {
		if strings.TrimSpace(m.Name) == "" {
			return Config{}, fmt.Errorf("layout: stable mapping has an empty name")
		}
		if m.Length < 1 {
			return Config{}, fmt.Errorf("layout: stable mapping %q has non-positive length", m.Name)
		}
		if m.Start < 0 || m.Start+m.Length > cfg.Pages {
			return Config{}, fmt.Errorf("layout: stable mapping %q [%d,%d) does not fit in %d pages", m.Name, m.Start, m.Start+m.Length, cfg.Pages)
		}
		key := mappingKey{name: m.Name, generation: m.Generation}
		if _, ok := stableSeen[key]; ok {
			return Config{}, fmt.Errorf("layout: duplicate stable mapping for %q generation %d", m.Name, m.Generation)
		}
		stableSeen[key] = struct{}{}
	}
	sort.Slice(out.Stable, func(i, j int) bool {
		return lessStable(out.Stable[i], out.Stable[j])
	})

	out.Leases = make([]Lease, len(cfg.Leases))
	for i, lease := range cfg.Leases {
		normalized := Lease{Name: lease.Name, Generation: lease.Generation}
		if len(lease.Pages) > 0 {
			pages := append([]int(nil), lease.Pages...)
			sort.Ints(pages)
			pages = dedupInts(pages)
			for _, page := range pages {
				if page < 0 || page >= cfg.Pages {
					return Config{}, fmt.Errorf("layout: lease %q page %d is outside [0,%d)", lease.Name, page, cfg.Pages)
				}
			}
			normalized.Pages = pages
		} else {
			if lease.Length < 1 {
				return Config{}, fmt.Errorf("layout: lease %q has non-positive length", lease.Name)
			}
			if lease.Start < 0 || lease.Start+lease.Length > cfg.Pages {
				return Config{}, fmt.Errorf("layout: lease %q [%d,%d) does not fit in %d pages", lease.Name, lease.Start, lease.Start+lease.Length, cfg.Pages)
			}
			normalized.Start = lease.Start
			normalized.Length = lease.Length
		}
		out.Leases[i] = normalized
	}
	sort.Slice(out.Leases, func(i, j int) bool {
		return lessLease(out.Leases[i], out.Leases[j])
	})

	out.OldReaderPages = append([]int(nil), cfg.OldReaderPages...)
	sort.Ints(out.OldReaderPages)
	out.OldReaderPages = dedupInts(out.OldReaderPages)
	for _, page := range out.OldReaderPages {
		if page < 0 || page >= cfg.Pages {
			return Config{}, fmt.Errorf("layout: old-reader page %d is outside [0,%d)", page, cfg.Pages)
		}
	}

	out.Constraints = make([]Constraint, len(cfg.Constraints))
	for i, c := range cfg.Constraints {
		if c.A < 0 || c.A >= len(out.Regions) || c.B < 0 || c.B >= len(out.Regions) {
			return Config{}, fmt.Errorf("layout: constraint %d references a region outside [0,%d)", i, len(out.Regions))
		}
		if c.Modulus == 0 {
			return Config{}, fmt.Errorf("layout: constraint %d has zero modulus", i)
		}
		allowed := append([]uint64(nil), c.Allowed...)
		for _, v := range allowed {
			if v >= c.Modulus {
				return Config{}, fmt.Errorf("layout: constraint %d has residue %d >= modulus %d", i, v, c.Modulus)
			}
		}
		sort.Slice(allowed, func(x, y int) bool { return allowed[x] < allowed[y] })
		allowed = dedupUint64s(allowed)
		out.Constraints[i] = Constraint{A: c.A, B: c.B, Modulus: c.Modulus, Allowed: allowed}
	}
	sort.Slice(out.Constraints, func(i, j int) bool {
		return lessConstraint(out.Constraints[i], out.Constraints[j])
	})

	return out, nil
}

func cloneConfig(cfg Config) Config {
	out := Config{
		Pages:      cfg.Pages,
		Generation: cfg.Generation,
	}
	out.Regions = append([]Region(nil), cfg.Regions...)
	out.Reserved = append([]int(nil), cfg.Reserved...)
	out.Stable = append([]StableMapping(nil), cfg.Stable...)
	out.Leases = make([]Lease, len(cfg.Leases))
	for i, lease := range cfg.Leases {
		out.Leases[i] = lease
		out.Leases[i].Pages = append([]int(nil), lease.Pages...)
	}
	out.OldReaderPages = append([]int(nil), cfg.OldReaderPages...)
	out.Constraints = make([]Constraint, len(cfg.Constraints))
	for i, c := range cfg.Constraints {
		out.Constraints[i] = Constraint{
			A:       c.A,
			B:       c.B,
			Modulus: c.Modulus,
			Allowed: append([]uint64(nil), c.Allowed...),
		}
	}
	return out
}

func fingerprintConfig(cfg Config) ([32]byte, error) {
	body, err := json.Marshal(cfg)
	if err != nil {
		return [32]byte{}, fmt.Errorf("layout: cannot fingerprint configuration: %w", err)
	}
	prefix := []byte("layout-config-v1\x00")
	h := sha256.New()
	_, _ = h.Write(prefix)
	_, _ = h.Write(body)
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

type mappingKey struct {
	name       string
	generation uint64
}

func lessStable(a, b StableMapping) bool {
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	if a.Generation != b.Generation {
		return a.Generation < b.Generation
	}
	if a.Start != b.Start {
		return a.Start < b.Start
	}
	return a.Length < b.Length
}

func lessLease(a, b Lease) bool {
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	if a.Generation != b.Generation {
		return a.Generation < b.Generation
	}
	if len(a.Pages) != len(b.Pages) {
		return len(a.Pages) < len(b.Pages)
	}
	for i := range a.Pages {
		if a.Pages[i] != b.Pages[i] {
			return a.Pages[i] < b.Pages[i]
		}
	}
	if a.Start != b.Start {
		return a.Start < b.Start
	}
	return a.Length < b.Length
}

func lessConstraint(a, b Constraint) bool {
	if a.A != b.A {
		return a.A < b.A
	}
	if a.B != b.B {
		return a.B < b.B
	}
	if a.Modulus != b.Modulus {
		return a.Modulus < b.Modulus
	}
	if len(a.Allowed) != len(b.Allowed) {
		return len(a.Allowed) < len(b.Allowed)
	}
	for i := range a.Allowed {
		if a.Allowed[i] != b.Allowed[i] {
			return a.Allowed[i] < b.Allowed[i]
		}
	}
	return false
}

func containsUint64(values []uint64, want uint64) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func dedupInts(values []int) []int {
	if len(values) == 0 {
		return nil
	}
	out := values[:1]
	for _, v := range values[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

func dedupUint64s(values []uint64) []uint64 {
	if len(values) == 0 {
		return nil
	}
	out := values[:1]
	for _, v := range values[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

func constraintHolds(c Constraint, a, b int) bool {
	diff := modDiff(a, b, c.Modulus)
	i := sort.Search(len(c.Allowed), func(i int) bool { return c.Allowed[i] >= diff })
	return i < len(c.Allowed) && c.Allowed[i] == diff
}

func modDiff(a, b int, modulus uint64) uint64 {
	if modulus == 0 {
		return 0
	}
	au := uint64(a) % modulus
	bu := uint64(b) % modulus
	if au >= bu {
		return au - bu
	}
	return modulus - (bu - au)
}

func spanMask(start, length int) uint64 {
	if start < 0 || length <= 0 || start+length > 64 {
		return 0
	}
	if length == 64 {
		return ^uint64(0)
	}
	return ((uint64(1) << uint(length)) - 1) << uint(start)
}

func leaseMask(lease Lease) uint64 {
	if len(lease.Pages) > 0 {
		var mask uint64
		for _, page := range lease.Pages {
			if page >= 0 && page < 64 {
				mask |= uint64(1) << uint(page)
			}
		}
		return mask
	}
	return spanMask(lease.Start, lease.Length)
}
