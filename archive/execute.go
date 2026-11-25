package archive

import (
	"context"
	"fmt"
	"sort"
)

// PageCache is a bounded cache of decoded physical blocks.  Capacity is in
// pages (physical blocks), not bytes.  It is not safe for concurrent use.
type PageCache struct {
	Capacity       uint64
	Version        uint64
	StrategyDigest Digest

	pages map[BlockID][]byte
	order []BlockID // oldest to newest
}

// NewPageCache creates an empty cache tagged with a manifest version and
// compression-strategy digest.
func NewPageCache(capacity, version uint64, strategy Digest) *PageCache {
	return &PageCache{
		Capacity:       capacity,
		Version:        version,
		StrategyDigest: strategy,
		pages:          make(map[BlockID][]byte),
	}
}

// Len returns the number of resident pages.
func (c *PageCache) Len() int { return len(c.order) }

// Contains reports whether a block is resident.
func (c *PageCache) Contains(id BlockID) bool {
	if c == nil {
		return false
	}
	_, ok := c.pages[id]
	return ok
}

// Get returns the decoded page for a block.  The returned slice must not be
// mutated by the caller.
func (c *PageCache) Get(id BlockID) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	p, ok := c.pages[id]
	return p, ok
}

// Put inserts or refreshes a page.  If the cache is full, the oldest page is
// evicted.  Executor-driven eviction uses explicit victims instead.
func (c *PageCache) Put(id BlockID, page []byte) {
	if c == nil {
		return
	}
	if c.pages == nil {
		c.pages = make(map[BlockID][]byte)
	}
	if _, ok := c.pages[id]; ok {
		c.pages[id] = page
		c.touch(id)
		return
	}
	if c.Capacity == 0 {
		return
	}
	if uint64(len(c.order)) >= c.Capacity {
		c.evict(c.order[0])
	}
	c.pages[id] = page
	c.order = append(c.order, id)
}

// Evict removes a specific page if present.
func (c *PageCache) Evict(id BlockID) bool {
	if c == nil {
		return false
	}
	if _, ok := c.pages[id]; !ok {
		return false
	}
	c.evict(id)
	return true
}

func (c *PageCache) evict(id BlockID) {
	delete(c.pages, id)
	for i, x := range c.order {
		if x == id {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}

func (c *PageCache) touch(id BlockID) {
	for i, x := range c.order {
		if x == id {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
	c.order = append(c.order, id)
}

// Snapshot returns a checkpoint of the cache, ordered oldest to newest.
func (c *PageCache) Snapshot() CacheCheckpoint {
	if c == nil {
		return CacheCheckpoint{}
	}
	return CacheCheckpoint{
		Version:        c.Version,
		StrategyDigest: c.StrategyDigest,
		Resident:       append([]BlockID(nil), c.order...),
	}
}

// Checkpoint is an alias for Snapshot.
func (c *PageCache) Checkpoint() CacheCheckpoint { return c.Snapshot() }

// Clone deep-copies the cache, including decoded page bytes.
func (c *PageCache) Clone() *PageCache {
	if c == nil {
		return nil
	}
	out := &PageCache{
		Capacity:       c.Capacity,
		Version:        c.Version,
		StrategyDigest: c.StrategyDigest,
		pages:          make(map[BlockID][]byte, len(c.pages)),
		order:          append([]BlockID(nil), c.order...),
	}
	for id, p := range c.pages {
		cp := make([]byte, len(p))
		copy(cp, p)
		out.pages[id] = cp
	}
	return out
}

// DecodeFunc decodes one physical block.  It must return a page of exactly
// block.Size bytes; anything else is treated as insufficient evidence.
type DecodeFunc func(ctx context.Context, block Block) ([]byte, error)

// ExecResult contains per-request output in caller order plus the resource
// accounts actually achieved.  PlannedDecodeBlocks/PlannedHits are the
// verified plan's upper-bound accounts; with a warm cache the actual decode
// count can be lower (a hit saves work) but ReturnBytes is unchanged.
type ExecResult struct {
	Data [][]byte

	DecodeBlocks uint64
	Hits         uint64
	Evictions    uint64
	ReturnBytes  uint64

	PlannedDecodeBlocks uint64
	PlannedHits         uint64

	FinalCache []BlockID
	Audit      *Audit
}

// Execute runs a plan.  CheckPlan runs first, so an unauthorized request, a
// stale plan or a malformed source segment rejects the whole batch before the
// decoder is called even once.
//
// Execution is transactional with respect to the supplied cache: work happens
// on a private clone and the caller's cache is replaced only on success.  If
// the decoder returns context.Canceled (or any error), the caller's cache is
// untouched, making retry-after-cancellation safe.
//
// Any version-matching pages already in the supplied cache may satisfy plan
// loads as hits; pages outside the plan's cache capacity and stale-version
// pages are not trusted.  The plan's checkpoint pages must all be present.
func Execute(ctx context.Context, m *Manifest, g Grant, batch []Request, p *Plan, cache *PageCache, decode DecodeFunc) (*ExecResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	audit, err := CheckPlan(m, g, batch, p)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	blockDefs := make(map[BlockID]Block, len(m.Blocks))
	for _, b := range m.Blocks {
		blockDefs[b.ID] = b
	}

	work := NewPageCache(p.Limits.CacheCapacity, m.Version, m.StrategyDigest)
	if cache != nil && cache.Version == m.Version && cache.StrategyDigest.Equal(m.StrategyDigest) {
		// Validate every page we might serve.  A version-matching cache with a
		// corrupt page is an evidence failure, not a reason to serve fewer bytes.
		valid := make(map[BlockID][]byte, len(cache.pages))
		for _, id := range cache.order {
			page, ok := cache.pages[id]
			if !ok {
				continue
			}
			bdef, ok := blockDefs[id]
			if !ok {
				return nil, planErr(ReasonInsufficientEvidence, "execute",
					fmt.Sprintf("supplied cache contains unknown block %d", id), nil)
			}
			if uint64(len(page)) != bdef.Size {
				return nil, planErr(ReasonInsufficientEvidence, "execute",
					fmt.Sprintf("cached page for block %d has %d bytes, want %d", id, len(page), bdef.Size), nil)
			}
			valid[id] = page
		}
		// The plan's checkpoint must be a subset of the actual cache.
		for _, id := range p.InitialCache {
			if _, ok := valid[id]; !ok {
				return nil, planErr(ReasonInsufficientEvidence, "execute",
					fmt.Sprintf("plan expects block %d in the supplied cache", id), nil)
			}
		}
		selected := make(map[BlockID]bool, len(p.InitialCache))
		count := uint64(0)
		for _, id := range p.InitialCache {
			if !selected[id] {
				selected[id] = true
				count++
			}
		}
		// Fill remaining capacity with the most recently used other pages.
		for i := len(cache.order) - 1; i >= 0 && count < p.Limits.CacheCapacity; i-- {
			id := cache.order[i]
			if selected[id] {
				continue
			}
			if _, ok := valid[id]; ok {
				selected[id] = true
				count++
			}
		}
		// Preserve cache recency order among the selected pages.
		for _, id := range cache.order {
			if selected[id] {
				work.pages[id] = valid[id]
				work.order = append(work.order, id)
				delete(selected, id)
			}
		}
	} else if cache != nil && len(p.InitialCache) > 0 {
		return nil, planErr(ReasonInsufficientEvidence, "execute",
			"supplied cache belongs to a different manifest version", nil)
	} else if len(p.InitialCache) > 0 {
		return nil, planErr(ReasonInsufficientEvidence, "execute",
			"plan relies on an initial cache but none was supplied", nil)
	}

	data := make([][]byte, len(batch))
	var decodes, hits, evictions uint64
	for si, st := range p.Steps {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch st.Kind {
		case StepLoad:
			if work.Contains(st.Block) {
				work.touch(st.Block)
				hits++
				continue
			}
			if decode == nil {
				return nil, planErr(ReasonInvalidInput, "execute", "nil decoder", nil)
			}
			if st.Evict {
				if !work.Evict(st.Victim) {
					return nil, planErr(ReasonInvalidPlan, "execute",
						fmt.Sprintf("step %d victim block %d is not resident", si, st.Victim), nil)
				}
				evictions++
			}
			bdef, ok := blockDefs[st.Block]
			if !ok {
				return nil, planErr(ReasonInsufficientEvidence, "execute",
					fmt.Sprintf("step %d loads unknown block %d", si, st.Block), nil)
			}
			page, derr := decode(ctx, bdef)
			if derr != nil {
				return nil, fmt.Errorf("archive: decode block %d: %w", st.Block, derr)
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if uint64(len(page)) != bdef.Size {
				return nil, planErr(ReasonInsufficientEvidence, "execute",
					fmt.Sprintf("decoder returned %d bytes for block %d, want %d", len(page), st.Block, bdef.Size), nil)
			}
			work.pages[st.Block] = page
			work.order = append(work.order, st.Block)
			decodes++

		case StepEmit:
			if st.Request < 0 || st.Request >= len(batch) {
				return nil, planErr(ReasonInvalidPlan, "execute", fmt.Sprintf("step %d invalid request", si), nil)
			}
			out, err := Materialize(audit.Sources[st.Request], work.pages)
			if err != nil {
				return nil, planErr(ReasonLeak, "execute",
					fmt.Sprintf("request %d materialization failed", st.Request), err)
			}
			if uint64(len(out)) != batch[st.Request].Length {
				return nil, planErr(ReasonLeak, "execute",
					fmt.Sprintf("request %d produced %d bytes, want %d", st.Request, len(out), batch[st.Request].Length), nil)
			}
			data[st.Request] = out
		}
	}
	plannedLoads := audit.DecodeBlocks + audit.Hits
	if decodes+hits != plannedLoads || decodes > audit.DecodeBlocks || hits < audit.Hits || evictions > audit.Evictions {
		return nil, planErr(ReasonInvalidPlan, "execute",
			fmt.Sprintf("execution accounts decode=%d hits=%d evictions=%d are inconsistent with plan %d/%d/%d",
				decodes, hits, evictions, audit.DecodeBlocks, audit.Hits, audit.Evictions), nil)
	}
	for i, buf := range data {
		if buf == nil && batch[i].Length != 0 {
			return nil, planErr(ReasonInvalidPlan, "execute", fmt.Sprintf("request %d has no output", i), nil)
		}
	}

	if cache != nil {
		cache.Capacity = work.Capacity
		cache.Version = work.Version
		cache.StrategyDigest = work.StrategyDigest
		cache.pages = work.pages
		cache.order = work.order
	}
	return &ExecResult{
		Data:         data,
		DecodeBlocks: decodes,
		Hits:         hits,
		Evictions:    evictions,
		ReturnBytes:  audit.ReturnBytes,

		PlannedDecodeBlocks: audit.DecodeBlocks,
		PlannedHits:         audit.Hits,

		FinalCache: append([]BlockID(nil), work.order...),
		Audit:      audit,
	}, nil
}

// ReadBatch is the convenience entry point: plan and execute one batch.  It
// returns the plan as well as the result so callers can archive evidence
// (checkpoint, steps, source segments and both resource accounts).
func ReadBatch(ctx context.Context, m *Manifest, g Grant, batch []Request, opts PlanOptions, cache *PageCache, decode DecodeFunc) (*Plan, *ExecResult, error) {
	p, err := PlanBatch(m, g, batch, opts)
	if err != nil {
		return nil, nil, err
	}
	res, err := Execute(ctx, m, g, batch, p, cache, decode)
	if err != nil {
		return p, nil, err
	}
	return p, res, nil
}

// SortedBlockIDs returns a sorted copy of ids.
func SortedBlockIDs(ids []BlockID) []BlockID {
	out := append([]BlockID(nil), ids...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
