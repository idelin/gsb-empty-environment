package netsec

type variable struct {
	name      string
	sensitive bool
	segments  []segment
	// origin classifies every literal byte of this variable. Inherited raw
	// values carry an untrusted origin and a single literal segment.
	origin Origin
}

// Builder accumulates variable definitions and an import allowlist, then
// resolves them into a provenance-tagged environment. A Builder is not safe
// for concurrent use.
type Builder struct {
	vars   map[string]*variable
	order  []string
	budget Budget
}

// NewBuilder returns an empty Builder using DefaultBudget.
func NewBuilder() *Builder {
	return &Builder{
		vars:   make(map[string]*variable),
		budget: DefaultBudget,
	}
}

// SetBudget replaces the expansion budget. Non-positive limits are rejected.
func (b *Builder) SetBudget(budget Budget) error {
	if budget.MaxOutputBytes <= 0 || budget.MaxDepth <= 0 {
		return &BudgetError{Reason: "budget limits must be positive"}
	}
	b.budget = budget
	return nil
}

// Define registers a variable from a trusted template. Invalid names,
// malformed templates and duplicate definitions are rejected.
func (b *Builder) Define(spec Spec) error {
	if !isValidName(spec.Name) {
		return &NameError{Name: spec.Name}
	}
	if _, exists := b.vars[spec.Name]; exists {
		return &DuplicateError{Name: spec.Name}
	}
	segments, err := parseTemplate(spec.Name, spec.Template)
	if err != nil {
		return err
	}
	b.vars[spec.Name] = &variable{
		name:      spec.Name,
		sensitive: spec.Sensitive,
		segments:  segments,
		origin:    Origin{Source: Trusted, Name: spec.Name},
	}
	b.order = append(b.order, spec.Name)
	return nil
}

// Import copies allowlisted variables from an ambient environment (for
// example os.Environ). Imported values are always untrusted and are stored as
// opaque literals: any "${...}" text inside them is never interpreted.
//
// Entries use the conventional "KEY=VALUE" form; an entry without '=' is
// rejected. Variables absent from env are silently skipped. Names already
// registered are rejected so an inherited value cannot shadow a launcher one.
func (b *Builder) Import(env []string, allowlist []string) error {
	ambient := make(map[string]string, len(env))
	for _, entry := range env {
		key, value, ok := splitEnvEntry(entry)
		if !ok {
			return &ImportError{Entry: entry}
		}
		ambient[key] = value
	}
	for _, name := range allowlist {
		if !isValidName(name) {
			return &ImportError{Entry: name}
		}
		value, present := ambient[name]
		if !present {
			continue
		}
		if _, exists := b.vars[name]; exists {
			return &DuplicateError{Name: name}
		}
		b.vars[name] = &variable{
			name: name,
			segments: []segment{{
				literal: true,
				text:    value,
			}},
			origin: Origin{Source: Untrusted, Name: name},
		}
		b.order = append(b.order, name)
	}
	return nil
}

// Environment is the resolved, provenance-tagged result of Build.
type Environment struct {
	values map[string]Value
	order  []string
	stats  Stats
}

// Get returns the expanded value of a variable.
func (e *Environment) Get(name string) (Value, bool) {
	value, ok := e.values[name]
	return value, ok
}

// OrderedNames returns variable names in definition/import order.
func (e *Environment) OrderedNames() []string {
	names := make([]string, len(e.order))
	copy(names, e.order)
	return names
}

// Env renders "KEY=VALUE" lines in definition/import order, ready for
// exec.Cmd.Env.
func (e *Environment) Env() []string {
	out := make([]string, 0, len(e.order))
	for _, name := range e.order {
		out = append(out, name+"="+e.values[name].Data)
	}
	return out
}

// Stats reports the expansion work consumed by Build.
func (e *Environment) Stats() Stats {
	return e.stats
}

type buildState struct {
	builder *Builder
	cache   map[string]Value
	onStack map[string]bool
	stats   Stats
	budget  Budget
	err     error
}

// Build validates the dependency graph and expands every registered variable.
// It fails on undefined references, reference cycles, sensitive values that
// incorporate untrusted bytes, or budget exhaustion.
func (b *Builder) Build() (*Environment, error) {
	if err := b.checkReferences(); err != nil {
		return nil, err
	}
	if err := b.checkCycles(); err != nil {
		return nil, err
	}
	maxDepth, err := b.maxReferenceDepth()
	if err != nil {
		return nil, err
	}

	state := &buildState{
		builder: b,
		cache:   make(map[string]Value),
		onStack: make(map[string]bool),
		budget:  b.budget,
	}
	state.stats.MaxDepthObserved = maxDepth

	env := &Environment{
		values: make(map[string]Value, len(b.order)),
		order:  make([]string, len(b.order)),
	}
	copy(env.order, b.order)

	for _, name := range b.order {
		value, err := state.expand(name)
		if err != nil {
			return nil, err
		}
		env.values[name] = value
	}

	env.stats = state.stats
	return env, nil
}

func (s *buildState) expand(name string) (Value, error) {
	if s.err != nil {
		return Value{}, s.err
	}
	if value, ok := s.cache[name]; ok {
		s.stats.CacheHits++
		return value, nil
	}

	// Defensive guard: Build pre-validates cycles before any expansion.
	if s.onStack[name] {
		s.err = &CycleError{Path: []string{name}}
		return Value{}, s.err
	}
	s.onStack[name] = true
	defer delete(s.onStack, name)

	variable := s.builder.vars[name]
	var runs []Run

	appendBytes := func(origin Origin, data string) error {
		if data == "" {
			return nil
		}
		s.stats.OutputBytes += len(data)
		if s.stats.OutputBytes > s.budget.MaxOutputBytes {
			s.err = &BudgetExceededError{
				Kind:  "output bytes",
				Limit: s.budget.MaxOutputBytes,
				Name:  name,
			}
			return s.err
		}
		if len(runs) > 0 && runs[len(runs)-1].Origin == origin {
			runs[len(runs)-1].Data += data
		} else {
			runs = append(runs, Run{Origin: origin, Data: data})
		}
		return nil
	}

	for _, seg := range variable.segments {
		if seg.literal {
			if err := appendBytes(variable.origin, seg.text); err != nil {
				return Value{}, err
			}
			continue
		}
		child, err := s.expand(seg.refName)
		if err != nil {
			return Value{}, err
		}
		for _, run := range child.Runs {
			if err := appendBytes(run.Origin, run.Data); err != nil {
				return Value{}, err
			}
		}
	}

	value := Value{Name: name, Runs: runs}
	for _, run := range runs {
		value.Data += run.Data
	}

	if variable.sensitive && !value.AllTrusted() {
		s.err = &SensitiveError{Name: name}
		return Value{}, s.err
	}

	s.cache[name] = value
	s.stats.Expansions++
	return value, nil
}

func (b *Builder) checkReferences() error {
	for _, name := range b.order {
		for _, seg := range b.vars[name].segments {
			if seg.literal {
				continue
			}
			if _, ok := b.vars[seg.refName]; !ok {
				return &UndefinedError{Ref: seg.refName, In: name}
			}
		}
	}
	return nil
}

// maxReferenceDepth returns the longest reference chain in the dependency
// graph measured in variables (a variable with no references has depth 1).
// Shared sub-expressions are memoized so diamond-shaped graphs stay linear.
// The graph is already known to be acyclic at this point.
func (b *Builder) maxReferenceDepth() (int, error) {
	depths := make(map[string]int, len(b.vars))
	var visit func(name string) (int, error)
	visit = func(name string) (int, error) {
		if depth, ok := depths[name]; ok {
			return depth, nil
		}
		deepest := 1
		for _, seg := range b.vars[name].segments {
			if seg.literal {
				continue
			}
			childDepth, err := visit(seg.refName)
			if err != nil {
				return 0, err
			}
			if 1+childDepth > deepest {
				deepest = 1 + childDepth
			}
			if deepest > b.budget.MaxDepth {
				return 0, &BudgetExceededError{
					Kind:  "depth",
					Limit: b.budget.MaxDepth,
					Name:  name,
				}
			}
		}
		depths[name] = deepest
		return deepest, nil
	}

	maxDepth := 0
	for _, name := range b.order {
		depth, err := visit(name)
		if err != nil {
			return 0, err
		}
		if depth > maxDepth {
			maxDepth = depth
		}
	}
	return maxDepth, nil
}

func (b *Builder) checkCycles() error {
	const (
		unvisited = iota
		inProgress
		done
	)
	state := make(map[string]int, len(b.vars))

	var visit func(name string, path []string) error
	visit = func(name string, path []string) error {
		switch state[name] {
		case done:
			return nil
		case inProgress:
			for i, node := range path {
				if node == name {
					return &CycleError{Path: append(append([]string{}, path[i:]...), name)}
				}
			}
			return &CycleError{Path: []string{name, name}}
		}
		state[name] = inProgress
		path = append(path, name)
		for _, seg := range b.vars[name].segments {
			if !seg.literal {
				if err := visit(seg.refName, path); err != nil {
					return err
				}
			}
		}
		state[name] = done
		return nil
	}

	for _, name := range b.order {
		if err := visit(name, nil); err != nil {
			return err
		}
	}
	return nil
}
