package netsec

import "sort"

// Table is an immutable, fully validated exception table. It is only returned
// after every function, entry, landing pad, action chain and personality has
// passed validation. On any failure the caller receives no table at all.
type Table struct {
	digest       ModuleDigest
	code         []byte
	instructions []Instruction
	functions    []compiledFunction
	entries      []compiledEntry
	actions      []ActionNode
}

// HandlerTable is an alias for Table.
type HandlerTable = Table

// ExceptionTable is an alias for Table.
type ExceptionTable = Table

type compiledFunction struct {
	fn         Function
	typeSet    map[TypeID]struct{}
	starts     map[uint64]bool // instruction start -> landing-pad registration
	boundaries map[uint64]bool
	prefixEnd  uint64
	entryIdx   []int
}

type compiledEntry struct {
	entry       ExceptionEntry
	personality Personality
	chain       []int
}

// Compile validates all untrusted metadata against the host's immutable code
// and trusted decoder output. It publishes a table only when validation is
// completely successful.
func Compile(m Module, meta Metadata, reg *PersonalityRegistry, limits ...Limits) (*Table, error) {
	lim, err := selectLimits(limits)
	if err != nil {
		return nil, err
	}
	if len(meta.Entries) > lim.MaxEntries {
		return nil, &ValidationError{
			Kind:   ErrEntryBudget,
			Detail: "exception entry budget exceeded",
		}
	}
	if len(meta.Actions) > lim.MaxActionNodes {
		return nil, &ValidationError{
			Kind:   ErrActionNodeBudget,
			Detail: "action node budget exceeded",
		}
	}

	code := append([]byte(nil), m.Code...)
	digest := ComputeModuleDigest(m)
	instrs, err := validateInstructions(code, m.Instructions)
	if err != nil {
		return nil, err
	}
	funcs, err := compileFunctions(code, instrs, m.Functions)
	if err != nil {
		return nil, err
	}
	actions := append([]ActionNode(nil), meta.Actions...)
	if err := validateActionStructure(actions); err != nil {
		return nil, err
	}

	fnByID := make(map[FunctionID]int, len(funcs))
	for i := range funcs {
		fnByID[funcs[i].fn.ID] = i
	}

	entries := make([]compiledEntry, 0, len(meta.Entries))
	for i, raw := range meta.Entries {
		fnIdx, ok := fnByID[raw.FunctionID]
		if !ok {
			return nil, &ValidationError{
				Kind:       ErrEntryFunction,
				Entry:      i,
				FunctionID: raw.FunctionID,
				Detail:     "entry references an unknown function",
			}
		}
		fn := &funcs[fnIdx]

		if raw.Start >= raw.End {
			return nil, &ValidationError{
				Kind:    ErrEntryRange,
				Entry:   i,
				Address: raw.Start,
				Detail:  "protected interval must be non-empty",
			}
		}
		if raw.Start < fn.fn.Start || raw.End > fn.fn.End {
			return nil, &ValidationError{
				Kind:    ErrEntryRange,
				Entry:   i,
				Address: raw.Start,
				Detail:  "protected interval is outside its function",
			}
		}
		if _, isStart := fn.starts[raw.Start]; !isStart {
			return nil, &ValidationError{
				Kind:    ErrEntryBoundary,
				Entry:   i,
				Address: raw.Start,
				Detail:  "protected start is not an instruction boundary",
			}
		}
		if raw.End != fn.fn.End {
			if _, isStart := fn.starts[raw.End]; !isStart {
				return nil, &ValidationError{
					Kind:    ErrEntryBoundary,
					Entry:   i,
					Address: raw.End,
					Detail:  "protected end is not an instruction boundary",
				}
			}
		}

		landing, registered := fn.starts[raw.LandingPad]
		if raw.LandingPad < fn.fn.Start || raw.LandingPad >= fn.fn.End {
			return nil, &ValidationError{
				Kind:    ErrLandingPadRange,
				Entry:   i,
				Address: raw.LandingPad,
				Detail:  "landing pad is outside its function",
			}
		}
		if !registered || !landing {
			return nil, &ValidationError{
				Kind:    ErrLandingPadInstruction,
				Entry:   i,
				Address: raw.LandingPad,
				Detail:  "landing pad is not a registered landing instruction start",
			}
		}
		if raw.LandingPad >= raw.Start && raw.LandingPad < raw.End {
			return nil, &ValidationError{
				Kind:    ErrLandingPadProtected,
				Entry:   i,
				Address: raw.LandingPad,
				Detail:  "landing pad lies inside the protected interval",
			}
		}
		if raw.LandingPad == fn.fn.Start || raw.LandingPad < fn.prefixEnd {
			return nil, &ValidationError{
				Kind:    ErrLandingPadEntryPrefix,
				Entry:   i,
				Address: raw.LandingPad,
				Detail:  "landing pad lies in the function entry prefix",
			}
		}

		chain, err := validateActionChain(actions, raw.ActionStart, fn.typeSet, lim.MaxActionChain, i)
		if err != nil {
			return nil, err
		}
		personality, ok := reg.Resolve(digest, raw.PersonalityID)
		if !ok {
			return nil, &ValidationError{
				Kind:       ErrPersonality,
				Entry:      i,
				FunctionID: raw.FunctionID,
				Detail:     "personality is not whitelisted for this module digest",
			}
		}

		entries = append(entries, compiledEntry{
			entry:       raw,
			personality: personality,
			chain:       chain,
		})
		fn.entryIdx = append(fn.entryIdx, i)
	}

	if err := validateNesting(entries, funcs); err != nil {
		return nil, err
	}

	return &Table{
		digest:       digest,
		code:         code,
		instructions: instrs,
		functions:    funcs,
		entries:      entries,
		actions:      actions,
	}, nil
}

// Validate is an alias for Compile.
func Validate(m Module, meta Metadata, reg *PersonalityRegistry, limits ...Limits) (*Table, error) {
	return Compile(m, meta, reg, limits...)
}

// Build is an alias for Compile.
func Build(m Module, meta Metadata, reg *PersonalityRegistry, limits ...Limits) (*Table, error) {
	return Compile(m, meta, reg, limits...)
}

func selectLimits(limits []Limits) (Limits, error) {
	if len(limits) == 0 {
		return Limits{}.normalize()
	}
	if len(limits) > 1 {
		return Limits{}, &ValidationError{
			Kind:   ErrLimits,
			Detail: "at most one Limits value may be supplied",
		}
	}
	return limits[0].normalize()
}

func validateInstructions(code []byte, input []Instruction) ([]Instruction, error) {
	instrs := append([]Instruction(nil), input...)
	sort.SliceStable(instrs, func(i, j int) bool {
		if instrs[i].Start != instrs[j].Start {
			return instrs[i].Start < instrs[j].Start
		}
		return instrs[i].End < instrs[j].End
	})
	for i, ins := range instrs {
		if ins.Start >= ins.End {
			return nil, &ValidationError{
				Kind:    ErrInstructionRange,
				Address: ins.Start,
				Detail:  "instruction interval must be non-empty",
			}
		}
		if ins.End > uint64(len(code)) {
			return nil, &ValidationError{
				Kind:    ErrInstructionRange,
				Address: ins.End,
				Detail:  "instruction extends past the immutable code",
			}
		}
		if i > 0 && instrs[i-1].End > ins.Start {
			return nil, &ValidationError{
				Kind:    ErrInstructionOverlap,
				Address: ins.Start,
				Detail:  "instruction intervals overlap",
			}
		}
	}
	return instrs, nil
}

func compileFunctions(code []byte, instrs []Instruction, input []Function) ([]compiledFunction, error) {
	fns := append([]Function(nil), input...)
	sort.SliceStable(fns, func(i, j int) bool {
		if fns[i].Start != fns[j].Start {
			return fns[i].Start < fns[j].Start
		}
		return fns[i].ID < fns[j].ID
	})
	seenID := make(map[FunctionID]struct{}, len(fns))
	out := make([]compiledFunction, len(fns))
	for i, fn := range fns {
		if _, exists := seenID[fn.ID]; exists {
			return nil, &ValidationError{
				Kind:       ErrFunctionID,
				FunctionID: fn.ID,
				Detail:     "duplicate function id",
			}
		}
		seenID[fn.ID] = struct{}{}
		if fn.Start >= fn.End {
			return nil, &ValidationError{
				Kind:       ErrFunctionRange,
				FunctionID: fn.ID,
				Address:    fn.Start,
				Detail:     "function interval must be non-empty",
			}
		}
		if fn.End > uint64(len(code)) {
			return nil, &ValidationError{
				Kind:       ErrFunctionRange,
				FunctionID: fn.ID,
				Address:    fn.End,
				Detail:     "function extends past the immutable code",
			}
		}
		if i > 0 && fns[i-1].End > fn.Start {
			return nil, &ValidationError{
				Kind:       ErrFunctionOverlap,
				FunctionID: fn.ID,
				Address:    fn.Start,
				Detail:     "function intervals overlap",
			}
		}

		typeSet := make(map[TypeID]struct{}, len(fn.TypeIDs))
		typeIDs := append([]TypeID(nil), fn.TypeIDs...)
		for _, id := range typeIDs {
			typeSet[id] = struct{}{}
		}

		starts := make(map[uint64]bool)
		boundaries := make(map[uint64]bool)
		first := sort.Search(len(instrs), func(j int) bool {
			return instrs[j].Start >= fn.Start
		})
		if first == len(instrs) || instrs[first].Start != fn.Start {
			return nil, &ValidationError{
				Kind:       ErrFunctionCoverage,
				FunctionID: fn.ID,
				Address:    fn.Start,
				Detail:     "function start is not an instruction boundary",
			}
		}
		cursor := fn.Start
		j := first
		for cursor < fn.End {
			if j == len(instrs) || instrs[j].Start != cursor {
				return nil, &ValidationError{
					Kind:       ErrFunctionCoverage,
					FunctionID: fn.ID,
					Address:    cursor,
					Detail:     "function interval has an instruction gap",
				}
			}
			if instrs[j].End > fn.End {
				return nil, &ValidationError{
					Kind:       ErrFunctionCoverage,
					FunctionID: fn.ID,
					Address:    instrs[j].End,
					Detail:     "instruction crosses the function boundary",
				}
			}
			starts[cursor] = instrs[j].LandingPad
			boundaries[cursor] = true
			cursor = instrs[j].End
			boundaries[cursor] = true
			j++
		}
		if cursor != fn.End {
			return nil, &ValidationError{
				Kind:       ErrFunctionCoverage,
				FunctionID: fn.ID,
				Address:    cursor,
				Detail:     "function is not completely covered by instructions",
			}
		}

		prefixEnd := fn.EntryPrefixEnd
		if prefixEnd == 0 {
			prefixEnd = fn.Start
		}
		if prefixEnd < fn.Start || prefixEnd > fn.End {
			return nil, &ValidationError{
				Kind:       ErrFunctionPrefix,
				FunctionID: fn.ID,
				Address:    prefixEnd,
				Detail:     "entry-prefix end is outside the function",
			}
		}
		if prefixEnd != fn.Start && prefixEnd != fn.End && !boundaries[prefixEnd] {
			return nil, &ValidationError{
				Kind:       ErrFunctionPrefix,
				FunctionID: fn.ID,
				Address:    prefixEnd,
				Detail:     "entry-prefix end is not an instruction boundary",
			}
		}

		fnCopy := fn
		fnCopy.TypeIDs = typeIDs
		out[i] = compiledFunction{
			fn:         fnCopy,
			typeSet:    typeSet,
			starts:     starts,
			boundaries: boundaries,
			prefixEnd:  prefixEnd,
		}
	}
	return out, nil
}

func validateActionStructure(actions []ActionNode) error {
	for i, node := range actions {
		if node.Next == NoAction {
			continue
		}
		if node.Next < 0 || node.Next >= len(actions) {
			return &ValidationError{
				Kind:   ErrActionIndex,
				Action: i,
				Detail: "action next index is out of range",
			}
		}
		if node.Next <= i {
			return &ValidationError{
				Kind:   ErrActionCycle,
				Action: i,
				Detail: "action next index must be strictly greater",
			}
		}
	}
	return nil
}

func validateActionChain(actions []ActionNode, start int, types map[TypeID]struct{}, budget, entry int) ([]int, error) {
	if start == NoAction {
		return nil, nil
	}
	if start < 0 || start >= len(actions) {
		return nil, &ValidationError{
			Kind:   ErrActionIndex,
			Entry:  entry,
			Action: start,
			Detail: "action chain start is out of range",
		}
	}

	chain := make([]int, 0, 8)
	idx := start
	for idx != NoAction {
		if len(chain) >= budget {
			return nil, &ValidationError{
				Kind:   ErrActionBudget,
				Entry:  entry,
				Action: idx,
				Detail: "action chain exceeds its length budget",
			}
		}
		node := actions[idx]
		if _, ok := types[node.TypeID]; !ok {
			return nil, &ValidationError{
				Kind:   ErrActionType,
				Entry:  entry,
				Action: idx,
				Detail: "action type is absent from the function type table",
			}
		}
		if node.Next != NoAction && node.Next <= idx {
			return nil, &ValidationError{
				Kind:   ErrActionCycle,
				Entry:  entry,
				Action: idx,
				Detail: "action chain is not strictly increasing",
			}
		}
		chain = append(chain, idx)
		idx = node.Next
	}
	return chain, nil
}

func validateNesting(entries []compiledEntry, funcs []compiledFunction) error {
	for fi := range funcs {
		group := append([]int(nil), funcs[fi].entryIdx...)
		sort.SliceStable(group, func(i, j int) bool {
			a := entries[group[i]].entry
			b := entries[group[j]].entry
			if a.Start != b.Start {
				return a.Start < b.Start
			}
			if a.End != b.End {
				return a.End > b.End
			}
			return group[i] < group[j]
		})

		type activeRange struct {
			entry int
			end   uint64
		}
		stack := make([]activeRange, 0, 8)
		for _, ei := range group {
			e := entries[ei].entry
			for len(stack) > 0 && stack[len(stack)-1].end <= e.Start {
				stack = stack[:len(stack)-1]
			}
			if len(stack) > 0 && e.End > stack[len(stack)-1].end {
				return &ValidationError{
					Kind:    ErrEntryOverlap,
					Entry:   ei,
					Address: e.Start,
					Detail:  "protected intervals cross instead of nesting",
				}
			}
			stack = append(stack, activeRange{entry: ei, end: e.End})
		}
	}
	return nil
}

// Digest returns the module digest to which this table is bound.
func (t *Table) Digest() ModuleDigest {
	if t == nil {
		return ModuleDigest{}
	}
	return t.digest
}

// Code returns a copy of the immutable code bytes snapshotted at compile time.
func (t *Table) Code() []byte {
	if t == nil {
		return nil
	}
	return append([]byte(nil), t.code...)
}

// Instructions returns a copy of the trusted decoder intervals snapshotted at
// compile time.
func (t *Table) Instructions() []Instruction {
	if t == nil {
		return nil
	}
	return append([]Instruction(nil), t.instructions...)
}

// Functions returns copies of the trusted function intervals snapshotted at
// compile time.
func (t *Table) Functions() []Function {
	if t == nil {
		return nil
	}
	out := make([]Function, len(t.functions))
	for i := range t.functions {
		out[i] = t.functions[i].fn
		out[i].TypeIDs = append([]TypeID(nil), t.functions[i].fn.TypeIDs...)
	}
	return out
}

// EntryCount returns the number of validated exception entries.
func (t *Table) EntryCount() int {
	if t == nil {
		return 0
	}
	return len(t.entries)
}

// Entries returns copies of the validated exception entries.
func (t *Table) Entries() []ExceptionEntry {
	if t == nil {
		return nil
	}
	out := make([]ExceptionEntry, len(t.entries))
	for i := range t.entries {
		out[i] = t.entries[i].entry
	}
	return out
}

// Entry returns a copy of one validated exception entry.
func (t *Table) Entry(i int) (ExceptionEntry, bool) {
	if t == nil || i < 0 || i >= len(t.entries) {
		return ExceptionEntry{}, false
	}
	return t.entries[i].entry, true
}

// Actions returns a copy of the action table.
func (t *Table) Actions() []ActionNode {
	if t == nil {
		return nil
	}
	return append([]ActionNode(nil), t.actions...)
}
