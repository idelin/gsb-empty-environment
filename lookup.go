package netsec

import (
	"fmt"
	"strings"
)

// NoIndex is returned when a lookup does not find a symbol.
const NoIndex uint32 = ^uint32(0)

// GNUChainStep records one symbol visited while walking a GNU chain.
type GNUChainStep struct {
	Index      uint32
	ChainIndex uint32
	Chain      uint32
	HashMatch  bool
	NameMatch  bool
}

// GNUPath is the complete path taken by a GNU lookup. It is intentionally
// verbose: reviewers can see the Bloom decision, the bucket, and every chain
// word that was examined.
type GNUPath struct {
	Hash       uint32
	BloomWord  uint32
	BloomBit0  uint32
	BloomBit1  uint32
	BloomMask  uint64
	BloomHit   bool
	Bucket     uint32
	Start      uint32
	Steps      []GNUChainStep
	MatchIndex uint32
	Found      bool
	Terminated bool
}

func (p *GNUPath) String() string {
	if p == nil {
		return "gnu=<none>"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "gnu(hash=0x%08x bloom[word=%d bits=%d,%d mask=0x%016x hit=%t]",
		p.Hash, p.BloomWord, p.BloomBit0, p.BloomBit1, p.BloomMask, p.BloomHit)
	if !p.BloomHit {
		b.WriteString(" -> not-found")
		return b.String()
	}
	fmt.Fprintf(&b, " bucket=%d start=%d", p.Bucket, p.Start)
	for _, step := range p.Steps {
		fmt.Fprintf(&b, " -> #%d(chain[%d]=0x%08x", step.Index, step.ChainIndex, step.Chain)
		if step.HashMatch {
			b.WriteString(",hash-match")
		}
		if step.NameMatch {
			b.WriteString(",name-match")
		}
		b.WriteByte(')')
	}
	switch {
	case p.Found:
		fmt.Fprintf(&b, " => #%d", p.MatchIndex)
	case p.Terminated:
		b.WriteString(" => not-found")
	default:
		b.WriteString(" => stopped")
	}
	b.WriteByte(')')
	return b.String()
}

// SysVStep records one symbol visited while walking a System V chain.
type SysVStep struct {
	Index     uint32
	Next      uint32
	NameMatch bool
}

// SysVPath is the complete path taken by a System V lookup.
type SysVPath struct {
	Hash       uint32
	Bucket     uint32
	Start      uint32
	Steps      []SysVStep
	MatchIndex uint32
	Found      bool
	Terminated bool
}

func (p *SysVPath) String() string {
	if p == nil {
		return "sysv=<none>"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "sysv(hash=0x%08x bucket=%d start=%d", p.Hash, p.Bucket, p.Start)
	for _, step := range p.Steps {
		fmt.Fprintf(&b, " -> #%d(next=%d", step.Index, step.Next)
		if step.NameMatch {
			b.WriteString(",name-match")
		}
		b.WriteByte(')')
	}
	switch {
	case p.Found:
		fmt.Fprintf(&b, " => #%d", p.MatchIndex)
	case p.Terminated:
		b.WriteString(" => not-found")
	default:
		b.WriteString(" => stopped")
	}
	b.WriteByte(')')
	return b.String()
}

// LookupResult is the result of a GNU or System V lookup. Exactly one of GNU
// and SysV is populated. Err reports malformed tables, out-of-range indexes,
// chain cycles, and work-budget exhaustion; it does not mean "not found".
type LookupResult struct {
	Index uint32
	Found bool
	Err   error
	GNU   *GNUPath
	SysV  *SysVPath
}

func (r LookupResult) String() string {
	if r.Err != nil {
		return fmt.Sprintf("error: %v", r.Err)
	}
	if r.SysV != nil {
		return r.SysV.String()
	}
	return r.GNU.String()
}

// Lookup finds name through the GNU hash table.
func (t *GNUTable) Lookup(names []string, name string) LookupResult {
	result := LookupResult{Index: NoIndex}
	path := &GNUPath{Hash: GNUHash(name), MatchIndex: NoIndex}
	result.GNU = path

	if err := t.validate(len(names)); err != nil {
		result.Err = err
		return result
	}

	wordCount := uint32(len(t.Bloom))
	path.BloomWord = (path.Hash / 64) % wordCount
	path.BloomBit0 = path.Hash % 64
	path.BloomBit1 = (path.Hash >> t.BloomShift) % 64
	path.BloomMask = uint64(1)<<path.BloomBit0 | uint64(1)<<path.BloomBit1
	path.BloomHit = t.Bloom[path.BloomWord]&path.BloomMask == path.BloomMask
	if !path.BloomHit {
		return result
	}

	path.Bucket = path.Hash % t.NBuckets
	path.Start = t.Buckets[path.Bucket]
	if path.Start == 0 {
		path.Terminated = true
		return result
	}
	if path.Start < t.SymOffset {
		result.Err = fmt.Errorf("%w: GNU bucket %d starts at %d below symoffset %d",
			ErrBadIndex, path.Bucket, path.Start, t.SymOffset)
		return result
	}

	maxSteps := len(t.Chains) + 1
	for i := path.Start; ; i++ {
		if uint64(i) >= uint64(len(names)) {
			result.Err = fmt.Errorf("%w: GNU chain reaches symbol %d with only %d names",
				ErrBadIndex, i, len(names))
			return result
		}
		chainIndex := i - t.SymOffset
		if uint64(chainIndex) >= uint64(len(t.Chains)) {
			result.Err = fmt.Errorf("%w: GNU chain index %d is outside chains length %d",
				ErrBadIndex, chainIndex, len(t.Chains))
			return result
		}

		chain := t.Chains[chainIndex]
		hashMatch := chain&^1 == path.Hash&^1
		nameMatch := hashMatch && names[i] == name
		path.Steps = append(path.Steps, GNUChainStep{
			Index:      i,
			ChainIndex: chainIndex,
			Chain:      chain,
			HashMatch:  hashMatch,
			NameMatch:  nameMatch,
		})
		if nameMatch {
			result.Index = i
			result.Found = true
			path.MatchIndex = i
			path.Found = true
			return result
		}
		if chain&1 != 0 {
			path.Terminated = true
			return result
		}
		if len(path.Steps) > maxSteps {
			result.Err = fmt.Errorf("%w: GNU lookup exceeded %d steps", ErrWorkBudget, maxSteps)
			return result
		}
		if i == ^uint32(0) {
			result.Err = fmt.Errorf("%w: GNU symbol index overflow", ErrBadIndex)
			return result
		}
	}
}

// Lookup finds name through the System V hash table.
func (t *SysVTable) Lookup(names []string, name string) LookupResult {
	result := LookupResult{Index: NoIndex}
	path := &SysVPath{Hash: SysVHash(name), MatchIndex: NoIndex}
	result.SysV = path

	if err := t.validate(len(names)); err != nil {
		result.Err = err
		return result
	}

	path.Bucket = path.Hash % uint32(len(t.Buckets))
	path.Start = t.Buckets[path.Bucket]
	if path.Start == 0 {
		path.Terminated = true
		return result
	}

	seen := make(map[uint32]struct{})
	maxSteps := len(t.Next) + 1
	for i := path.Start; ; {
		if uint64(i) >= uint64(len(names)) {
			result.Err = fmt.Errorf("%w: System V chain reaches symbol %d with only %d names",
				ErrBadIndex, i, len(names))
			return result
		}
		if uint64(i) >= uint64(len(t.Next)) {
			result.Err = fmt.Errorf("%w: System V next index %d is outside next length %d",
				ErrBadIndex, i, len(t.Next))
			return result
		}
		if _, ok := seen[i]; ok {
			result.Err = fmt.Errorf("%w: System V chain revisits symbol %d", ErrChainCycle, i)
			return result
		}
		seen[i] = struct{}{}

		next := t.Next[i]
		nameMatch := names[i] == name
		path.Steps = append(path.Steps, SysVStep{Index: i, Next: next, NameMatch: nameMatch})
		if nameMatch {
			result.Index = i
			result.Found = true
			path.MatchIndex = i
			path.Found = true
			return result
		}
		if next == 0 {
			path.Terminated = true
			return result
		}
		if len(path.Steps) > maxSteps {
			result.Err = fmt.Errorf("%w: System V lookup exceeded %d steps", ErrWorkBudget, maxSteps)
			return result
		}
		i = next
	}
}

// Find is a compact GNU lookup wrapper.
func (t *GNUTable) Find(names []string, name string) (uint32, bool) {
	result := t.Lookup(names, name)
	return result.Index, result.Found && result.Err == nil
}

// Find is a compact System V lookup wrapper.
func (t *SysVTable) Find(names []string, name string) (uint32, bool) {
	result := t.Lookup(names, name)
	return result.Index, result.Found && result.Err == nil
}

// LinearLookup performs an exact byte-for-byte linear name lookup over
// nonempty symbols. Symbol 0 is the empty sentinel and is never returned.
func LinearLookup(names []string, name string) (uint32, bool) {
	if name == "" {
		return NoIndex, false
	}
	for i := 1; i < len(names); i++ {
		candidate := names[i]
		if candidate == name {
			return uint32(i), true
		}
	}
	return NoIndex, false
}
