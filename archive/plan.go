package archive

import (
	"container/heap"
	"fmt"
	"math"
	"math/bits"
	"sort"
)

// NoLimit disables a quota when used in Limits.
const NoLimit uint64 = ^uint64(0)

// Limits keeps the two resource accounts separate.  DecodeBlocks bounds the
// number of physical blocks actually decoded; ReturnBytes bounds the union of
// requested root coordinates.  CacheCapacity is measured in physical pages.
type Limits struct {
	CacheCapacity uint64
	DecodeBlocks  uint64
	ReturnBytes   uint64
}

// DefaultLimits returns limits with a real cache capacity and no quotas.
func DefaultLimits(cacheCapacity uint64) Limits {
	return Limits{CacheCapacity: cacheCapacity, DecodeBlocks: NoLimit, ReturnBytes: NoLimit}
}

// Request asks for Length root coordinates beginning at Start.
type Request struct {
	Start  uint64
	Length uint64
}

// NewRequest constructs a root-coordinate read request.
func NewRequest(start, length uint64) Request { return Request{Start: start, Length: length} }

// End returns Start+Length and whether the addition overflowed.
func (r Request) End() (uint64, bool) {
	if r.Start > ^uint64(0)-r.Length {
		return 0, false
	}
	return r.Start + r.Length, true
}

// CacheCheckpoint describes pages already present before execution.  Resident
// is ordered oldest to newest, matching LRU recency.  A checkpoint whose
// Version or StrategyDigest does not match the manifest is stale evidence: it
// is ignored rather than trusted, and its pages do not save decode work.
type CacheCheckpoint struct {
	Version        uint64
	StrategyDigest Digest
	Resident       []BlockID
}

// PlanOptions configures PlanBatch.
type PlanOptions struct {
	Limits             Limits
	Checkpoint         *CacheCheckpoint
	ExactLimit         int  // exhaustive request-order search up to this many requests; default 8
	PreferRequestOrder bool // among equal-cost plans, prefer one closer to caller order
}

// DefaultPlanOptions returns sensible defaults for a cache of the given
// capacity.
func DefaultPlanOptions(cacheCapacity uint64) PlanOptions {
	return PlanOptions{Limits: DefaultLimits(cacheCapacity), ExactLimit: 8, PreferRequestOrder: true}
}

// StepKind is the kind of an execution step.
type StepKind uint8

const (
	// StepLoad ensures a physical block is resident.  If it is already resident
	// the step is a cache hit and costs no decode work.  Otherwise it is a
	// decode, optionally evicting one resident page first.
	StepLoad StepKind = iota + 1
	// StepEmit materializes one request.  All of that request's physical blocks
	// must be resident at this point.
	StepEmit
)

// Step is one plan operation.  Physical work may be reordered, but Emit steps
// carry the original request index so output can be returned in request order.
type Step struct {
	Kind    StepKind
	Block   BlockID
	Evict   bool
	Victim  BlockID
	Request int
}

// Plan is a compact execution plan.  Its size depends on manifest shape and
// request count, not on the expanded length: repeated subgraphs are kept as
// Piece trees with symbolic repetition counts.
type Plan struct {
	ManifestFingerprint Digest
	Version             uint64
	Tenant              string
	GrantDigest         Digest
	Limits              Limits

	Requests []Request
	Sources  [][]*Piece

	Steps []Step

	InitialCache      []BlockID
	CheckpointVersion uint64
	CheckpointDigest  Digest
	CheckpointStale   bool

	RequiredBlocks []BlockID
	DecodeBlocks   uint64
	ReturnBytes    uint64
	Exact          bool
}

// PlanBatch computes a lowest-decode-cost execution plan for a batch of reads.
// All requests are authorized before planning; any unauthorized request rejects
// the entire batch.  The result distinguishes:
//
//   - ErrUnauthorized: at least one read is outside the tenant's root intervals.
//   - ErrReturnQuota: the deduplicated returned-data account exceeds its quota.
//   - ErrNoFeasiblePlan: the proven minimum decode cost exceeds its quota (or a
//     request needs more distinct pages than can ever be resident at once).
//   - ErrIncompleteSearch: the heuristic search used for a large batch could
//     not prove whether a feasible plan exists.
//   - ErrInsufficientEvidence: the manifest/checkpoint facts needed to decide
//     are missing, stale in an inconsistent way, or invalid.
func PlanBatch(m *Manifest, g Grant, batch []Request, opts PlanOptions) (*Plan, error) {
	if m == nil {
		return nil, planErr(ReasonInsufficientEvidence, "plan", "nil manifest", nil)
	}
	if err := m.Validate(); err != nil {
		return nil, planErr(ReasonInsufficientEvidence, "plan", "manifest does not verify", err)
	}
	if err := g.Validate(); err != nil {
		return nil, planErr(ReasonInvalidInput, "plan", "invalid grant", err)
	}
	grant := g.Normalized()

	for i, r := range batch {
		end, ok := r.End()
		if !ok {
			return nil, planErr(ReasonInvalidInput, "plan", fmt.Sprintf("request %d overflows", i), nil)
		}
		if end > m.TotalLength {
			return nil, planErr(ReasonInvalidInput, "plan",
				fmt.Sprintf("request %d [%d,%d) beyond total length %d", i, r.Start, end, m.TotalLength), nil)
		}
	}

	// Whole-batch authorization happens before resolution, planning, or any
	// decoder callback.
	for i, r := range batch {
		if !grant.Covers(r.Start, r.Start+r.Length) {
			return nil, planErr(ReasonUnauthorized, "plan",
				fmt.Sprintf("request %d [%d,%d) is outside the tenant grant", i, r.Start, r.Start+r.Length), nil)
		}
	}

	sources := make([][]*Piece, len(batch))
	needs := make([][]BlockID, len(batch))
	for i, r := range batch {
		parts, err := Resolve(m, r.Start, r.Start+r.Length)
		if err != nil {
			return nil, planErr(ReasonInsufficientEvidence, "plan",
				fmt.Sprintf("cannot resolve request %d", i), err)
		}
		sources[i] = parts
		needs[i] = SourceBlocks(parts)
	}

	returnBytes := unionLength(batch)
	if returnBytes > opts.Limits.ReturnBytes {
		return nil, planErr(ReasonReturnQuota, "plan",
			fmt.Sprintf("union of requested roots is %d bytes, quota %d", returnBytes, opts.Limits.ReturnBytes), nil)
	}

	blockDefs := make(map[BlockID]Block, len(m.Blocks))
	for _, b := range m.Blocks {
		blockDefs[b.ID] = b
	}

	initial, ckVersion, ckDigest, stale, err := prepareCheckpoint(m, opts.Checkpoint, opts.Limits.CacheCapacity, blockDefs)
	if err != nil {
		return nil, err
	}

	needSets := make([]map[BlockID]bool, len(needs))
	for i, list := range needs {
		s := make(map[BlockID]bool, len(list))
		for _, id := range list {
			s[id] = true
		}
		needSets[i] = s
	}

	capacity := opts.Limits.CacheCapacity
	for i, list := range needs {
		if uint64(len(list)) > capacity {
			return nil, planErr(ReasonNoFeasiblePlan, "plan",
				fmt.Sprintf("request %d needs %d distinct pages but cache holds %d", i, len(list), capacity), nil)
		}
	}

	// A proven lower bound: every required block that is not already resident
	// must be decoded at least once.
	required := make(map[BlockID]bool)
	for _, list := range needs {
		for _, id := range list {
			required[id] = true
		}
	}
	lowerBound := uint64(0)
	for id := range required {
		if !containsBlock(initial, id) {
			lowerBound++
		}
	}
	if lowerBound > opts.Limits.DecodeBlocks {
		return nil, planErr(ReasonNoFeasiblePlan, "plan",
			fmt.Sprintf("at least %d distinct pages must be decoded, quota %d", lowerBound, opts.Limits.DecodeBlocks), nil)
	}

	exactLimit := opts.ExactLimit
	if exactLimit <= 0 {
		exactLimit = 8
	}
	if exactLimit > 9 {
		exactLimit = 9
	}

	var order []int
	var sim simResult
	exact := len(batch) <= exactLimit
	if exact {
		order, sim = bestEmissionOrder(batch, needs, needSets, initial, capacity, opts.PreferRequestOrder)
	} else {
		if s, ok := exactCacheSearch(needs, initial, capacity); ok {
			sim = s
			exact = true
		} else {
			order = greedyEmissionOrder(needs, needSets, initial, capacity, len(batch))
			order, sim = improveEmissionOrder(order, needs, needSets, initial, capacity, opts.PreferRequestOrder)
		}
	}

	if sim.misses > opts.Limits.DecodeBlocks {
		if !exact && lowerBound <= opts.Limits.DecodeBlocks {
			return nil, planErr(ReasonIncompleteSearch, "plan",
				fmt.Sprintf("heuristic found %d decodes, quota %d; lower bound %d is within quota", sim.misses, opts.Limits.DecodeBlocks, lowerBound), nil)
		}
		return nil, planErr(ReasonNoFeasiblePlan, "plan",
			fmt.Sprintf("minimum decode work is %d pages, quota %d", sim.misses, opts.Limits.DecodeBlocks), nil)
	}

	reqUnion := make([]BlockID, 0, len(required))
	for id := range required {
		reqUnion = append(reqUnion, id)
	}
	sort.Slice(reqUnion, func(i, j int) bool { return reqUnion[i] < reqUnion[j] })

	p := &Plan{
		ManifestFingerprint: m.Fingerprint(),
		Version:             m.Version,
		Tenant:              grant.Tenant,
		GrantDigest:         grant.Digest(),
		Limits:              opts.Limits,
		Requests:            append([]Request(nil), batch...),
		Sources:             ClonePiecesSlice(sources),
		Steps:               sim.steps,
		InitialCache:        append([]BlockID(nil), initial...),
		CheckpointVersion:   ckVersion,
		CheckpointDigest:    ckDigest,
		CheckpointStale:     stale,
		RequiredBlocks:      reqUnion,
		DecodeBlocks:        sim.misses,
		ReturnBytes:         returnBytes,
		Exact:               exact,
	}
	return p, nil
}

func prepareCheckpoint(m *Manifest, cp *CacheCheckpoint, capacity uint64, defs map[BlockID]Block) (initial []BlockID, version uint64, digest Digest, stale bool, err error) {
	if cp == nil {
		return nil, 0, Digest{}, false, nil
	}
	version = cp.Version
	digest = cp.StrategyDigest
	if cp.Version != m.Version || !cp.StrategyDigest.Equal(m.StrategyDigest) {
		// Old-version or old-strategy cache is not evidence for this manifest.
		return nil, version, digest, true, nil
	}
	if uint64(len(cp.Resident)) > capacity {
		return nil, version, digest, false, planErr(ReasonInsufficientEvidence, "plan",
			fmt.Sprintf("checkpoint has %d pages but cache capacity is %d", len(cp.Resident), capacity), nil)
	}
	seen := make(map[BlockID]int, len(cp.Resident))
	ordered := make([]BlockID, 0, len(cp.Resident))
	for _, id := range cp.Resident {
		if _, ok := defs[id]; !ok {
			return nil, version, digest, false, planErr(ReasonInsufficientEvidence, "plan",
				fmt.Sprintf("checkpoint references unknown block %d", id), nil)
		}
		if prev, ok := seen[id]; ok {
			// The later occurrence is the more recent one; keep it.
			ordered = append(ordered[:prev], ordered[prev+1:]...)
			for k := range seen {
				if seen[k] > prev {
					seen[k]--
				}
			}
		}
		seen[id] = len(ordered)
		ordered = append(ordered, id)
	}
	return ordered, version, digest, false, nil
}

func containsBlock(list []BlockID, id BlockID) bool {
	for _, x := range list {
		if x == id {
			return true
		}
	}
	return false
}

// ClonePiecesSlice deep-copies a slice of piece lists.
func ClonePiecesSlice(in [][]*Piece) [][]*Piece {
	if in == nil {
		return nil
	}
	out := make([][]*Piece, len(in))
	for i := range in {
		out[i] = ClonePieces(in[i])
	}
	return out
}

func unionLength(reqs []Request) uint64 {
	type span struct{ a, b uint64 }
	spans := make([]span, 0, len(reqs))
	for _, r := range reqs {
		if r.Length == 0 {
			continue
		}
		spans = append(spans, span{r.Start, r.Start + r.Length})
	}
	if len(spans) == 0 {
		return 0
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].a != spans[j].a {
			return spans[i].a < spans[j].a
		}
		return spans[i].b < spans[j].b
	})
	var total uint64
	cur := spans[0]
	for _, s := range spans[1:] {
		if s.a <= cur.b {
			if s.b > cur.b {
				cur.b = s.b
			}
			continue
		}
		total += cur.b - cur.a
		cur = s
	}
	total += cur.b - cur.a
	return total
}

type simResult struct {
	steps     []Step
	misses    uint64
	hits      uint64
	evictions uint64
	order     []int
	final     []BlockID
}

type simCache struct {
	resident map[BlockID]bool
	order    []BlockID
}

func newSimCache(initial []BlockID, capacity uint64) *simCache {
	c := &simCache{resident: make(map[BlockID]bool, len(initial))}
	limit := int(capacity)
	if capacity > uint64(math.MaxInt) {
		limit = math.MaxInt
	}
	for _, id := range initial {
		if c.resident[id] {
			c.touch(id)
			continue
		}
		if limit > 0 && len(c.order) >= limit {
			delete(c.resident, c.order[0])
			c.order = c.order[1:]
		}
		c.resident[id] = true
		c.order = append(c.order, id)
	}
	return c
}

func (c *simCache) has(id BlockID) bool { return c.resident[id] }

func (c *simCache) full(capacity uint64) bool { return uint64(len(c.order)) >= capacity }

func (c *simCache) touch(id BlockID) {
	for i, x := range c.order {
		if x == id {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
	c.order = append(c.order, id)
}

func (c *simCache) remove(id BlockID) {
	delete(c.resident, id)
	for i, x := range c.order {
		if x == id {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}

func (c *simCache) insert(id BlockID) {
	c.resident[id] = true
	c.order = append(c.order, id)
}

func simulate(order []int, needs [][]BlockID, needSets []map[BlockID]bool, initial []BlockID, capacity uint64) simResult {
	cache := newSimCache(initial, capacity)
	res := simResult{order: append([]int(nil), order...)}
	for pos, req := range order {
		for _, id := range needs[req] {
			if cache.has(id) {
				cache.touch(id)
				res.hits++
				res.steps = append(res.steps, Step{Kind: StepLoad, Block: id})
				continue
			}
			var victim BlockID
			hasVictim := false
			if cache.full(capacity) {
				victim, hasVictim = chooseVictim(cache, needSets[req], order, needSets, pos)
				if !hasVictim {
					// This should be impossible: a request's requirement set is
					// no larger than capacity, so some non-current page exists.
					victim = cache.order[0]
					hasVictim = true
				}
				cache.remove(victim)
				res.evictions++
			}
			res.steps = append(res.steps, Step{Kind: StepLoad, Block: id, Evict: hasVictim, Victim: victim})
			cache.insert(id)
			cache.touch(id)
			res.misses++
		}
		res.steps = append(res.steps, Step{Kind: StepEmit, Request: req})
	}
	res.final = append([]BlockID(nil), cache.order...)
	return res
}

func chooseVictim(cache *simCache, currentNeed map[BlockID]bool, order []int, needSets []map[BlockID]bool, pos int) (BlockID, bool) {
	var best BlockID
	bestUse := uint64(0)
	found := false
	for i, id := range cache.order {
		if currentNeed[id] {
			continue
		}
		use := nextUse(order, needSets, pos, id)
		if !found || use > bestUse || (use == bestUse && i == 0) {
			best = id
			bestUse = use
			found = true
		}
	}
	return best, found
}

func nextUse(order []int, needSets []map[BlockID]bool, pos int, id BlockID) uint64 {
	for k := pos; k < len(order); k++ {
		if needSets[order[k]][id] {
			return uint64(k)
		}
	}
	return math.MaxUint64
}

func betterSim(a, b simResult, preferOriginal bool) bool {
	if a.misses != b.misses {
		return a.misses < b.misses
	}
	if a.evictions != b.evictions {
		return a.evictions < b.evictions
	}
	if preferOriginal {
		da := disturbance(a.order)
		db := disturbance(b.order)
		if da != db {
			return da < db
		}
	}
	for i := 0; i < len(a.order) && i < len(b.order); i++ {
		if a.order[i] != b.order[i] {
			return a.order[i] < b.order[i]
		}
	}
	return len(a.order) < len(b.order)
}

func disturbance(order []int) int {
	d := 0
	for pos, req := range order {
		v := pos - req
		if v < 0 {
			v = -v
		}
		d += v
	}
	return d
}

func bestEmissionOrder(batch []Request, needs [][]BlockID, needSets []map[BlockID]bool, initial []BlockID, capacity uint64, preferOriginal bool) ([]int, simResult) {
	n := len(batch)
	if n == 0 {
		return nil, simulate(nil, needs, needSets, initial, capacity)
	}
	used := make([]bool, n)
	cur := make([]int, 0, n)
	var best *simResult
	var rec func()
	rec = func() {
		if len(cur) == n {
			s := simulate(cur, needs, needSets, initial, capacity)
			if best == nil || betterSim(s, *best, preferOriginal) {
				cp := s
				best = &cp
			}
			return
		}
		for i := 0; i < n; i++ {
			if used[i] {
				continue
			}
			used[i] = true
			cur = append(cur, i)
			rec()
			cur = cur[:len(cur)-1]
			used[i] = false
		}
	}
	rec()
	return best.order, *best
}

func greedyEmissionOrder(needs [][]BlockID, needSets []map[BlockID]bool, initial []BlockID, capacity uint64, n int) []int {
	if n == 0 {
		return nil
	}
	remaining := make([]bool, n)
	for i := range remaining {
		remaining[i] = true
	}
	cache := newSimCache(initial, capacity)
	order := make([]int, 0, n)
	for len(order) < n {
		bestReq := -1
		bestMissing := math.MaxInt
		for i := 0; i < n; i++ {
			if !remaining[i] {
				continue
			}
			missing := 0
			for _, id := range needs[i] {
				if !cache.has(id) {
					missing++
				}
			}
			if missing < bestMissing {
				bestMissing = missing
				bestReq = i
			}
		}
		req := bestReq
		order = append(order, req)
		remaining[req] = false
		for _, id := range needs[req] {
			if cache.has(id) {
				cache.touch(id)
				continue
			}
			if cache.full(capacity) {
				// Evict a page not needed by this request, preferring one not
				// needed by any remaining request.
				victim := cache.order[0]
				for _, cand := range cache.order {
					if needSets[req][cand] {
						continue
					}
					victim = cand
					neededLater := false
					for k := 0; k < n; k++ {
						if remaining[k] && k != req && needSets[k][cand] {
							neededLater = true
							break
						}
					}
					if !neededLater {
						break
					}
				}
				cache.remove(victim)
			}
			cache.insert(id)
			cache.touch(id)
		}
	}
	return order
}

func improveEmissionOrder(order []int, needs [][]BlockID, needSets []map[BlockID]bool, initial []BlockID, capacity uint64, preferOriginal bool) ([]int, simResult) {
	bestOrder := append([]int(nil), order...)
	best := simulate(bestOrder, needs, needSets, initial, capacity)
	n := len(bestOrder)
	if n > 24 {
		return bestOrder, best
	}
	for pass := 0; pass < 3; pass++ {
		improved := false
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				cand := append([]int(nil), bestOrder...)
				cand[i], cand[j] = cand[j], cand[i]
				s := simulate(cand, needs, needSets, initial, capacity)
				if betterSim(s, best, preferOriginal) {
					bestOrder = cand
					best = s
					improved = true
				}
			}
		}
		if !improved {
			break
		}
	}
	return bestOrder, best
}

// exactCacheSearch is a bounded 0-1 state search over (emitted set, resident
// page set).  It can prove the global minimum for batches that are too large
// for request-order enumeration, and it naturally models prefetch and explicit
// eviction.  It returns ok=false when the state-space estimate is too large.
func exactCacheSearch(needs [][]BlockID, initial []BlockID, capacity uint64) (simResult, bool) {
	const maxExactStates = 1_500_000
	n := len(needs)
	if n == 0 || n > 30 {
		return simResult{}, false
	}

	blockIndex := make(map[BlockID]int)
	for _, list := range needs {
		for _, id := range list {
			if _, ok := blockIndex[id]; !ok {
				blockIndex[id] = len(blockIndex)
			}
		}
	}
	for _, id := range initial {
		if _, ok := blockIndex[id]; !ok {
			blockIndex[id] = len(blockIndex)
		}
	}
	k := len(blockIndex)
	if k > 30 {
		return simResult{}, false
	}

	combo := cacheSetCount(k, capacity)
	emittedStates := uint64(1) << uint(n)
	if combo == ^uint64(0) || emittedStates > maxExactStates || combo > maxExactStates/emittedStates {
		return simResult{}, false
	}

	needMask := make([]uint64, n)
	var neededMask uint64
	for i, list := range needs {
		for _, id := range list {
			bit := uint64(1) << uint(blockIndex[id])
			needMask[i] |= bit
			neededMask |= bit
		}
	}
	var initialMask uint64
	for _, id := range initial {
		initialMask |= uint64(1) << uint(blockIndex[id])
	}
	idOfBit := make([]BlockID, k)
	for id, idx := range blockIndex {
		idOfBit[idx] = id
	}

	goalEmitted := uint64(1)<<uint(n) - 1
	start := exactState{emitted: 0, cache: initialMask}
	if start.emitted == goalEmitted {
		return simResult{}, true
	}
	dist := map[exactState]uint64{start: 0}
	prev := make(map[exactState]exactPrev)
	h := &exactHeap{{st: start, cost: 0}}
	heap.Init(h)

	for h.Len() > 0 {
		it := heap.Pop(h).(exactItem)
		if d, ok := dist[it.st]; !ok || d != it.cost {
			continue
		}
		if it.st.emitted == goalEmitted {
			return reconstructExact(it.st, start, prev, idOfBit, n), true
		}

		// Emit any request whose pages are resident.  Cost 0.
		for i := 0; i < n; i++ {
			bit := uint64(1) << uint(i)
			if it.st.emitted&bit != 0 || needMask[i]&^it.st.cache != 0 {
				continue
			}
			ns := exactState{emitted: it.st.emitted | bit, cache: it.st.cache}
			if old, ok := dist[ns]; !ok || old > it.cost {
				dist[ns] = it.cost
				prev[ns] = exactPrev{from: it.st, act: exactAction{load: false, req: i}}
				heap.Push(h, exactItem{st: ns, cost: it.cost})
			}
		}

		// Load any needed page.  Cost 1, evicting one resident page if full.
		budget := capacity
		if budget > uint64(k) {
			budget = uint64(k)
		}
		cacheCount := uint64(bits.OnesCount64(it.st.cache))
		for b := 0; b < k; b++ {
			bit := uint64(1) << uint(b)
			if it.st.cache&bit != 0 || neededMask&bit == 0 {
				continue
			}
			if cacheCount < budget {
				ns := exactState{emitted: it.st.emitted, cache: it.st.cache | bit}
				if old, ok := dist[ns]; !ok || old > it.cost+1 {
					dist[ns] = it.cost + 1
					prev[ns] = exactPrev{from: it.st, act: exactAction{load: true, block: idOfBit[b]}}
					heap.Push(h, exactItem{st: ns, cost: it.cost + 1})
				}
				continue
			}
			for v := 0; v < k; v++ {
				vbit := uint64(1) << uint(v)
				if it.st.cache&vbit == 0 {
					continue
				}
				ns := exactState{emitted: it.st.emitted, cache: (it.st.cache &^ vbit) | bit}
				if old, ok := dist[ns]; !ok || old > it.cost+1 {
					dist[ns] = it.cost + 1
					prev[ns] = exactPrev{from: it.st, act: exactAction{load: true, block: idOfBit[b], victim: idOfBit[v], evict: true}}
					heap.Push(h, exactItem{st: ns, cost: it.cost + 1})
				}
			}
		}
	}
	return simResult{}, false
}

type exactState struct {
	emitted uint64
	cache   uint64
}

type exactAction struct {
	load   bool
	block  BlockID
	evict  bool
	victim BlockID
	req    int
}

type exactPrev struct {
	from exactState
	act  exactAction
}

type exactItem struct {
	st   exactState
	cost uint64
}

type exactHeap []exactItem

func (h exactHeap) Len() int           { return len(h) }
func (h exactHeap) Less(i, j int) bool { return h[i].cost < h[j].cost }
func (h exactHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *exactHeap) Push(x any)        { *h = append(*h, x.(exactItem)) }
func (h *exactHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

func reconstructExact(goal, start exactState, prev map[exactState]exactPrev, idOfBit []BlockID, n int) simResult {
	var acts []exactAction
	cur := goal
	for cur != start {
		p, ok := prev[cur]
		if !ok {
			break
		}
		acts = append(acts, p.act)
		cur = p.from
	}
	for i, j := 0, len(acts)-1; i < j; i, j = i+1, j-1 {
		acts[i], acts[j] = acts[j], acts[i]
	}
	res := simResult{}
	for _, a := range acts {
		if a.load {
			res.steps = append(res.steps, Step{Kind: StepLoad, Block: a.block, Evict: a.evict, Victim: a.victim})
			res.misses++
			if a.evict {
				res.evictions++
			}
		} else {
			res.steps = append(res.steps, Step{Kind: StepEmit, Request: a.req})
			res.order = append(res.order, a.req)
		}
	}
	for b := 0; b < len(idOfBit); b++ {
		if goal.cache&(uint64(1)<<uint(b)) != 0 {
			res.final = append(res.final, idOfBit[b])
		}
	}
	return res
}

func cacheSetCount(k int, capacity uint64) uint64 {
	if capacity > uint64(k) {
		capacity = uint64(k)
	}
	var sum uint64
	c := uint64(1)
	for i := 0; i <= k; i++ {
		if uint64(i) <= capacity {
			if sum > ^uint64(0)-c {
				return ^uint64(0)
			}
			sum += c
		}
		if i == k {
			break
		}
		// C(k, i+1) = C(k, i) * (k-i) / (i+1)
		num := uint64(k - i)
		if num != 0 && c > ^uint64(0)/num {
			return ^uint64(0)
		}
		c = c * num / uint64(i+1)
	}
	return sum
}
