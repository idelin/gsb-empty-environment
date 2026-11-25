package archive

import (
	"fmt"
	"sort"
)

// Audit is the compact re-verification result produced by CheckPlan.  It
// contains derived facts, not decoded bytes.
type Audit struct {
	Requests       []Request
	Sources        [][]*Piece
	RequiredBlocks []BlockID
	DecodedBlocks  []BlockID

	DecodeBlocks uint64
	Hits         uint64
	Evictions    uint64
	ReturnBytes  uint64

	InitialCache    []BlockID
	FinalCache      []BlockID
	CheckpointStale bool
}

// CheckPlan re-verifies a compact plan using the plan's recorded limits.  It
// checks every source segment against a fresh resolution of the manifest,
// re-checks authorization, replays cache insertions/evictions, and recomputes
// both resource accounts.  No decoder callback is invoked.
func CheckPlan(m *Manifest, g Grant, batch []Request, p *Plan) (*Audit, error) {
	if p == nil {
		return nil, planErr(ReasonInvalidPlan, "check", "nil plan", nil)
	}
	return CheckPlanWithLimits(m, g, batch, p, p.Limits)
}

// CheckPlanWithLimits is CheckPlan with caller-supplied ceilings.
func CheckPlanWithLimits(m *Manifest, g Grant, batch []Request, p *Plan, limits Limits) (*Audit, error) {
	if m == nil {
		return nil, planErr(ReasonInsufficientEvidence, "check", "nil manifest", nil)
	}
	if err := m.Validate(); err != nil {
		return nil, planErr(ReasonInsufficientEvidence, "check", "manifest does not verify", err)
	}
	if err := g.Validate(); err != nil {
		return nil, planErr(ReasonInvalidInput, "check", "invalid grant", err)
	}
	grant := g.Normalized()
	if p == nil {
		return nil, planErr(ReasonInvalidPlan, "check", "nil plan", nil)
	}
	if !p.ManifestFingerprint.Equal(m.Fingerprint()) {
		return nil, planErr(ReasonInvalidPlan, "check", "plan was built for a different manifest", nil)
	}
	if p.Version != m.Version {
		return nil, planErr(ReasonInvalidPlan, "check", "plan version does not match manifest", nil)
	}
	if p.Tenant != grant.Tenant {
		return nil, planErr(ReasonInvalidPlan, "check", "plan tenant does not match", nil)
	}
	// A different grant digest is not fatal: the caller-supplied grant is the
	// authority for this check, and every request and every decoded block is
	// re-checked against it below.
	if len(batch) != len(p.Requests) {
		return nil, planErr(ReasonInvalidPlan, "check", "batch size does not match plan", nil)
	}
	for i := range batch {
		if batch[i] != p.Requests[i] {
			return nil, planErr(ReasonInvalidPlan, "check", fmt.Sprintf("request %d differs from plan", i), nil)
		}
		req := batch[i]
		end, ok := req.End()
		if !ok || end > m.TotalLength {
			return nil, planErr(ReasonInvalidInput, "check", fmt.Sprintf("request %d out of range", i), nil)
		}
		if !grant.Covers(req.Start, end) {
			return nil, planErr(ReasonUnauthorized, "check", fmt.Sprintf("request %d is outside grant", i), nil)
		}
	}

	// Re-resolve from the manifest and compare every source segment structure.
	sources := make([][]*Piece, len(batch))
	needs := make([][]BlockID, len(batch))
	blockDefs := make(map[BlockID]Block, len(m.Blocks))
	for _, b := range m.Blocks {
		blockDefs[b.ID] = b
	}
	allowed := make(map[BlockID]bool)
	for i, req := range batch {
		parts, err := Resolve(m, req.Start, req.Start+req.Length)
		if err != nil {
			return nil, planErr(ReasonInsufficientEvidence, "check", fmt.Sprintf("cannot resolve request %d", i), err)
		}
		if len(parts) > 0 {
			if err := checkPieces(parts, req.Length, ""); err != nil {
				return nil, planErr(ReasonLeak, "check", fmt.Sprintf("request %d source does not exactly cover its interval", i), err)
			}
		}
		if err := checkLeafBounds(parts, blockDefs); err != nil {
			return nil, planErr(ReasonLeak, "check", fmt.Sprintf("request %d source crosses block bounds", i), err)
		}
		if i < len(p.Sources) && !EqualPieces(parts, p.Sources[i]) {
			return nil, planErr(ReasonInvalidPlan, "check", fmt.Sprintf("request %d source segments were altered", i), nil)
		}
		sources[i] = parts
		needs[i] = SourceBlocks(parts)
		for _, id := range needs[i] {
			allowed[id] = true
		}
	}
	if len(p.Sources) != len(batch) {
		return nil, planErr(ReasonInvalidPlan, "check", "source list size does not match batch", nil)
	}

	derivedRequired := make([]BlockID, 0, len(allowed))
	for id := range allowed {
		derivedRequired = append(derivedRequired, id)
	}
	sort.Slice(derivedRequired, func(i, j int) bool { return derivedRequired[i] < derivedRequired[j] })
	if !sameBlockList(derivedRequired, p.RequiredBlocks) {
		return nil, planErr(ReasonInvalidPlan, "check", "required-block account was altered", nil)
	}
	returnBytes := unionLength(batch)
	if returnBytes != p.ReturnBytes {
		return nil, planErr(ReasonInvalidPlan, "check", "returned-data account was altered", nil)
	}
	if returnBytes > limits.ReturnBytes {
		return nil, planErr(ReasonReturnQuota, "check", fmt.Sprintf("returned-data account %d exceeds quota %d", returnBytes, limits.ReturnBytes), nil)
	}

	// Initial cache must be genuine current-version evidence if non-empty.
	if len(p.InitialCache) > 0 {
		if p.CheckpointStale {
			return nil, planErr(ReasonInvalidPlan, "check", "stale checkpoint pages appear in a plan", nil)
		}
		if p.CheckpointVersion != m.Version || !p.CheckpointDigest.Equal(m.StrategyDigest) {
			return nil, planErr(ReasonInsufficientEvidence, "check", "initial cache has the wrong version or strategy", nil)
		}
	}
	if uint64(len(p.InitialCache)) > limits.CacheCapacity {
		return nil, planErr(ReasonInvalidPlan, "check", "initial cache exceeds capacity", nil)
	}
	for _, id := range p.InitialCache {
		if _, ok := blockDefs[id]; !ok {
			return nil, planErr(ReasonInsufficientEvidence, "check", fmt.Sprintf("initial cache references unknown block %d", id), nil)
		}
	}

	// Replay the cache.  Victim choices are explicit so eviction is checkable.
	cache := newSimCache(p.InitialCache, limits.CacheCapacity)
	emitted := make([]bool, len(batch))
	audit := &Audit{
		Requests:        append([]Request(nil), batch...),
		Sources:         sources,
		RequiredBlocks:  derivedRequired,
		InitialCache:    append([]BlockID(nil), p.InitialCache...),
		CheckpointStale: p.CheckpointStale,
	}
	seenLoad := make(map[BlockID]bool)
	for si, st := range p.Steps {
		switch st.Kind {
		case StepLoad:
			if _, ok := blockDefs[st.Block]; !ok {
				return nil, planErr(ReasonInvalidPlan, "check", fmt.Sprintf("step %d loads unknown block %d", si, st.Block), nil)
			}
			if !allowed[st.Block] {
				return nil, planErr(ReasonUnauthorized, "check",
					fmt.Sprintf("step %d decodes block %d that is not justified by any authorized request", si, st.Block), nil)
			}
			if cache.has(st.Block) {
				if st.Evict {
					return nil, planErr(ReasonInvalidPlan, "check", fmt.Sprintf("step %d evicts on a cache hit", si), nil)
				}
				cache.touch(st.Block)
				audit.Hits++
				continue
			}
			audit.DecodeBlocks++
			seenLoad[st.Block] = true
			audit.DecodedBlocks = append(audit.DecodedBlocks, st.Block)
			if cache.full(limits.CacheCapacity) {
				if !st.Evict {
					return nil, planErr(ReasonInvalidPlan, "check", fmt.Sprintf("step %d misses with a full cache but no victim", si), nil)
				}
				if !cache.has(st.Victim) {
					return nil, planErr(ReasonInvalidPlan, "check", fmt.Sprintf("step %d evicts non-resident block %d", si, st.Victim), nil)
				}
				if st.Victim == st.Block {
					return nil, planErr(ReasonInvalidPlan, "check", fmt.Sprintf("step %d evicts the block it loads", si), nil)
				}
				cache.remove(st.Victim)
				audit.Evictions++
			} else if st.Evict {
				return nil, planErr(ReasonInvalidPlan, "check", fmt.Sprintf("step %d evicts without a full cache", si), nil)
			}
			cache.insert(st.Block)
			cache.touch(st.Block)

		case StepEmit:
			if st.Request < 0 || st.Request >= len(batch) {
				return nil, planErr(ReasonInvalidPlan, "check", fmt.Sprintf("step %d emits invalid request %d", si, st.Request), nil)
			}
			if emitted[st.Request] {
				return nil, planErr(ReasonInvalidPlan, "check", fmt.Sprintf("request %d emitted twice", st.Request), nil)
			}
			for _, id := range needs[st.Request] {
				if !cache.has(id) {
					return nil, planErr(ReasonInvalidPlan, "check",
						fmt.Sprintf("request %d emitted while block %d is not resident", st.Request, id), nil)
				}
				if !seenLoad[id] && !containsBlock(p.InitialCache, id) {
					return nil, planErr(ReasonInvalidPlan, "check",
						fmt.Sprintf("request %d uses block %d that was never loaded", st.Request, id), nil)
				}
			}
			emitted[st.Request] = true
			cache.touchMany(needs[st.Request])

		default:
			return nil, planErr(ReasonInvalidPlan, "check", fmt.Sprintf("step %d has unknown kind", si), nil)
		}
	}
	for i, ok := range emitted {
		if !ok {
			return nil, planErr(ReasonInvalidPlan, "check", fmt.Sprintf("request %d was never emitted", i), nil)
		}
	}
	if audit.DecodeBlocks != p.DecodeBlocks {
		return nil, planErr(ReasonInvalidPlan, "check",
			fmt.Sprintf("decode account %d != plan's %d", audit.DecodeBlocks, p.DecodeBlocks), nil)
	}
	if audit.DecodeBlocks > limits.DecodeBlocks {
		return nil, planErr(ReasonNoFeasiblePlan, "check",
			fmt.Sprintf("decode account %d exceeds quota %d", audit.DecodeBlocks, limits.DecodeBlocks), nil)
	}
	audit.ReturnBytes = returnBytes
	audit.FinalCache = append([]BlockID(nil), cache.order...)
	return audit, nil
}

func (c *simCache) touchMany(ids []BlockID) {
	for _, id := range ids {
		if c.has(id) {
			c.touch(id)
		}
	}
}

func checkLeafBounds(parts []*Piece, blocks map[BlockID]Block) error {
	for _, p := range parts {
		if p == nil {
			return fmt.Errorf("%w: nil piece", ErrInvalidManifest)
		}
		if p.IsLeaf() {
			b, ok := blocks[p.Block]
			if !ok {
				return fmt.Errorf("%w: unknown block %d", ErrInvalidManifest, p.Block)
			}
			if p.Off > b.Size || p.NLen > b.Size-p.Off {
				return fmt.Errorf("%w: block %d range [%d,%d) beyond size %d", ErrInvalidManifest, p.Block, p.Off, p.Off+p.NLen, b.Size)
			}
			continue
		}
		if err := checkLeafBounds(p.Parts, blocks); err != nil {
			return err
		}
	}
	return nil
}

func sameBlockList(a, b []BlockID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
