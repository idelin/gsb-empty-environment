package pagearchive

import (
	"fmt"
	"sort"
)

type planOption struct {
	full  bool
	base  int
	cost  int
	patch []byte
}

type planner struct {
	pages      [][]byte
	snapshots  [][]int
	options    [][]planOption
	minimum    []int
	metadata   int
	budget     int
	maxDepth   int
	cacheLimit int

	chosen   []*planOption
	depths   []int
	visit    []uint8
	storage  int
	best     *Layout
	bestRead int
	bestSize int
	hasWork  bool
}

// Plan selects a feasible representation for every page.
//
// The returned layout first minimizes the largest actual per-snapshot read
// byte count and, among equal read results, minimizes persistent bytes.
func Plan(input PlanInput) (*Layout, error) {
	if err := validatePlanInput(input); err != nil {
		return nil, err
	}

	pageCount := len(input.Pages)
	options := make([][]planOption, pageCount)
	minimum := make([]int, pageCount)
	for page := range input.Pages {
		fullCost := len(input.Pages[page]) + input.MetadataCost
		if fullCost < 0 {
			return nil, fmt.Errorf("%w: persistent cost overflows", ErrInvalidInput)
		}
		options[page] = []planOption{{full: true, cost: fullCost}}
		minimum[page] = fullCost
	}

	seen := make([]map[int]struct{}, pageCount)
	for page := range seen {
		seen[page] = make(map[int]struct{})
	}
	for _, edge := range input.CandidateEdges {
		if edge.From < 0 || edge.From >= pageCount || edge.To < 0 || edge.To >= pageCount {
			return nil, fmt.Errorf("%w: candidate edge references an unknown page", ErrInvalidInput)
		}
		if edge.From == edge.To {
			return nil, fmt.Errorf("%w: candidate edge cannot reference itself", ErrInvalidInput)
		}
		if _, ok := seen[edge.To][edge.From]; ok {
			return nil, fmt.Errorf("%w: duplicate candidate edge %d->%d", ErrInvalidInput, edge.From, edge.To)
		}
		seen[edge.To][edge.From] = struct{}{}
		if len(seen[edge.To]) > MaxBaseCandidates {
			return nil, fmt.Errorf("%w: page %d has more than %d candidate bases", ErrInvalidInput, edge.To, MaxBaseCandidates)
		}

		patch := canonicalPatch(input.Pages[edge.From], input.Pages[edge.To])
		cost := len(patch) + input.MetadataCost
		if cost < 0 {
			return nil, fmt.Errorf("%w: persistent cost overflows", ErrInvalidInput)
		}
		options[edge.To] = append(options[edge.To], planOption{
			base:  edge.From,
			cost:  cost,
			patch: patch,
		})
		if cost < minimum[edge.To] {
			minimum[edge.To] = cost
		}
	}

	for page := range options {
		sort.Slice(options[page], func(i, j int) bool {
			if options[page][i].cost != options[page][j].cost {
				return options[page][i].cost < options[page][j].cost
			}
			if options[page][i].full != options[page][j].full {
				return options[page][i].full
			}
			return options[page][i].base < options[page][j].base
		})
	}

	state := &planner{
		pages:      input.Pages,
		snapshots:  input.Snapshots,
		options:    options,
		minimum:    minimum,
		metadata:   input.MetadataCost,
		budget:     input.Budget,
		maxDepth:   input.MaxDepth,
		cacheLimit: input.CacheCapacity,
		chosen:     make([]*planOption, pageCount),
		depths:     make([]int, pageCount),
		visit:      make([]uint8, pageCount),
		hasWork:    len(input.Snapshots) > 0,
	}

	state.search(0)
	if state.best == nil {
		return nil, ErrNoFeasibleLayout
	}
	return state.best, nil
}

func validatePlanInput(input PlanInput) error {
	if len(input.Pages) == 0 || len(input.Pages) > MaxPages {
		return fmt.Errorf("%w: page count must be in [1,%d]", ErrInvalidInput, MaxPages)
	}
	pageSize := len(input.Pages[0])
	if pageSize == 0 {
		return fmt.Errorf("%w: pages must be non-empty", ErrInvalidInput)
	}
	for page, data := range input.Pages {
		if len(data) != pageSize {
			return fmt.Errorf("%w: page %d has length %d, want %d", ErrInvalidInput, page, len(data), pageSize)
		}
	}
	if input.MetadataCost < 0 {
		return fmt.Errorf("%w: metadata cost cannot be negative", ErrInvalidInput)
	}
	if input.Budget < 0 {
		return fmt.Errorf("%w: budget cannot be negative", ErrInvalidInput)
	}
	if input.MaxDepth < 0 || input.MaxDepth > MaxDependencyDepth {
		return fmt.Errorf("%w: max depth must be in [0,%d]", ErrInvalidInput, MaxDependencyDepth)
	}
	if input.CacheCapacity < 1 || input.CacheCapacity > MaxCachePages {
		return fmt.Errorf("%w: cache capacity must be in [1,%d]", ErrInvalidInput, MaxCachePages)
	}
	for snapshot, accesses := range input.Snapshots {
		for _, page := range accesses {
			if page < 0 || page >= len(input.Pages) {
				return fmt.Errorf("%w: snapshot %d references unknown page %d", ErrInvalidInput, snapshot, page)
			}
		}
	}
	return nil
}

func (p *planner) search(page int) {
	if page == len(p.chosen) {
		p.acceptLayout()
		return
	}

	minimumRemaining := 0
	for next := page; next < len(p.chosen); next++ {
		remainingBudget := p.budget - p.storage - minimumRemaining
		if remainingBudget < 0 || p.minimum[next] > remainingBudget {
			return
		}
		minimumRemaining += p.minimum[next]
	}
	if p.storage+minimumRemaining > p.budget {
		return
	}

	if p.best != nil && p.hasWork {
		lowerRead := p.simulate(false)
		if lowerRead > p.bestRead {
			return
		}
	}

	for optionIndex := range p.options[page] {
		option := &p.options[page][optionIndex]
		depth := 0
		if !option.full {
			ok, optionDepth := p.optionDepth(page, option)
			if !ok {
				continue
			}
			depth = optionDepth
			if depth > p.maxDepth {
				continue
			}
		}
		if p.storage+option.cost > p.budget {
			continue
		}

		p.chosen[page] = option
		p.depths[page] = depth
		p.storage += option.cost
		depthOK := p.refreshDepths(page)
		if depthOK {
			p.search(page + 1)
		}
		p.storage -= option.cost
		p.depths[page] = 0
		p.chosen[page] = nil
	}
}

func (p *planner) optionDepth(page int, option *planOption) (bool, int) {
	if option.full {
		return true, 0
	}
	if option.base < page {
		if p.chosen[option.base] == nil {
			return false, 0
		}
		return true, p.depths[option.base] + 1
	}

	current := page
	chainLength := 0
	for current >= page {
		chainLength++
		if current == page {
			current = option.base
			continue
		}
		currentOption := p.chosen[current]
		if currentOption == nil || currentOption.full {
			return true, 1
		}
		if currentOption.base == page {
			return false, 0
		}
		current = currentOption.base
	}

	if p.chosen[current] == nil {
		return true, 1
	}
	return true, p.depths[current] + chainLength
}

func (p *planner) refreshDepths(changedPage int) bool {
	for page := 0; page <= changedPage; page++ {
		p.visit[page] = 0
	}
	var depth func(int) (int, bool)
	depth = func(page int) (int, bool) {
		if page > changedPage || p.chosen[page] == nil {
			return 0, true
		}
		if p.visit[page] == 1 {
			return 0, false
		}
		if p.visit[page] == 2 {
			return p.depths[page], true
		}
		p.visit[page] = 1
		option := p.chosen[page]
		pageDepth := 0
		if option != nil && !option.full {
			baseDepth, ok := depth(option.base)
			if !ok {
				return 0, false
			}
			pageDepth = baseDepth + 1
		}
		p.visit[page] = 2
		p.depths[page] = pageDepth
		return pageDepth, true
	}
	for page := 0; page <= changedPage; page++ {
		pageDepth, ok := depth(page)
		if !ok || pageDepth > p.maxDepth {
			return false
		}
	}
	return true
}

func (p *planner) acceptLayout() {
	read := p.simulate(true)
	perSnapshot := p.snapshotReads(true)
	if p.best != nil {
		if read > p.bestRead || (read == p.bestRead && p.storage >= p.bestSize) {
			return
		}
	}

	representations := make([]Representation, len(p.chosen))
	maxDepth := 0
	for page, option := range p.chosen {
		representations[page] = Representation{
			Page:         page,
			Full:         option.full,
			Base:         option.base,
			MetadataCost: p.metadata,
		}
		if option.full {
			representations[page].Payload = append([]byte(nil), p.pages[page]...)
		} else {
			representations[page].Payload = append([]byte(nil), option.patch...)
		}
		if p.depths[page] > maxDepth {
			maxDepth = p.depths[page]
		}
	}

	layout := &Layout{
		Representations:      representations,
		PersistentBytes:      p.storage,
		MaxSnapshotReadBytes: read,
		SnapshotReadBytes:    perSnapshot,
		DependencyDepth:      maxDepth,
	}
	p.best = layout
	p.bestRead = read
	p.bestSize = p.storage
}

func (p *planner) simulate(complete bool) int {
	largest := 0
	for _, accesses := range p.snapshots {
		cache := newLRUCache(p.cacheLimit)
		total := 0
		for _, page := range accesses {
			total += p.load(page, cache, complete)
		}
		if total > largest {
			largest = total
		}
	}
	return largest
}

func (p *planner) snapshotReads(complete bool) []int {
	reads := make([]int, len(p.snapshots))
	for snapshot, accesses := range p.snapshots {
		cache := newLRUCache(p.cacheLimit)
		for _, page := range accesses {
			reads[snapshot] += p.load(page, cache, complete)
		}
	}
	return reads
}

func (p *planner) load(page int, cache *lruCache, complete bool) int {
	if cache.contains(page) {
		cache.touch(page)
		return 0
	}

	option := p.chosen[page]
	cost := p.minimum[page]
	if option != nil {
		cost = option.cost
	}
	if complete && option == nil {
		panic("pagearchive: incomplete layout reached complete simulation")
	}
	if option != nil && !option.full {
		cost += p.load(option.base, cache, complete)
	}
	cache.add(page)
	return cost
}

type lruCache struct {
	limit int
	pages []int
}

func newLRUCache(limit int) *lruCache {
	return &lruCache{limit: limit}
}

func (c *lruCache) contains(page int) bool {
	for _, cached := range c.pages {
		if cached == page {
			return true
		}
	}
	return false
}

func (c *lruCache) touch(page int) {
	for i, cached := range c.pages {
		if cached == page {
			c.pages = append(c.pages[:i], c.pages[i+1:]...)
			c.pages = append(c.pages, page)
			return
		}
	}
}

func (c *lruCache) add(page int) {
	c.touch(page)
	c.pages = append(c.pages, page)
	if len(c.pages) > c.limit {
		c.pages = c.pages[1:]
	}
}
