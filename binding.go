package vex

import (
	"fmt"
	"sort"
)

type groupPlan struct {
	id      string
	options []ObjectID
}

type bindingState struct {
	groups  map[string]ObjectID
	symbols map[string]symbolDef
}

func (c *configEvaluator) bindAndReach() {
	defs := make(map[string][]symbolDef)
	for _, obj := range c.ordered {
		for _, def := range obj.defs {
			defs[def.name] = append(defs[def.name], def)
		}
	}

	refs := c.allRefs()
	activeRefs := make([]Reference, 0, len(refs))
	needed := make(map[string]bool)
	for _, ref := range refs {
		active, err := c.refActive("reference "+ref.Symbol, ref)
		if err != nil {
			c.issue("condition", err.Error())
			continue
		}
		if active {
			activeRefs = append(activeRefs, ref)
			needed[ref.Symbol] = true
		}
	}

	symbols := make([]string, 0, len(needed))
	for name := range needed {
		symbols = append(symbols, name)
	}
	sort.Strings(symbols)

	groups := make([]groupPlan, 0)
	groupObjects := make(map[string]map[string]ObjectID)
	for _, name := range symbols {
		for _, def := range defs[name] {
			if def.kind != BindingGroup {
				continue
			}
			if groupObjects[def.group] == nil {
				groupObjects[def.group] = make(map[string]ObjectID)
			}
			groupObjects[def.group][idKey(def.object)] = def.object
		}
	}
	for group, objects := range groupObjects {
		options := make([]ObjectID, 0, len(objects))
		for _, object := range objects {
			options = append(options, object)
		}
		sortIDs(options)
		groups = append(groups, groupPlan{id: group, options: options})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].id < groups[j].id })

	for _, name := range symbols {
		strong := uniqueDefs(defs[name], BindingStrong)
		if len(strong) > 1 {
			c.proof.Notes = append(c.proof.Notes, "multiple strong definitions for "+name)
			c.finish(ConfigNotLinkable, false, "duplicate strong definitions make the configuration unlinkable")
			return
		}
		gids := make(map[string]bool)
		for _, def := range defs[name] {
			if def.kind == BindingGroup {
				gids[def.group] = true
			}
		}
		if len(gids) > 1 {
			c.proof.Notes = append(c.proof.Notes, "symbol "+name+" belongs to multiple choose-one groups")
			c.finish(ConfigNotLinkable, false, "ambiguous choose-one groups make the configuration unlinkable")
			return
		}
	}

	var anyLinkable, anyAffected bool
	exceeded := false
	state := bindingState{
		groups:  make(map[string]ObjectID),
		symbols: make(map[string]symbolDef),
	}
	enumerated := 0

	var recSymbols func(int)
	var recGroups func(int)

	commit := func() {
		enumerated++
		if enumerated > c.a.maxStates {
			exceeded = true
			return
		}
		set := c.evaluateBinding(activeRefs, state, enumerated)
		c.proof.Bindings = append(c.proof.Bindings, set)
		if set.Linkable {
			anyLinkable = true
			if len(set.MarkedReachable) > 0 {
				anyAffected = true
			}
		}
	}

	recSymbols = func(index int) {
		if exceeded {
			return
		}
		if index == len(symbols) {
			commit()
			return
		}
		name := symbols[index]
		options := c.symbolOptions(name, defs[name], state)
		if len(options) == 0 {
			recSymbols(index + 1)
			return
		}
		for _, option := range options {
			state.symbols[name] = option
			recSymbols(index + 1)
			delete(state.symbols, name)
			if exceeded {
				return
			}
		}
	}

	recGroups = func(index int) {
		if exceeded {
			return
		}
		if index == len(groups) {
			recSymbols(0)
			return
		}
		group := groups[index]
		for _, option := range group.options {
			state.groups[group.id] = option
			recGroups(index + 1)
			delete(state.groups, group.id)
			if exceeded {
				return
			}
		}
	}

	recGroups(0)
	c.proof.Enumerated = enumerated
	if exceeded {
		c.issue("enumeration", fmt.Sprintf("binding enumeration exceeded %d states", c.a.maxStates))
		if anyAffected && len(c.issues) == 1 {
			// An affected witness is existential; an enumeration cap cannot
			// erase a complete witness that was already checked.
			c.finish(ConfigAffected, true, "a legal binding reaches a marked object identity")
			return
		}
		c.finish(ConfigUnknown, anyLinkable, "binding enumeration limit reached")
		return
	}
	if len(c.issues) > 0 {
		c.finish(ConfigUnknown, anyLinkable, "signed evidence is incomplete or inconsistent")
		return
	}
	if !anyLinkable {
		if c.expectLinkable {
			c.issue("linkability", "signed evidence declares the configuration linkable but no legal linking exists")
			c.finish(ConfigUnknown, false, "linkability declaration contradicts the link evidence")
			return
		}
		c.finish(ConfigNotLinkable, false, "no legal binding produces a linkable image")
		return
	}
	if anyAffected {
		c.finish(ConfigAffected, true, "a legal binding reaches a marked object identity")
		return
	}
	c.finish(ConfigNotAffected, true, "all legal bindings are entry-unreachable from marked objects")
}

func uniqueDefs(defs []symbolDef, kind BindingKind) []symbolDef {
	seen := make(map[string]bool)
	out := make([]symbolDef, 0)
	for _, def := range defs {
		if def.kind != kind {
			continue
		}
		key := idKey(def.object)
		if !seen[key] {
			seen[key] = true
			out = append(out, def)
		}
	}
	sort.Slice(out, func(i, j int) bool { return idKey(out[i].object) < idKey(out[j].object) })
	return out
}

func (c *configEvaluator) symbolOptions(name string, defs []symbolDef, state bindingState) []symbolDef {
	strong := uniqueDefs(defs, BindingStrong)
	if len(strong) > 0 {
		return strong[:1]
	}
	var groups []symbolDef
	groups = append(groups, uniqueDefs(defs, BindingGroup)...)
	if len(groups) > 0 {
		group := groups[0].group
		chosen, ok := state.groups[group]
		if !ok {
			return nil
		}
		for _, def := range groups {
			if idKey(def.object) == idKey(chosen) {
				return []symbolDef{def}
			}
		}
		return nil
	}
	weak := uniqueDefs(defs, BindingWeak)
	if len(weak) > 0 {
		return weak
	}
	return nil
}

func (c *configEvaluator) evaluateBinding(activeRefs []Reference, state bindingState, index int) BindingSet {
	set := BindingSet{Index: index, Linkable: true}
	groupNames := make([]string, 0, len(state.groups))
	for name := range state.groups {
		groupNames = append(groupNames, name)
	}
	sort.Strings(groupNames)
	for _, name := range groupNames {
		set.Groups = append(set.Groups, GroupSelection{Group: name, Object: state.groups[name]})
	}
	for _, name := range sortedSymbolNames(state.symbols) {
		def := state.symbols[name]
		set.Bindings = append(set.Bindings, BindingChoice{
			Symbol: name,
			Object: def.object,
			Kind:   def.kind,
		})
	}

	for _, ref := range activeRefs {
		if _, ok := state.symbols[ref.Symbol]; !ok && !ref.Optional {
			set.Linkable = false
			if set.Failure == "" {
				set.Failure = "unresolved reference " + ref.Symbol
			}
		}
	}
	if !set.Linkable {
		return set
	}

	reachable := make(map[string]ObjectID)
	marked := make(map[string]ObjectID)
	processedObjects := make(map[string]bool)
	processedSymbols := make(map[string]bool)
	queue := append([]Reference(nil), c.entries...)

	enqueue := func(ref Reference) bool {
		active, err := c.refActive("reachability "+ref.Symbol, ref)
		if err != nil {
			c.issue("condition", err.Error())
			return false
		}
		if !active {
			return true
		}
		def, ok := state.symbols[ref.Symbol]
		if !ok {
			if ref.Optional {
				return true
			}
			set.Linkable = false
			if set.Failure == "" {
				set.Failure = "unresolved reference " + ref.Symbol
			}
			return false
		}
		objectKey := idKey(def.object)
		reachable[objectKey] = def.object
		if c.a.isMarked(def.object) {
			marked[objectKey] = def.object
		}
		if !processedSymbols[ref.Symbol+"\x00"+objectKey] {
			processedSymbols[ref.Symbol+"\x00"+objectKey] = true
			queue = append(queue, def.refs...)
		}
		if !processedObjects[objectKey] {
			processedObjects[objectKey] = true
			if obj, ok := c.extract[objectKey]; ok {
				queue = append(queue, obj.refs...)
			}
		}
		return true
	}

	for len(queue) > 0 && set.Linkable {
		ref := queue[0]
		queue = queue[1:]
		if !enqueue(ref) {
			set.Linkable = false
			if set.Failure == "" {
				set.Failure = "condition evaluation failed for " + ref.Symbol
			}
		}
	}
	for key, id := range reachable {
		_ = key
		set.Reachable = append(set.Reachable, id)
	}
	for key, id := range marked {
		_ = key
		set.MarkedReachable = append(set.MarkedReachable, id)
	}
	sortIDs(set.Reachable)
	sortIDs(set.MarkedReachable)
	return set
}

func sortedSymbolNames(symbols map[string]symbolDef) []string {
	names := make([]string, 0, len(symbols))
	for name := range symbols {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (a *analyzer) isMarked(id ObjectID) bool {
	key := idKey(id)
	for _, marked := range a.marked {
		if idKey(marked) == key {
			return true
		}
	}
	return false
}
