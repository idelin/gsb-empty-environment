package capmig

import (
	"errors"
	"fmt"
)

// MaxAmbiguousHandoffs bounds the Boolean history enumeration at 2^18.
const MaxAmbiguousHandoffs = 18

type index struct {
	subjects map[string]int
	physical map[string]int

	// alias -> physical; physical names also resolve to themselves.
	aliasToPhysical map[string]string

	// oldPhysical -> reachable newPhysical (reflexive-transitive closure).
	mapEdges   map[string]map[string]bool
	mapReach   map[string]map[string]bool
	objectPath map[[2]string][]string

	// coarseAction -> expansion closure, with a path per reachable pair.
	actionReach map[string]map[string]bool
	actionPath  map[[2]string][]string

	grants      map[string]int
	revocations map[string]int

	handoffs       []int
	ambiguousHops  []int
	handoffByID    map[string]int
	ambiguousIndex map[string]int
}

func buildIndex(in *Input) (*index, error) {
	idx := &index{
		subjects:        map[string]int{},
		physical:        map[string]int{},
		aliasToPhysical: map[string]string{},
		mapEdges:        map[string]map[string]bool{},
		mapReach:        map[string]map[string]bool{},
		objectPath:      map[[2]string][]string{},
		actionReach:     map[string]map[string]bool{},
		actionPath:      map[[2]string][]string{},
		grants:          map[string]int{},
		revocations:     map[string]int{},
		handoffByID:     map[string]int{},
		ambiguousIndex:  map[string]int{},
	}

	addSubject := func(s string) {
		if s == "" {
			return
		}
		if _, ok := idx.subjects[s]; !ok {
			idx.subjects[s] = len(idx.subjects)
		}
	}
	addPhysical := func(p string) {
		if p == "" {
			return
		}
		if _, ok := idx.physical[p]; !ok {
			idx.physical[p] = len(idx.physical)
		}
	}

	// --- Alias bindings -----------------------------------------------------
	for _, b := range in.AliasBindings {
		if b.Alias == "" || b.PhysicalID == "" {
			return nil, errors.New("alias binding has empty alias or physical id")
		}
		if existing, ok := idx.aliasToPhysical[b.Alias]; ok && existing != b.PhysicalID {
			return nil, fmt.Errorf("alias %q binds to conflicting physical objects %q and %q",
				b.Alias, existing, b.PhysicalID)
		}
		idx.aliasToPhysical[b.Alias] = b.PhysicalID
		addPhysical(b.PhysicalID)
	}

	resolve := func(name string) (string, bool) {
		if name == "" {
			return "", false
		}
		if p, ok := idx.aliasToPhysical[name]; ok {
			return p, true
		}
		if _, ok := idx.physical[name]; ok {
			return name, true
		}
		return "", false
	}

	// --- Resource mapping edges --------------------------------------------
	for _, e := range in.ResourceMaps {
		oldP, ok := resolve(e.OldObject)
		if !ok {
			addPhysical(e.OldObject)
			oldP = e.OldObject
		}
		newP, ok := resolve(e.NewObject)
		if !ok {
			addPhysical(e.NewObject)
			newP = e.NewObject
		}
		if oldP == newP {
			continue
		}
		if idx.mapEdges[oldP] == nil {
			idx.mapEdges[oldP] = map[string]bool{}
		}
		idx.mapEdges[oldP][newP] = true
	}

	// --- Action splits ------------------------------------------------------
	type split struct {
		old  string
		news []string
	}
	splits := make([]split, 0, len(in.ActionSplits))
	actionSeen := map[string]bool{}
	addAction := func(a string) {
		if a == "" || actionSeen[a] {
			return
		}
		actionSeen[a] = true
		idx.actionReach[a] = map[string]bool{a: true}
		idx.actionPath[[2]string{a, a}] = []string{a}
	}
	for _, s := range in.ActionSplits {
		if s.OldAction == "" || len(s.NewActions) == 0 {
			return nil, errors.New("action split has empty old action or no new actions")
		}
		addAction(s.OldAction)
		seen := map[string]bool{}
		for _, a := range s.NewActions {
			if a == "" {
				return nil, fmt.Errorf("action split of %q contains empty action", s.OldAction)
			}
			if a == s.OldAction {
				return nil, fmt.Errorf("action split of %q is self-referential", s.OldAction)
			}
			if seen[a] {
				return nil, fmt.Errorf("action split of %q lists %q twice", s.OldAction, a)
			}
			seen[a] = true
			addAction(a)
		}
		splits = append(splits, split{s.OldAction, append([]string(nil), s.NewActions...)})
	}
	for _, s := range splits {
		for _, a := range s.news {
			idx.actionReach[s.old][a] = true
			idx.actionPath[[2]string{s.old, a}] = []string{s.old, a}
		}
	}

	// --- Grants -------------------------------------------------------------
	for gi, g := range in.Grants {
		if g.Token == "" || g.Subject == "" || g.Object == "" || g.Action == "" {
			return nil, fmt.Errorf("grant %d has an empty token/subject/object/action field", gi)
		}
		if existing, ok := idx.grants[g.Token]; ok {
			return nil, fmt.Errorf("duplicate grant for token %q (grants %d and %d)", g.Token, existing, gi)
		}
		idx.grants[g.Token] = gi
		addSubject(g.Subject)
		addPhysical(g.Object)
		if _, ok := idx.aliasToPhysical[g.Object]; !ok {
			idx.aliasToPhysical[g.Object] = g.Object
		}
		addAction(g.Action)
	}

	// --- Revocations --------------------------------------------------------
	for ri, r := range in.Revocations {
		if r.Token == "" {
			return nil, fmt.Errorf("revocation %d has empty token", ri)
		}
		if _, ok := idx.grants[r.Token]; !ok {
			return nil, fmt.Errorf("revocation for unknown token %q", r.Token)
		}
		if existing, ok := idx.revocations[r.Token]; ok {
			return nil, fmt.Errorf("duplicate revocation for token %q (records %d and %d)",
				r.Token, existing, ri)
		}
		idx.revocations[r.Token] = ri
	}

	// --- Handoffs -----------------------------------------------------------
	ambCount := 0
	for hi, h := range in.Handoffs {
		if h.Token == "" || h.From == "" || h.To == "" {
			return nil, fmt.Errorf("handoff %d has an empty token/from/to field", hi)
		}
		if h.From == h.To {
			return nil, fmt.Errorf("handoff %d is self-transfer for subject %q", hi, h.From)
		}
		id := h.ID
		if id == "" {
			id = fmt.Sprintf("hop-%d", hi)
		}
		if existing, ok := idx.handoffByID[id]; ok {
			return nil, fmt.Errorf("duplicate handoff id %q (hops %d and %d)", id, existing, hi)
		}
		idx.handoffByID[id] = hi
		idx.handoffs = append(idx.handoffs, hi)
		if _, ok := idx.grants[h.Token]; !ok {
			return nil, fmt.Errorf("handoff %q references unknown token %q", id, h.Token)
		}
		addSubject(h.From)
		addSubject(h.To)
		if h.LostConfirmation {
			if ambCount >= MaxAmbiguousHandoffs {
				return nil, fmt.Errorf("more than %d confirmation-lost handoffs", MaxAmbiguousHandoffs)
			}
			idx.ambiguousIndex[id] = ambCount
			idx.ambiguousHops = append(idx.ambiguousHops, hi)
			ambCount++
		}
	}

	// --- Tickets ------------------------------------------------------------
	ticketIDs := map[string]bool{}
	for ti, t := range in.Tickets {
		if t.ID == "" || t.Subject == "" || t.NewObject == "" || len(t.NewActions) == 0 {
			return nil, fmt.Errorf("ticket %d has an empty id/subject/object/actions field", ti)
		}
		if ticketIDs[t.ID] {
			return nil, fmt.Errorf("duplicate ticket id %q", t.ID)
		}
		ticketIDs[t.ID] = true
		addSubject(t.Subject)
		phys, ok := resolve(t.NewObject)
		if !ok {
			return nil, fmt.Errorf("ticket %q targets unresolvable object %q", t.ID, t.NewObject)
		}
		addPhysical(phys)
		actSeen := map[string]bool{}
		for _, a := range t.NewActions {
			if a == "" {
				return nil, fmt.Errorf("ticket %q contains empty action", t.ID)
			}
			if actSeen[a] {
				return nil, fmt.Errorf("ticket %q lists action %q twice", t.ID, a)
			}
			actSeen[a] = true
			if !actionSeen[a] {
				return nil, fmt.Errorf("ticket %q requests unknown action %q", t.ID, a)
			}
		}
		if len(t.AncestorTokens) == 0 {
			return nil, fmt.Errorf("ticket %q has no ancestor tokens", t.ID)
		}
		ancSeen := map[string]bool{}
		for _, tok := range t.AncestorTokens {
			if ancSeen[tok] {
				return nil, fmt.Errorf("ticket %q lists ancestor token %q twice", t.ID, tok)
			}
			ancSeen[tok] = true
			if _, ok := idx.grants[tok]; !ok {
				return nil, fmt.Errorf("ticket %q references unknown ancestor token %q", t.ID, tok)
			}
		}
		for _, hid := range t.DependencyHandoffs {
			hi, ok := idx.handoffByID[hid]
			if !ok {
				return nil, fmt.Errorf("ticket %q depends on unknown handoff %q", t.ID, hid)
			}
			if !ancSeen[in.Handoffs[hi].Token] {
				return nil, fmt.Errorf("ticket %q dependency %q touches token outside its ancestors", t.ID, hid)
			}
		}
	}

	idx.buildObjectClosure()
	if err := idx.buildActionClosure(); err != nil {
		return nil, err
	}
	return idx, nil
}

func (idx *index) buildObjectClosure() {
	nodes := map[string]bool{}
	for u, vs := range idx.mapEdges {
		nodes[u] = true
		for v := range vs {
			nodes[v] = true
		}
	}
	for start := range nodes {
		reach := map[string]bool{start: true}
		parent := map[string]string{start: ""}
		queue := []string{start}
		for len(queue) > 0 {
			u := queue[0]
			queue = queue[1:]
			for v := range idx.mapEdges[u] {
				if !reach[v] {
					reach[v] = true
					parent[v] = u
					queue = append(queue, v)
				}
			}
		}
		idx.mapReach[start] = reach
		for target := range reach {
			if target == start {
				idx.objectPath[[2]string{start, target}] = []string{start}
				continue
			}
			path := []string{}
			for cur := target; cur != ""; cur = parent[cur] {
				path = append([]string{cur}, path...)
			}
			idx.objectPath[[2]string{start, target}] = path
		}
	}
}

func (idx *index) buildActionClosure() error {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var visit func(a string) error
	visit = func(a string) error {
		switch color[a] {
		case gray:
			return fmt.Errorf("action split graph has a cycle at %q", a)
		case black:
			return nil
		}
		color[a] = gray
		for child := range idx.actionReach[a] {
			if child == a {
				continue
			}
			if err := visit(child); err != nil {
				return err
			}
			for grand := range idx.actionReach[child] {
				if !idx.actionReach[a][grand] {
					idx.actionReach[a][grand] = true
					idx.actionPath[[2]string{a, grand}] = append(
						append([]string(nil), idx.actionPath[[2]string{a, child}]...), grand)
				}
			}
		}
		color[a] = black
		return nil
	}
	for a := range idx.actionReach {
		if err := visit(a); err != nil {
			return err
		}
	}
	return nil
}

func (idx *index) resolveObject(name string) string {
	if p, ok := idx.aliasToPhysical[name]; ok {
		return p
	}
	return name
}

func (idx *index) objectReachable(oldPhysical, newPhysical string) ([]string, bool) {
	if oldPhysical == newPhysical {
		return []string{oldPhysical}, true
	}
	reach, ok := idx.mapReach[oldPhysical]
	if !ok || !reach[newPhysical] {
		return nil, false
	}
	path, ok := idx.objectPath[[2]string{oldPhysical, newPhysical}]
	return append([]string(nil), path...), ok
}

func (idx *index) actionCovers(grantedAction, requiredAction string) ([]string, bool) {
	if grantedAction == requiredAction {
		return []string{grantedAction}, true
	}
	reach, ok := idx.actionReach[grantedAction]
	if !ok || !reach[requiredAction] {
		return nil, false
	}
	path, ok := idx.actionPath[[2]string{grantedAction, requiredAction}]
	return append([]string(nil), path...), ok
}
