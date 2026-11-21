package typeset

import (
	"fmt"
	"sort"
	"strings"
)

type fieldKey struct {
	allocation string
	identity   string
	field      string
}

type engine struct {
	g               *graph
	allocs          []Allocation
	allocByID       map[string]Allocation
	upcasts         []Upcast
	fields          []FieldAssignment
	calls           []VirtualCall
	values          map[string]map[string]ref
	fieldValues     map[fieldKey]map[string]ref
	conversionAmbs  []Ambiguity
	diagnostics     []string
	allocationTrace map[string]TraceStep
}

func newEngine(g *graph, model Model) (*engine, error) {
	e := &engine{
		g:               g,
		values:          make(map[string]map[string]ref),
		fieldValues:     make(map[fieldKey]map[string]ref),
		allocByID:       make(map[string]Allocation, len(model.Allocations)),
		allocationTrace: make(map[string]TraceStep),
	}
	allocs := append([]Allocation(nil), model.Allocations...)
	for i := range allocs {
		alloc := &allocs[i]
		if strings.TrimSpace(alloc.ID) == "" {
			alloc.ID = strings.TrimSpace(alloc.Name)
		}
		if strings.TrimSpace(alloc.ID) == "" {
			alloc.ID = fmt.Sprintf("alloc:%d", i)
		}
		class := strings.TrimSpace(alloc.Class)
		if class == "" {
			class = strings.TrimSpace(alloc.Type)
		}
		if class == "" {
			return nil, fmt.Errorf("%w: allocation %s has no class", ErrInvalidModel, alloc.ID)
		}
		if !g.hasClass(class) {
			return nil, fmt.Errorf("%w: allocation %s uses %s", ErrUnknownClass, alloc.ID, class)
		}
		if _, exists := e.allocByID[alloc.ID]; exists {
			return nil, fmt.Errorf("%w: allocation %s", ErrDuplicateID, alloc.ID)
		}
		alloc.Class = class
		e.allocByID[alloc.ID] = *alloc
		site := alloc.Site
		if site == "" {
			site = alloc.Location
		}
		if site == "" {
			site = alloc.Source
		}
		e.allocationTrace[alloc.ID] = TraceStep{Kind: "allocation", ID: alloc.ID, Site: site, Class: class}
	}
	e.allocs = allocs

	e.upcasts = append([]Upcast(nil), model.Upcasts...)
	seenUpcast := make(map[string]bool, len(e.upcasts))
	for i := range e.upcasts {
		if strings.TrimSpace(e.upcasts[i].ID) == "" {
			e.upcasts[i].ID = fmt.Sprintf("upcast:%d", i)
		}
		if seenUpcast[e.upcasts[i].ID] {
			return nil, fmt.Errorf("%w: upcast %s", ErrDuplicateID, e.upcasts[i].ID)
		}
		seenUpcast[e.upcasts[i].ID] = true
		to := upcastTarget(e.upcasts[i])
		if to != "" && !g.hasClass(to) {
			return nil, fmt.Errorf("%w: upcast %s targets %s", ErrUnknownClass, e.upcasts[i].ID, to)
		}
	}
	e.fields = append([]FieldAssignment(nil), model.FieldAssignments...)
	seenField := make(map[string]bool, len(e.fields))
	for i := range e.fields {
		if strings.TrimSpace(e.fields[i].ID) == "" {
			e.fields[i].ID = fmt.Sprintf("field:%d", i)
		}
		if seenField[e.fields[i].ID] {
			return nil, fmt.Errorf("%w: field assignment %s", ErrDuplicateID, e.fields[i].ID)
		}
		seenField[e.fields[i].ID] = true
		if e.fields[i].Field == "" {
			e.fields[i].Field = e.fields[i].Name
		}
		if e.fields[i].Field == "" && e.fields[i].To != "" {
			if left, field, ok := splitFieldExpr(e.fields[i].To); ok {
				e.fields[i].Field = field
				if firstNonEmpty(e.fields[i].Object, e.fields[i].Receiver, e.fields[i].Target) == "" {
					e.fields[i].Object = left
				}
			} else {
				e.fields[i].Field = e.fields[i].To
			}
		}
	}
	e.calls = append([]VirtualCall(nil), model.Calls...)
	seenCall := make(map[string]bool, len(e.calls))
	for i := range e.calls {
		call := &e.calls[i]
		if strings.TrimSpace(call.ID) == "" {
			call.ID = fmt.Sprintf("call:%d", i)
		}
		if seenCall[call.ID] {
			return nil, fmt.Errorf("%w: call %s", ErrDuplicateID, call.ID)
		}
		seenCall[call.ID] = true
		if call.Method == "" {
			call.Method = call.Name
		}
		class := call.StaticType
		if class == "" {
			class = call.Type
		}
		if class != "" && !g.hasClass(class) {
			return nil, fmt.Errorf("%w: call %s uses static type %s", ErrUnknownClass, call.ID, class)
		}
		call.StaticType = class
	}
	return e, nil
}

func (e *engine) run() {
	for _, alloc := range e.allocs {
		root := ref{
			allocID: alloc.ID,
			site:    e.siteFor(alloc.ID),
			dynamic: alloc.Class,
			static:  alloc.Class,
			trace:   []TraceStep{e.allocationTrace[alloc.ID]},
		}
		e.addValue(alloc.ID, root)
	}

	for {
		changed := false
		for _, alloc := range e.allocs {
			if len(alloc.Fields) == 0 {
				continue
			}
			objects := sortedRefs(e.values[alloc.ID])
			fields := make([]string, 0, len(alloc.Fields))
			for field := range alloc.Fields {
				fields = append(fields, field)
			}
			sort.Strings(fields)
			for _, field := range fields {
				var stored []ref
				for _, valueID := range alloc.Fields[field] {
					stored = append(stored, e.refsForValue(valueID)...)
				}
				if e.storeIntoRefs(objects, field, "", nil, stored, alloc.ID, "allocation") {
					changed = true
				}
			}
		}
		for _, cast := range e.upcasts {
			sourceID := firstNonEmpty(cast.From, cast.Object, cast.Source, cast.Value)
			if sourceID == "" {
				continue
			}
			to := upcastTarget(cast)
			if to == "" {
				e.addDiagnostic(fmt.Sprintf("upcast %s has no destination", cast.ID))
				continue
			}
			selector := cast.Path
			if len(selector) == 0 && cast.PathString != "" {
				selector = splitSelector(cast.PathString)
			}
			for _, source := range e.refsForValue(sourceID) {
				converted, ambiguities, err := e.convertRef(source, to, selector, TraceStep{
					Kind:  "upcast",
					ID:    cast.ID,
					Site:  cast.Site,
					Class: to,
				})
				if err != nil {
					e.addDiagnostic(fmt.Sprintf("upcast %s: %v", cast.ID, err))
					continue
				}
				for _, ambiguity := range ambiguities {
					ambiguity.CallID = cast.ID
					e.addConversionAmbiguity(ambiguity)
				}
				for _, value := range converted {
					if e.addValue(cast.ID, value) {
						changed = true
					}
				}
			}
		}
		for _, assignment := range e.fields {
			objects := e.assignmentObjects(assignment)
			values := e.assignmentValues(assignment)
			field := assignment.Field
			if field == "" {
				e.addDiagnostic(fmt.Sprintf("field assignment %s has no field", assignment.ID))
				continue
			}
			selector := assignment.Path
			if len(selector) == 0 && assignment.PathString != "" {
				selector = splitSelector(assignment.PathString)
			}
			if e.storeIntoRefs(objects, field, assignment.Owner, selector, values, assignment.ID, assignment.Site) {
				changed = true
			}
		}
		if !changed {
			break
		}
	}
}

func (e *engine) assignmentObjects(assignment FieldAssignment) []ref {
	object := firstNonEmpty(assignment.Object, assignment.Receiver, assignment.Target, assignment.To)
	if object != "" {
		return e.refsForValue(object)
	}
	if assignment.Class != "" {
		return e.refsForClass(assignment.Class)
	}
	return nil
}

func (e *engine) assignmentValues(assignment FieldAssignment) []ref {
	value := firstNonEmpty(assignment.Value, assignment.Source, assignment.From)
	if value == "" {
		return nil
	}
	return e.refsForValue(value)
}

func (e *engine) siteFor(allocation string) string {
	alloc := e.allocByID[allocation]
	if alloc.Site != "" {
		return alloc.Site
	}
	if alloc.Location != "" {
		return alloc.Location
	}
	return alloc.Source
}

func (e *engine) addValue(id string, values ...ref) bool {
	if id == "" {
		return false
	}
	set := e.values[id]
	if set == nil {
		set = make(map[string]ref)
		e.values[id] = set
	}
	changed := false
	for _, value := range values {
		if value.allocID == "" {
			continue
		}
		key := value.key()
		if _, exists := set[key]; exists {
			continue
		}
		set[key] = value
		changed = true
	}
	return changed
}

func (e *engine) refsForValue(id string) []ref {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	if values, ok := e.values[id]; ok {
		return sortedRefs(values)
	}
	if alloc, ok := e.allocByID[id]; ok {
		return sortedRefs(e.values[alloc.ID])
	}
	if e.g.hasClass(id) {
		return e.refsForClass(id)
	}
	if left, field, ok := splitFieldExpr(id); ok {
		return e.loadFromRefs(e.refsForValue(left), field, "")
	}
	if strings.HasPrefix(id, "field:") {
		rest := strings.TrimPrefix(id, "field:")
		if left, field, ok := splitFieldExpr(rest); ok {
			return e.loadFromRefs(e.refsForValue(left), field, "")
		}
	}
	return nil
}

func splitFieldExpr(value string) (string, string, bool) {
	if index := strings.LastIndex(value, "->"); index > 0 && index+2 < len(value) {
		return value[:index], value[index+2:], true
	}
	if index := strings.LastIndexByte(value, '.'); index > 0 && index+1 < len(value) {
		return value[:index], value[index+1:], true
	}
	return "", "", false
}

func (e *engine) refsForClass(class string) []ref {
	var out []ref
	seen := make(map[string]bool)
	for _, alloc := range e.allocs {
		if !e.g.isAncestor(class, alloc.Class) {
			continue
		}
		refs, _, err := e.g.refsForStaticClass(alloc.Class, class, "", nil)
		if err != nil {
			continue
		}
		for _, value := range refs {
			value.allocID = alloc.ID
			value.site = e.siteFor(alloc.ID)
			value.trace = []TraceStep{e.allocationTrace[alloc.ID]}
			if !seen[value.key()] {
				seen[value.key()] = true
				out = append(out, value)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key() < out[j].key() })
	return out
}

func (e *engine) convertRef(source ref, to string, selector []string, trace TraceStep) ([]ref, []Ambiguity, error) {
	if to == "" {
		return []ref{source}, nil, nil
	}
	if !e.g.hasClass(to) {
		return nil, nil, fmt.Errorf("%w: %s", ErrUnknownClass, to)
	}
	if source.staticClass() == to {
		out := source
		out.static = to
		return []ref{out}, nil, nil
	}
	if !e.g.isAncestor(to, source.staticClass()) {
		return nil, nil, fmt.Errorf("%w: %s is not a base of %s", ErrInvalidConversion, to, source.staticClass())
	}
	locals := e.g.localPaths(source.staticClass(), to)
	if len(locals) == 0 {
		return nil, nil, fmt.Errorf("%w: no path from %s to %s", ErrInvalidConversion, source.staticClass(), to)
	}
	selector = normalizeSelector(source.dynamic, selector)
	if len(selector) != 0 {
		filtered := locals[:0]
		for _, local := range locals {
			full := appendPaths(source.path, local)
			if matchPathSelector(selector, source.dynamic, full) {
				filtered = append(filtered, local)
			}
		}
		locals = filtered
		if len(locals) == 0 {
			return nil, nil, fmt.Errorf("%w: no selected path to %s", ErrInvalidConversion, to)
		}
	}

	type converted struct {
		value    ref
		identity string
	}
	var candidates []converted
	seen := make(map[string]bool)
	for _, local := range locals {
		full := appendPaths(source.path, local)
		identity := e.g.subobjectIdentity(source.dynamic, full)
		if seen[identity] {
			continue
		}
		seen[identity] = true
		value := source.withStatic(to, full, trace)
		candidates = append(candidates, converted{value: value, identity: identity})
	}
	if len(candidates) == 0 {
		return nil, nil, fmt.Errorf("%w: no path from %s to %s", ErrInvalidConversion, source.staticClass(), to)
	}
	out := make([]ref, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, candidate.value)
	}
	var ambiguities []Ambiguity
	if len(candidates) > 1 && len(selector) == 0 {
		overriders := make([]Target, 0, len(candidates))
		for _, candidate := range candidates {
			overriders = append(overriders, Target{Class: to, Method: pathDisplay(source.dynamic, candidate.value.path)})
		}
		ambiguities = append(ambiguities, Ambiguity{
			Kind:        "conversion",
			DynamicType: source.dynamic,
			StaticType:  to,
			Reason:      fmt.Sprintf("%s has multiple non-virtual subobjects of %s; select a path", source.dynamic, to),
			Overriders:  overriders,
		})
	}
	return out, ambiguities, nil
}

func (g *graph) fieldAccess(class, field, owner string) ([]accessSlot, bool) {
	if owner != "" {
		if !g.hasClass(owner) || !g.isAncestor(owner, class) {
			return nil, false
		}
		var out []accessSlot
		for _, path := range g.localPaths(class, owner) {
			out = append(out, accessSlot{root: owner, path: path})
		}
		return out, len(out) != 0
	}
	node := g.classes[class]
	if node == nil {
		return nil, false
	}
	if _, ok := node.fields[field]; ok {
		return []accessSlot{{root: class}}, true
	}
	var out []accessSlot
	for _, base := range node.bases {
		child, found := g.fieldAccess(base.class, field, "")
		if !found {
			continue
		}
		step := Step{Class: base.class, Name: base.class, Virtual: base.virtual, Index: base.index}
		for _, slot := range child {
			out = append(out, prependSlot(step, slot))
		}
	}
	return out, len(out) != 0
}

func (e *engine) storeIntoRefs(objects []ref, field, owner string, selector []string, values []ref, assignmentID, site string) bool {
	if field == "" || len(objects) == 0 || len(values) == 0 {
		return false
	}
	changed := false
	seenAmbiguity := make(map[string]bool)
	for _, object := range objects {
		accesses, found := e.g.fieldAccess(object.staticClass(), field, owner)
		if !found {
			accesses = []accessSlot{{root: object.staticClass()}}
		}
		var identities []string
		for _, access := range accesses {
			full := appendPaths(object.path, access.path)
			if len(selector) != 0 && !matchPathSelector(normalizeSelector(object.dynamic, selector), object.dynamic, full) {
				continue
			}
			identity := e.g.subobjectIdentity(object.dynamic, full)
			if containsString(identities, identity) {
				continue
			}
			identities = append(identities, identity)
			key := fieldKey{allocation: object.allocID, identity: identity, field: field}
			set := e.fieldValues[key]
			if set == nil {
				set = make(map[string]ref)
				e.fieldValues[key] = set
			}
			for _, value := range values {
				value.trace = appendTrace(value.trace, TraceStep{
					Kind:  "field_assignment",
					ID:    assignmentID,
					Site:  site,
					Field: field,
				})
				valueKey := value.key()
				if _, exists := set[valueKey]; exists {
					continue
				}
				set[valueKey] = value
				changed = true
			}
		}
		if len(identities) > 1 && len(selector) == 0 && !seenAmbiguity[object.key()] {
			seenAmbiguity[object.key()] = true
			e.addConversionAmbiguity(Ambiguity{
				Kind:        "field_lookup",
				DynamicType: object.dynamic,
				StaticType:  object.staticClass(),
				Field:       field,
				Path:        object.path.Clone(),
				Reason:      fmt.Sprintf("field %s of %s has multiple subobject slots", field, object.staticClass()),
			})
		}
	}
	return changed
}

func (e *engine) loadFromRefs(objects []ref, field, owner string, selectors ...[]string) []ref {
	if field == "" || len(objects) == 0 {
		return nil
	}
	var selector []string
	if len(selectors) != 0 {
		selector = selectors[0]
	}
	var out []ref
	seen := make(map[string]bool)
	seenAmbiguity := make(map[string]bool)
	for _, object := range objects {
		accesses, found := e.g.fieldAccess(object.staticClass(), field, owner)
		if !found {
			accesses = []accessSlot{{root: object.staticClass()}}
		}
		identities := make([]string, 0, len(accesses))
		for _, access := range accesses {
			full := appendPaths(object.path, access.path)
			if len(selector) != 0 && !matchPathSelector(normalizeSelector(object.dynamic, selector), object.dynamic, full) {
				continue
			}
			identity := e.g.subobjectIdentity(object.dynamic, full)
			if containsString(identities, identity) {
				continue
			}
			identities = append(identities, identity)
			key := fieldKey{allocation: object.allocID, identity: identity, field: field}
			for _, value := range e.fieldValues[key] {
				value.trace = appendTrace(value.trace, TraceStep{
					Kind:  "field_load",
					Field: field,
				})
				if seen[value.key()] {
					continue
				}
				seen[value.key()] = true
				out = append(out, value)
			}
		}
		if len(identities) > 1 && len(selector) == 0 && !seenAmbiguity[object.key()] {
			seenAmbiguity[object.key()] = true
			e.addConversionAmbiguity(Ambiguity{
				Kind:        "field_lookup",
				DynamicType: object.dynamic,
				StaticType:  object.staticClass(),
				Field:       field,
				Path:        object.path.Clone(),
				Reason:      fmt.Sprintf("field %s of %s has multiple subobject slots", field, object.staticClass()),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key() < out[j].key() })
	return out
}

func (e *engine) addConversionAmbiguity(ambiguity Ambiguity) {
	key := ambiguity.Kind + "|" + ambiguity.CallID + "|" + ambiguity.DynamicType + "|" + ambiguity.StaticType + "|" + ambiguity.Method + "|" + ambiguity.Field + "|" + ambiguity.Reason
	for _, existing := range e.conversionAmbs {
		other := existing.Kind + "|" + existing.CallID + "|" + existing.DynamicType + "|" + existing.StaticType + "|" + existing.Method + "|" + existing.Field + "|" + existing.Reason
		if key == other {
			return
		}
	}
	e.conversionAmbs = append(e.conversionAmbs, ambiguity)
}

func (e *engine) addDiagnostic(message string) {
	for _, existing := range e.diagnostics {
		if existing == message {
			return
		}
	}
	e.diagnostics = append(e.diagnostics, message)
}

func sortedRefs(values map[string]ref) []ref {
	out := make([]ref, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key() < out[j].key() })
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func upcastTarget(cast Upcast) string {
	return firstNonEmpty(cast.To, cast.Target, cast.Type, cast.Dest, cast.Destination)
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
