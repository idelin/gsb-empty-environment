package typeset

import (
	"encoding/json"
	"fmt"
	"sort"
)

// AnalyzeModel runs the complete analysis over a typed model.
//
// Inheritance cycles and structurally invalid models are rejected with an
// error.  Ambiguous virtual slots, conversions and field lookups are reported
// in Result.Ambiguities while still returning every candidate target for
// soundness.  Use AnalyzeStrict when an ambiguity must be an error.
func AnalyzeModel(model Model) (*Result, error) {
	h, err := BuildHierarchy(model.Classes)
	if err != nil {
		return nil, err
	}
	e, err := newEngine(h.g, model)
	if err != nil {
		return nil, err
	}
	e.run()
	return e.result()
}

// AnalyzeProgram is an alias for AnalyzeModel.
func AnalyzeProgram(model Model) (*Result, error) { return AnalyzeModel(model) }

// AnalyzeClasses analyzes a hierarchy with no allocations or calls.
func AnalyzeClasses(classes []Class) (*Result, error) {
	return AnalyzeModel(Model{Classes: classes})
}

// AnalyzeStrict is AnalyzeModel plus an error when any ambiguity is reported.
// The result is still returned and fully populated.
func AnalyzeStrict(args ...any) (*Result, error) {
	result, err := Analyze(args...)
	if err != nil {
		return result, err
	}
	if len(result.Ambiguities) != 0 {
		return result, &AmbiguityError{Items: append([]Ambiguity(nil), result.Ambiguities...)}
	}
	return result, nil
}

// Analyze accepts the common embedding forms:
//
//	Analyze(Model{...})
//	Analyze([]Class{...}, []Allocation{...}, []Upcast{...},
//	        []FieldAssignment{...}, []VirtualCall{...})
//	Analyze(classes, allocations, upcasts, assignments, calls)
//	AnalyzeJSON([]byte(...))
//
// Slice arguments may be omitted or supplied in any order after the classes.
func Analyze(args ...any) (*Result, error) {
	model, err := modelFromArgs(args...)
	if err != nil {
		return nil, err
	}
	return AnalyzeModel(model)
}

// AnalyzeJSON decodes and analyzes a model.  data may be a string or []byte.
func AnalyzeJSON(data any) (*Result, error) {
	var model Model
	raw, err := jsonBytes(data)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &model); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidModel, err)
	}
	return AnalyzeModel(model)
}

// DecodeJSON decodes a model without running it.  data may be a string or
// []byte.
func DecodeJSON(data any) (Model, error) {
	var model Model
	raw, err := jsonBytes(data)
	if err != nil {
		return Model{}, err
	}
	if err := json.Unmarshal(raw, &model); err != nil {
		return Model{}, fmt.Errorf("%w: %v", ErrInvalidModel, err)
	}
	return model, nil
}

func jsonBytes(data any) ([]byte, error) {
	switch value := data.(type) {
	case []byte:
		return value, nil
	case string:
		return []byte(value), nil
	default:
		return nil, fmt.Errorf("%w: JSON input must be string or []byte, got %T", ErrInvalidModel, data)
	}
}

func modelFromArgs(args ...any) (Model, error) {
	var model Model
	if len(args) == 0 {
		return model, fmt.Errorf("%w: no input", ErrInvalidModel)
	}
	if len(args) == 1 {
		switch value := args[0].(type) {
		case Model:
			return value, nil
		case *Model:
			if value == nil {
				return model, fmt.Errorf("%w: nil model", ErrInvalidModel)
			}
			return *value, nil
		case []Class:
			model.Classes = value
			return model, nil
		case []byte:
			return DecodeJSON(value)
		case string:
			return DecodeJSON([]byte(value))
		case *Hierarchy:
			return model, fmt.Errorf("%w: hierarchy input also needs allocations/calls", ErrInvalidModel)
		}
	}
	for _, arg := range args {
		switch value := arg.(type) {
		case Model:
			model = mergeModel(model, value)
		case *Model:
			if value != nil {
				model = mergeModel(model, *value)
			}
		case []Class:
			model.Classes = append(model.Classes, value...)
		case map[string]Class:
			names := make([]string, 0, len(value))
			for name := range value {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				class := value[name]
				if class.Name == "" {
					class.Name = name
				}
				model.Classes = append(model.Classes, class)
			}
		case map[string][]Base:
			names := make([]string, 0, len(value))
			for name := range value {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				model.Classes = append(model.Classes, Class{Name: name, Bases: value[name]})
			}
		case map[string][]string:
			names := make([]string, 0, len(value))
			for name := range value {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				bases := make([]Base, len(value[name]))
				for i, base := range value[name] {
					bases[i] = Base{Class: base}
				}
				model.Classes = append(model.Classes, Class{Name: name, Bases: bases})
			}
		case []Allocation:
			model.Allocations = append(model.Allocations, value...)
		case []Upcast:
			model.Upcasts = append(model.Upcasts, value...)
		case []FieldAssignment:
			model.FieldAssignments = append(model.FieldAssignments, value...)
		case []VirtualCall:
			model.Calls = append(model.Calls, value...)
		case []byte:
			decoded, err := DecodeJSON(value)
			if err != nil {
				return model, err
			}
			model = mergeModel(model, decoded)
		case string:
			decoded, err := DecodeJSON([]byte(value))
			if err != nil {
				return model, err
			}
			model = mergeModel(model, decoded)
		default:
			return model, fmt.Errorf("%w: unsupported argument %T", ErrInvalidModel, arg)
		}
	}
	if len(model.Classes) == 0 {
		return model, fmt.Errorf("%w: no classes", ErrInvalidModel)
	}
	return model, nil
}

func mergeModel(dst, src Model) Model {
	dst.Classes = append(dst.Classes, src.Classes...)
	dst.Allocations = append(dst.Allocations, src.Allocations...)
	dst.Upcasts = append(dst.Upcasts, src.Upcasts...)
	dst.FieldAssignments = append(dst.FieldAssignments, src.FieldAssignments...)
	dst.Calls = append(dst.Calls, src.Calls...)
	return dst
}

func (e *engine) result() (*Result, error) {
	result := &Result{
		ByCall:     make(map[string]CallResult),
		Targets:    make(map[string][]Target),
		Candidates: make(map[string][]Target),
		Layouts:    make(map[string]ObjectLayout, len(e.g.layouts)),
		Validated:  true,
	}
	for _, layout := range e.g.layouts {
		result.Layouts[layout.DynamicType] = cloneLayout(layout)
	}
	allAmbiguities := append([]Ambiguity(nil), e.conversionAmbs...)
	var validationMessages []string

	for _, call := range e.calls {
		callResult := e.analyzeCall(call)
		result.Calls = append(result.Calls, callResult)
		result.ByCall[callResult.ID] = callResult
		result.Targets[callResult.ID] = append([]Target(nil), callResult.Targets...)
		result.Candidates[callResult.ID] = append([]Target(nil), callResult.Targets...)
		allAmbiguities = append(allAmbiguities, callResult.Ambiguities...)

		check := e.validateCall(call, callResult)
		callResult.Validation = check
		result.Calls[len(result.Calls)-1] = callResult
		result.ByCall[callResult.ID] = callResult
		if !check.OK {
			result.Validated = false
			result.Validation.OK = false
			result.Validation.Missing = append(result.Validation.Missing, check.Missing...)
			validationMessages = append(validationMessages, check.Messages...)
		}
		result.Validation.Expected = append(result.Validation.Expected, check.Expected...)
		result.Validation.Spurious = append(result.Validation.Spurious, check.Spurious...)
		result.Validation.ReceiverCount += check.ReceiverCount
	}
	result.Validation.Missing = uniqueTargets(result.Validation.Missing)
	result.Validation.Expected = uniqueTargets(result.Validation.Expected)
	result.Validation.Spurious = uniqueTargets(result.Validation.Spurious)
	result.Validation.Messages = append(result.Validation.Messages, validationMessages...)
	result.Validation.OK = len(result.Validation.Missing) == 0
	result.Validation.Spurious = differenceTargets(result.Validation.Spurious, result.Validation.Expected)
	if result.Validation.OK {
		result.Validation.Messages = append(result.Validation.Messages, "finite object execution model found no missed virtual-call targets")
	} else {
		result.Validation.Messages = append(result.Validation.Messages, "finite object execution model found missed virtual-call targets")
	}

	allAmbiguities = append(allAmbiguities, e.conversionAmbs...)
	result.Ambiguities = dedupeAmbiguities(allAmbiguities)
	sortAmbiguities(result.Ambiguities)
	result.Diagnostics = append([]string(nil), e.diagnostics...)
	sort.Strings(result.Diagnostics)
	if !result.Validation.OK {
		return result, fmt.Errorf("%w: %d target(s) missing", ErrValidationFailed, len(result.Validation.Missing))
	}
	return result, nil
}

func (e *engine) analyzeCall(call VirtualCall) CallResult {
	out := CallResult{
		ID:         call.ID,
		Site:       call.Site,
		Method:     firstNonEmpty(call.Method, call.Name, call.Signature),
		StaticType: call.StaticType,
	}
	field := call.Field
	if field == "" {
		if _, parsed, ok := splitFieldExpr(firstNonEmpty(call.Receiver, call.Value)); ok {
			field = parsed
		}
	}
	method := callMethodKey(call)
	if method == "" {
		out.Error = "call has no method"
		return out
	}
	refs, ambiguities := e.callReceivers(call, method)
	out.ReceiverCount = len(refs)
	out.Ambiguities = append(out.Ambiguities, ambiguities...)
	seenTargets := make(map[string]bool)
	seenProvenance := make(map[string]bool)
	allNonVirtual := len(refs) != 0
	for _, value := range refs {
		targets, ambs, nonVirtual, found := e.g.resolveAt(value.dynamic, value.path, value.staticClass(), method)
		if !found {
			if out.Error == "" {
				out.Error = fmt.Sprintf("%s: %s::%s", ErrUnknownMethod, value.staticClass(), method)
			}
			continue
		}
		allNonVirtual = allNonVirtual && nonVirtual
		for _, ambiguity := range ambs {
			ambiguity.CallID = call.ID
			out.Ambiguities = append(out.Ambiguities, ambiguity)
		}
		for _, target := range targets {
			if !seenTargets[target.Key()] {
				seenTargets[target.Key()] = true
				out.Targets = append(out.Targets, target)
			}
			provenance := e.g.provenanceFor(value, target, field)
			key := target.Key() + "|" + provenance.Allocation + "|" + pathKey(provenance.ReceiverPath) + "|" + provenance.StaticType
			if seenProvenance[key] {
				continue
			}
			seenProvenance[key] = true
			out.Provenance = append(out.Provenance, provenance)
		}
	}
	sortTargets(out.Targets)
	out.Candidates = append([]Target(nil), out.Targets...)
	sort.Slice(out.Provenance, func(i, j int) bool {
		a, b := out.Provenance[i], out.Provenance[j]
		if a.Target.Key() != b.Target.Key() {
			return a.Target.Key() < b.Target.Key()
		}
		if a.Allocation != b.Allocation {
			return a.Allocation < b.Allocation
		}
		return pathKey(a.ReceiverPath) < pathKey(b.ReceiverPath)
	})
	out.Ambiguities = dedupeAmbiguities(out.Ambiguities)
	sortAmbiguities(out.Ambiguities)
	out.Ambiguous = len(out.Ambiguities) != 0
	out.NonVirtual = allNonVirtual && len(out.Targets) != 0
	return out
}

func (e *engine) callReceivers(call VirtualCall, method string) ([]ref, []Ambiguity) {
	receiver := firstNonEmpty(call.Receiver, call.Value)
	var candidates []ref
	var ambiguities []Ambiguity
	selector := call.Path
	if len(selector) == 0 && call.PathString != "" {
		selector = splitSelector(call.PathString)
	}

	if receiver != "" {
		if call.Field != "" {
			candidates = e.loadFromRefs(e.refsForValue(receiver), call.Field, "", selector)
		} else if left, field, ok := splitFieldExpr(receiver); ok && len(selector) != 0 {
			candidates = e.loadFromRefs(e.refsForValue(left), field, "", selector)
		} else {
			candidates = e.refsForValue(receiver)
		}
	}
	if len(candidates) == 0 && call.Object != "" {
		objects := e.refsForValue(call.Object)
		if call.Field != "" {
			candidates = e.loadFromRefs(objects, call.Field, "", selector)
		} else {
			candidates = objects
		}
	}
	if len(candidates) == 0 && call.StaticType != "" {
		candidates = e.refsForClass(call.StaticType)
	}
	if len(candidates) == 0 && call.StaticType == "" {
		for _, alloc := range e.allocs {
			candidates = append(candidates, sortedRefs(e.values[alloc.ID])...)
		}
	}

	if call.StaticType != "" {
		if len(selector) != 0 {
			filtered := candidates[:0]
			for _, candidate := range candidates {
				if candidate.staticClass() == call.StaticType && !matchPathSelector(normalizeSelector(candidate.dynamic, selector), candidate.dynamic, candidate.path) {
					continue
				}
				filtered = append(filtered, candidate)
			}
			candidates = filtered
		}
		var converted []ref
		for _, candidate := range candidates {
			values, ambs, err := e.convertRef(candidate, call.StaticType, selector, TraceStep{
				Kind:  "upcast",
				ID:    "implicit:" + call.ID,
				Site:  call.Site,
				Class: call.StaticType,
			})
			if err != nil {
				e.addDiagnostic(fmt.Sprintf("call %s: %v", call.ID, err))
				continue
			}
			converted = append(converted, values...)
			for _, ambiguity := range ambs {
				ambiguity.CallID = call.ID
				ambiguities = append(ambiguities, ambiguity)
			}
		}
		candidates = converted
	}

	seen := make(map[string]ref)
	for _, candidate := range candidates {
		seen[candidate.key()] = candidate
	}
	out := make([]ref, 0, len(seen))
	for _, candidate := range seen {
		out = append(out, candidate)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key() < out[j].key() })
	if call.StaticType != "" && len(call.Path) == 0 && call.PathString == "" {
		byAllocation := make(map[string]map[string]bool)
		for _, candidate := range out {
			if byAllocation[candidate.allocID] == nil {
				byAllocation[candidate.allocID] = make(map[string]bool)
			}
			byAllocation[candidate.allocID][candidate.pathKey()] = true
		}
		for allocation, paths := range byAllocation {
			if len(paths) <= 1 {
				continue
			}
			ambiguities = append(ambiguities, Ambiguity{
				Kind:        "conversion",
				CallID:      call.ID,
				DynamicType: firstDynamicType(out, allocation),
				StaticType:  call.StaticType,
				Method:      callMethodKey(call),
				Reason:      fmt.Sprintf("allocation %s has multiple non-virtual subobjects of %s; select a path", allocation, call.StaticType),
			})
		}
	}
	return out, dedupeAmbiguities(ambiguities)
}

func firstDynamicType(values []ref, allocation string) string {
	for _, value := range values {
		if value.allocID == allocation {
			return value.dynamic
		}
	}
	return ""
}

func (e *engine) validateCall(call VirtualCall, candidate CallResult) Validation {
	check := Validation{OK: true}
	// The finite executor materializes every allocation and every subobject
	// path.  Re-running resolution from that finite receiver set catches an
	// aggregation error or an omitted implicit upcast without using any
	// unbounded/recursive class-space approximation.
	refs, _ := e.callReceivers(call, candidate.Method)
	check.ReceiverCount = len(refs)
	expected := make(map[string]Target)
	for _, value := range refs {
		targets, _, _, found := e.g.resolveAt(value.dynamic, value.path, value.staticClass(), callMethodKey(call))
		if !found {
			continue
		}
		for _, target := range targets {
			expected[target.Key()] = target
		}
	}
	for _, target := range expected {
		check.Expected = append(check.Expected, target)
	}
	sortTargets(check.Expected)
	candidateSet := targetKeys(candidate.Targets)
	expectedSet := targetKeys(check.Expected)
	for key, target := range expected {
		if !candidateSet[key] {
			check.Missing = append(check.Missing, target)
		}
	}
	for _, target := range candidate.Targets {
		if !expectedSet[target.Key()] {
			check.Spurious = append(check.Spurious, target)
		}
	}
	sortTargets(check.Missing)
	sortTargets(check.Spurious)
	check.OK = len(check.Missing) == 0
	if !check.OK {
		check.Messages = append(check.Messages, fmt.Sprintf("call %s missed %d target(s)", call.ID, len(check.Missing)))
	}
	if len(candidate.Targets) == 0 && candidate.Error != "" {
		check.OK = true
	}
	return check
}

func callMethodKey(call VirtualCall) string {
	return firstNonEmpty(call.Signature, call.Method, call.Name)
}

func uniqueTargets(targets []Target) []Target {
	seen := make(map[string]bool)
	out := make([]Target, 0, len(targets))
	for _, target := range targets {
		if seen[target.Key()] {
			continue
		}
		seen[target.Key()] = true
		out = append(out, target)
	}
	sortTargets(out)
	return out
}

func differenceTargets(left, right []Target) []Target {
	rightSet := targetKeys(right)
	var out []Target
	for _, target := range left {
		if !rightSet[target.Key()] {
			out = append(out, target)
		}
	}
	sortTargets(out)
	return out
}

func dedupeAmbiguities(items []Ambiguity) []Ambiguity {
	seen := make(map[string]bool)
	out := make([]Ambiguity, 0, len(items))
	for _, item := range items {
		key := item.Kind + "|" + item.CallID + "|" + item.DynamicType + "|" + item.StaticType + "|" + item.Method + "|" + item.Field + "|" + item.Slot + "|" + item.Reason + "|" + pathKey(item.Path)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	return out
}
