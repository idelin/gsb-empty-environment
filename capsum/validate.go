package capsum

import (
	"fmt"
	"sort"

	"prescreen.local/ps-2237843e01/pathlang"
)

// Validate checks structural well-formedness and builds the resolved index.
func Validate(p *Program) (*index, error) {
	if p == nil {
		return nil, fmt.Errorf("capsum: nil program")
	}
	if p.MaxHandles < 0 || p.MaxHandles > 4 {
		return nil, fmt.Errorf("capsum: max_handles must be in [0,4], got %d", p.MaxHandles)
	}
	x := &index{
		prog:        p,
		nodeSet:     map[string]struct{}{},
		domains:     map[string]*Domain{},
		roots:       map[string]*Root{},
		out:         map[string][]int{},
		in:          map[string][]int{},
		pairReturn:  map[int]int{},
		pairCall:    map[int]int{},
		taskReturn:  map[int]int{},
		taskConsume: map[int]int{},
		genIndex:    map[string]int{},
		domainGens:  map[string][]int{},
		handleIndex: map[string]int{},
	}

	for _, n := range p.Nodes {
		if n == "" {
			return nil, fmt.Errorf("capsum: empty node id")
		}
		if _, dup := x.nodeSet[n]; dup {
			return nil, fmt.Errorf("capsum: duplicate node %q", n)
		}
		x.nodeSet[n] = struct{}{}
	}
	if _, ok := x.nodeSet[p.EntryNode]; !ok {
		return nil, fmt.Errorf("capsum: entry node %q not in nodes", p.EntryNode)
	}

	rootNames := make([]string, 0, len(p.Roots))
	for i := range p.Roots {
		r := &p.Roots[i]
		if r.Name == "" {
			return nil, fmt.Errorf("capsum: root with empty name")
		}
		if _, dup := x.roots[r.Name]; dup {
			return nil, fmt.Errorf("capsum: duplicate root %q", r.Name)
		}
		x.roots[r.Name] = r
		rootNames = append(rootNames, r.Name)
	}
	for _, alias := range p.Roots {
		if alias.AliasOf != "" {
			if _, ok := x.roots[alias.AliasOf]; !ok {
				return nil, fmt.Errorf("capsum: alias %q points at unknown root %q", alias.Name, alias.AliasOf)
			}
		}
	}

	for i := range p.Domains {
		dm := &p.Domains[i]
		if dm.Name == "" {
			return nil, fmt.Errorf("capsum: domain with empty name")
		}
		if _, dup := x.domains[dm.Name]; dup {
			return nil, fmt.Errorf("capsum: duplicate domain %q", dm.Name)
		}
		x.domains[dm.Name] = dm
	}
	for _, d := range p.EntryDomains {
		if _, ok := x.domains[d]; !ok {
			return nil, fmt.Errorf("capsum: entry domain %q undefined", d)
		}
	}

	handleSet := map[string]struct{}{}
	edgeIDs := map[string]struct{}{}
	callPairs := map[string]int{}
	consumePairs := map[string]int{}
	compSet := map[string]struct{}{}

	for ei := range p.Edges {
		e := &p.Edges[ei]
		if e.ID == "" {
			return nil, fmt.Errorf("capsum: edge %d has empty id", ei)
		}
		if _, dup := edgeIDs[e.ID]; dup {
			return nil, fmt.Errorf("capsum: duplicate edge id %q", e.ID)
		}
		edgeIDs[e.ID] = struct{}{}
		if _, ok := x.nodeSet[e.From]; !ok {
			return nil, fmt.Errorf("capsum: edge %q from unknown node %q", e.ID, e.From)
		}
		if _, ok := x.nodeSet[e.To]; !ok {
			return nil, fmt.Errorf("capsum: edge %q to unknown node %q", e.ID, e.To)
		}
		switch e.Kind {
		case EdgeLocal:
			// Ordinary intra-procedural flow carries no extra metadata.
		case EdgeCall, EdgePriv:
			if len(e.Domains) == 0 {
				return nil, fmt.Errorf("capsum: %s edge %q needs domains", e.Kind, e.ID)
			}
			for _, d := range e.Domains {
				if _, ok := x.domains[d]; !ok {
					return nil, fmt.Errorf("capsum: edge %q references unknown domain %q", e.ID, d)
				}
			}
			if e.PairID == "" {
				return nil, fmt.Errorf("capsum: %s edge %q needs pair_id", e.Kind, e.ID)
			}
			if other, ok := callPairs[e.PairID]; ok {
				return nil, fmt.Errorf("capsum: pair_id %q reused by edges %q and %q", e.PairID, p.Edges[other].ID, e.ID)
			}
			callPairs[e.PairID] = ei
		case EdgeReturn:
			if e.PairID == "" {
				return nil, fmt.Errorf("capsum: return edge %q needs pair_id", e.ID)
			}
			other, ok := callPairs[e.PairID]
			if !ok {
				return nil, fmt.Errorf("capsum: return edge %q has unmatched pair_id %q", e.ID, e.PairID)
			}
			call := &p.Edges[other]
			if call.Kind != EdgeCall && call.Kind != EdgePriv {
				return nil, fmt.Errorf("capsum: return %q paired with non-call edge %q", e.ID, call.ID)
			}
			x.pairReturn[other] = ei
			x.pairCall[ei] = other
		case EdgePost, EdgeConsume:
			if e.Handle == "" {
				return nil, fmt.Errorf("capsum: %s edge %q needs handle", e.Kind, e.ID)
			}
			handleSet[e.Handle] = struct{}{}
			if e.Kind == EdgeConsume {
				if e.ConsumePairID == "" {
					return nil, fmt.Errorf("capsum: consume edge %q needs consume_pair_id", e.ID)
				}
				if other, ok := consumePairs[e.ConsumePairID]; ok {
					return nil, fmt.Errorf("capsum: consume_pair_id %q reused by %q and %q", e.ConsumePairID, p.Edges[other].ID, e.ID)
				}
				consumePairs[e.ConsumePairID] = ei
			}
		case EdgeTaskReturn:
			if e.ConsumePairID == "" {
				return nil, fmt.Errorf("capsum: task_return edge %q needs consume_pair_id", e.ID)
			}
			other, ok := consumePairs[e.ConsumePairID]
			if !ok {
				return nil, fmt.Errorf("capsum: task_return edge %q unmatched consume_pair_id %q", e.ID, e.ConsumePairID)
			}
			consume := &p.Edges[other]
			if consume.Kind != EdgeConsume {
				return nil, fmt.Errorf("capsum: task_return %q paired with non-consume edge %q", e.ID, consume.ID)
			}
			x.taskReturn[other] = ei
			x.taskConsume[ei] = other
		default:
			return nil, fmt.Errorf("capsum: edge %q has unknown kind %q", e.ID, e.Kind)
		}
		if e.Action != nil {
			if _, ok := x.roots[e.Action.Root]; !ok {
				return nil, fmt.Errorf("capsum: action on edge %q uses unknown root %q", e.ID, e.Action.Root)
			}
			if _, err := pathlang.Normalize(e.Action.Path); err != nil {
				return nil, fmt.Errorf("capsum: action on edge %q: %w", e.ID, err)
			}
			for _, c := range e.Action.Path {
				compSet[c] = struct{}{}
			}
		}
		x.out[e.From] = append(x.out[e.From], ei)
		x.in[e.To] = append(x.in[e.To], ei)
	}

	for callEI, retEI := range x.pairReturn {
		call := &p.Edges[callEI]
		ret := &p.Edges[retEI]
		// The return must leave the callee; its destination is the caller-side
		// return point, which may differ from the call node for recursive
		// graphs that return into distinct continuation sites.
		if ret.From != call.To {
			return nil, fmt.Errorf("capsum: return %q does not leave callee of %q", ret.ID, call.ID)
		}
	}
	for consumeEI, retEI := range x.taskReturn {
		consume := &p.Edges[consumeEI]
		ret := &p.Edges[retEI]
		if ret.From != consume.To {
			return nil, fmt.Errorf("capsum: task_return %q does not leave task of %q", ret.ID, consume.ID)
		}
	}
	for pair, callEI := range callPairs {
		if _, ok := x.pairReturn[callEI]; !ok {
			return nil, fmt.Errorf("capsum: call pair %q has no return edge", pair)
		}
	}
	for pair, consumeEI := range consumePairs {
		if _, ok := x.taskReturn[consumeEI]; !ok {
			return nil, fmt.Errorf("capsum: consume pair %q has no task_return edge", pair)
		}
	}

	handles := make([]string, 0, len(handleSet))
	for h := range handleSet {
		handles = append(handles, h)
	}
	sort.Strings(handles)
	if len(handles) > p.MaxHandles {
		return nil, fmt.Errorf("capsum: %d distinct handles exceed max_handles %d", len(handles), p.MaxHandles)
	}
	x.handles = handles
	for i, h := range handles {
		x.handleIndex[h] = i
	}

	for _, dm := range x.domains {
		for _, g := range dm.Grants {
			for _, c := range g.Components {
				compSet[c] = struct{}{}
			}
		}
	}
	comps := make([]string, 0, len(compSet))
	for c := range compSet {
		comps = append(comps, c)
	}
	sort.Strings(comps)
	x.componentAlphabet = comps
	x.alphabet = pathlang.BuildAlphabet(rootNames, comps)

	if err := x.buildGenerators(); err != nil {
		return nil, err
	}
	return x, nil
}

func (x *index) buildGenerators() error {
	domainNames := make([]string, 0, len(x.domains))
	for name := range x.domains {
		domainNames = append(domainNames, name)
	}
	sort.Strings(domainNames)
	for _, name := range domainNames {
		dm := x.domains[name]
		ids := make([]int, 0, len(dm.Grants))
		for _, g := range dm.Grants {
			if _, ok := x.roots[g.Root]; !ok {
				return fmt.Errorf("capsum: domain %q grants unknown root %q", name, g.Root)
			}
			if _, err := pathlang.Normalize(g.Components); err != nil {
				return fmt.Errorf("capsum: domain %q: %w", name, err)
			}
			key := generatorKey(g)
			id, ok := x.genIndex[key]
			if !ok {
				d, err := x.buildGenerator(g)
				if err != nil {
					return fmt.Errorf("capsum: domain %q: %w", name, err)
				}
				id = len(x.generators)
				x.genIndex[key] = id
				x.generators = append(x.generators, d)
			}
			ids = append(ids, id)
		}
		sort.Ints(ids)
		x.domainGens[name] = dedupInts(ids)
	}
	return nil
}
