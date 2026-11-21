package typeset

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type ref struct {
	allocID string
	site    string
	dynamic string
	static  string
	path    Path
	trace   []TraceStep
}

func (r ref) staticClass() string {
	if r.static != "" {
		return r.static
	}
	if len(r.path) != 0 {
		return r.path[len(r.path)-1].ClassName()
	}
	return r.dynamic
}

func (r ref) key() string {
	return r.allocID + "|" + r.dynamic + "|" + pathKey(r.path) + "|" + r.staticClass()
}

func (r ref) pathKey() string { return pathKey(r.path) }

func (r ref) withStatic(static string, path Path, trace ...TraceStep) ref {
	r.static = static
	r.path = path
	if len(trace) != 0 {
		r.trace = appendTrace(r.trace, trace...)
	}
	return r
}

type accessSlot struct {
	root   string
	static bool
	path   Path
}

func prependSlot(step Step, slot accessSlot) accessSlot {
	path := make(Path, 0, len(slot.path)+1)
	path = append(path, step)
	path = append(path, slot.path...)
	slot.path = path
	return slot
}

// methodAccess computes the declarations that participate in member lookup at
// a class.  Virtual slots keep their original virtual root, while static
// declarations are kept separate so that repeated non-virtual bases can still
// be distinguished by subobject identity.
func (g *graph) methodAccess(class, key string) ([]accessSlot, bool) {
	node := g.classes[class]
	if node == nil {
		return nil, false
	}
	if _, ok := node.methods[key]; ok {
		if !g.methodVirtual(class, key) {
			return []accessSlot{{root: class, static: true}}, true
		}
		var roots []accessSlot
		for _, base := range node.bases {
			child, found := g.methodAccess(base.class, key)
			if !found {
				continue
			}
			step := Step{Class: base.class, Name: base.class, Virtual: base.virtual, Index: base.index}
			for _, slot := range child {
				if slot.static {
					continue
				}
				roots = append(roots, prependSlot(step, slot))
			}
		}
		if len(roots) == 0 {
			return []accessSlot{{root: class}}, true
		}
		return roots, true
	}

	var slots []accessSlot
	for _, base := range node.bases {
		child, found := g.methodAccess(base.class, key)
		if !found {
			continue
		}
		step := Step{Class: base.class, Name: base.class, Virtual: base.virtual, Index: base.index}
		for _, slot := range child {
			slots = append(slots, prependSlot(step, slot))
		}
	}
	return slots, len(slots) != 0
}

func (g *graph) resolveAt(dynamic string, receiverPath Path, static, key string) ([]Target, []Ambiguity, bool, bool) {
	accesses, found := g.methodAccess(static, key)
	if !found {
		return nil, nil, false, false
	}

	type slotResult struct {
		identity string
		targets  []Target
		ambs     []Ambiguity
		static   bool
	}
	results := make([]slotResult, 0, len(accesses))
	seenSlots := make(map[string]int)
	for _, access := range accesses {
		full := appendPaths(receiverPath, access.path)
		identity := g.subobjectIdentity(dynamic, full)
		if access.static {
			identity = "static:" + identity
		} else if pathHasVirtual(full) {
			identity = g.subobjectIdentity(dynamic, full)
		} else {
			identity = "path:" + pathKey(full)
		}
		if index, ok := seenSlots[identity]; ok {
			results[index].static = results[index].static && access.static
			continue
		}
		seenSlots[identity] = len(results)

		result := slotResult{identity: identity, static: access.static}
		if access.static {
			target, ok := g.targetFor(access.root, key)
			if ok {
				target.NonVirtual = true
				result.targets = []Target{target}
			}
		} else if pathHasVirtual(full) {
			result.targets, result.ambs = g.finalVirtualOverriders(dynamic, access.root, key, receiverPath)
		} else {
			target := g.finalNonVirtualOverrider(dynamic, access.root, full, key)
			if target.Class != "" {
				result.targets = []Target{target}
			}
		}
		results = append(results, result)
	}

	var targets []Target
	var ambiguities []Ambiguity
	seenTargets := make(map[string]bool)
	nonVirtualSlots := 0
	virtualSlots := 0
	for _, result := range results {
		if result.static {
			nonVirtualSlots++
		} else {
			virtualSlots++
		}
		for _, target := range result.targets {
			if !seenTargets[target.Key()] {
				seenTargets[target.Key()] = true
				targets = append(targets, target)
			}
		}
		ambiguities = append(ambiguities, result.ambs...)
	}
	if len(results) > 1 && len(targets) > 1 {
		ambiguities = append(ambiguities, Ambiguity{
			Kind:        "call_lookup",
			DynamicType: dynamic,
			StaticType:  static,
			Method:      key,
			Path:        receiverPath.Clone(),
			Overriders:  append([]Target(nil), targets...),
			Reason:      fmt.Sprintf("%s has multiple non-dominated lookup results for %s", dynamic, key),
		})
	} else if len(results) > 1 && nonVirtualSlots == len(results) {
		ambiguities = append(ambiguities, Ambiguity{
			Kind:        "call_lookup",
			DynamicType: dynamic,
			StaticType:  static,
			Method:      key,
			Path:        receiverPath.Clone(),
			Overriders:  append([]Target(nil), targets...),
			Reason:      fmt.Sprintf("%s has multiple non-virtual subobject lookups for %s", dynamic, key),
		})
	}
	sortTargets(targets)
	sortAmbiguities(ambiguities)
	return targets, ambiguities, virtualSlots == 0 && nonVirtualSlots != 0, true
}

func (g *graph) finalVirtualOverriders(dynamic, root, key string, receiverPath Path) ([]Target, []Ambiguity) {
	var candidates []Target
	seen := make(map[string]bool)
	for _, name := range g.order {
		node := g.classes[name]
		if _, ok := node.methods[key]; !ok || !g.methodVirtual(name, key) {
			continue
		}
		if !g.isAncestor(root, name) || !g.isAncestor(name, dynamic) {
			continue
		}
		target, ok := g.targetFor(name, key)
		if ok && !seen[target.Key()] {
			seen[target.Key()] = true
			candidates = append(candidates, target)
		}
	}
	if len(candidates) == 0 {
		if target, ok := g.targetFor(root, key); ok {
			candidates = append(candidates, target)
		}
	}

	var maxima []Target
	for i, candidate := range candidates {
		dominated := false
		for j, other := range candidates {
			if i == j {
				continue
			}
			if g.isAncestor(candidate.Class, other.Class) && candidate.Class != other.Class {
				dominated = true
				break
			}
		}
		if !dominated {
			maxima = append(maxima, candidate)
		}
	}
	sortTargets(maxima)
	if len(maxima) > 1 {
		ambiguity := Ambiguity{
			Kind:        "virtual_slot",
			DynamicType: dynamic,
			StaticType:  receiverStatic(receiverPath, dynamic),
			Method:      key,
			Slot:        "virtual:" + root,
			Path:        receiverPath.Clone(),
			Overriders:  append([]Target(nil), maxima...),
			Reason: fmt.Sprintf(
				"shared virtual slot %s::%s has multiple non-dominated final overriders in %s",
				root, key, dynamic,
			),
		}
		return maxima, []Ambiguity{ambiguity}
	}
	return maxima, nil
}

func receiverStatic(path Path, dynamic string) string {
	if len(path) == 0 {
		return dynamic
	}
	return path[len(path)-1].ClassName()
}

func (g *graph) finalNonVirtualOverrider(dynamic, root string, full Path, key string) Target {
	classes := pathClasses(dynamic, full)
	for _, name := range classes {
		node := g.classes[name]
		if node == nil {
			continue
		}
		if _, ok := node.methods[key]; !ok || !g.methodVirtual(name, key) {
			continue
		}
		if target, ok := g.targetFor(name, key); ok {
			return target
		}
	}
	if target, ok := g.targetFor(root, key); ok {
		return target
	}
	return Target{}
}

func (g *graph) refsForStaticClass(dynamic, static, method string, selector []string) ([]ref, []Ambiguity, error) {
	if !g.hasClass(dynamic) {
		return nil, nil, fmt.Errorf("%w: %s", ErrUnknownClass, dynamic)
	}
	if !g.hasClass(static) {
		return nil, nil, fmt.Errorf("%w: %s", ErrUnknownClass, static)
	}
	if !g.isAncestor(static, dynamic) {
		return nil, nil, fmt.Errorf("%w: %s is not a base of %s", ErrInvalidConversion, static, dynamic)
	}

	var candidates []ref
	for _, path := range g.localPaths(dynamic, static) {
		candidates = append(candidates, ref{
			dynamic: dynamic,
			static:  static,
			path:    path.Clone(),
		})
	}
	if len(candidates) == 0 {
		return nil, nil, fmt.Errorf("%w: %s is not a subobject of %s", ErrInvalidConversion, static, dynamic)
	}

	selector = normalizeSelector(dynamic, selector)
	if len(selector) != 0 {
		filtered := candidates[:0]
		for _, candidate := range candidates {
			if matchPathSelector(selector, dynamic, candidate.path) {
				filtered = append(filtered, candidate)
			}
		}
		candidates = filtered
		if len(candidates) == 0 {
			return nil, nil, fmt.Errorf("%w: no path %s from %s to %s", ErrInvalidConversion, strings.Join(selector, "::"), dynamic, static)
		}
	}

	seen := make(map[string]ref)
	for _, candidate := range candidates {
		identity := g.subobjectIdentity(dynamic, candidate.path)
		if existing, ok := seen[identity]; ok {
			_ = existing
			continue
		}
		seen[identity] = candidate
	}
	out := make([]ref, 0, len(seen))
	for _, candidate := range seen {
		out = append(out, candidate)
	}
	sort.Slice(out, func(i, j int) bool {
		return pathKey(out[i].path) < pathKey(out[j].path)
	})

	var ambiguities []Ambiguity
	if len(out) > 1 && len(selector) == 0 {
		overriders := make([]Target, 0, len(out))
		for _, candidate := range out {
			overriders = append(overriders, Target{Class: candidate.staticClass(), Method: method})
		}
		ambiguities = append(ambiguities, Ambiguity{
			Kind:        "conversion",
			DynamicType: dynamic,
			StaticType:  static,
			Method:      method,
			Reason:      fmt.Sprintf("%s has multiple non-virtual subobjects of %s; select a path", dynamic, static),
			Overriders:  overriders,
		})
	}
	return out, ambiguities, nil
}

func normalizeSelector(root string, selector []string) []string {
	if len(selector) == 0 {
		return nil
	}
	out := make([]string, 0, len(selector))
	for _, part := range selector {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.Contains(part, "::") || strings.Contains(part, "->") || strings.Contains(part, "/") || strings.Contains(part, ".") {
			out = append(out, splitSelector(part)...)
			continue
		}
		out = append(out, part)
	}
	if len(out) != 0 && cleanSelectorPart(out[0]) == root {
		out = out[1:]
	}
	return out
}

func splitSelector(value string) []string {
	value = strings.ReplaceAll(value, "->", "::")
	value = strings.ReplaceAll(value, "/", "::")
	value = strings.ReplaceAll(value, ".", "::")
	raw := strings.Split(value, "::")
	out := raw[:0]
	for _, item := range raw {
		if strings.TrimSpace(item) != "" {
			out = append(out, strings.TrimSpace(item))
		}
	}
	return out
}

func cleanSelectorPart(part string) string {
	if index := strings.IndexByte(part, '['); index >= 0 {
		return part[:index]
	}
	return part
}

func matchPathSelector(selector []string, root string, path Path) bool {
	classes := pathClasses(root, path)
	if len(selector) > len(classes) {
		return false
	}
	start := len(classes) - len(selector)
	if len(selector) != 0 && cleanSelectorPart(selector[0]) == root {
		start = 0
	}
	for i, part := range selector {
		if start+i >= len(classes) {
			return false
		}
		name, index, hasIndex := parseSelectorPart(part)
		step := Step{Class: classes[start+i]}
		if start+i > 0 {
			step = path[start+i-1]
		}
		if step.ClassName() != name {
			return false
		}
		if hasIndex {
			if step.Repeated {
				if step.Occurrence != index {
					return false
				}
			} else if step.Index != index {
				return false
			}
		}
	}
	return true
}

func parseSelectorPart(part string) (string, int, bool) {
	part = strings.TrimSpace(part)
	open := strings.LastIndexByte(part, '[')
	if open < 0 || !strings.HasSuffix(part, "]") {
		return part, 0, false
	}
	index, err := strconv.Atoi(part[open+1 : len(part)-1])
	if err != nil {
		return part, 0, false
	}
	return part[:open], index, true
}

func appendTrace(trace []TraceStep, steps ...TraceStep) []TraceStep {
	out := make([]TraceStep, 0, len(trace)+len(steps))
	out = append(out, trace...)
	out = append(out, steps...)
	return out
}

func (g *graph) provenanceFor(value ref, target Target, field string) Provenance {
	trace := appendTrace(nil, value.trace...)
	if len(trace) == 0 || trace[0].Kind != "allocation" {
		trace = append([]TraceStep{{
			Kind:  "allocation",
			ID:    value.allocID,
			Site:  value.site,
			Class: value.dynamic,
		}}, trace...)
	}
	sourcePath := make([]string, 0, len(trace))
	for _, step := range trace {
		if step.ID != "" {
			sourcePath = append(sourcePath, step.Kind+":"+step.ID)
		} else if step.Field != "" {
			sourcePath = append(sourcePath, step.Kind+":"+step.Field)
		}
	}
	return Provenance{
		Target:         target,
		Allocation:     value.allocID,
		AllocationID:   value.allocID,
		AllocationSite: value.site,
		DynamicType:    value.dynamic,
		StaticType:     value.staticClass(),
		ReceiverPath:   value.path.Clone(),
		Path:           pathClasses(value.dynamic, value.path),
		SourcePath:     sourcePath,
		Field:          field,
		Trace:          trace,
	}
}
