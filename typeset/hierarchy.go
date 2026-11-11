package typeset

import "fmt"

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// Analyzer holds a validated inheritance graph and all derived analyses.
// It is safe for concurrent read-only use after NewAnalyzer succeeds.
type Analyzer struct {
	classes   map[string]*Class
	order     []string // class names in input order
	ancestors map[string]map[string]bool

	// subObjects maps most-derived class to its subobject table.
	subObjects map[string]*SubobjectTable

	// overrides maps most-derived class to its final-overrider table.
	overrides map[string]*OverrideTable
}

// NewAnalyzer validates the inheritance graph and builds the structural
// analyses (subobject layout and final overriders). It returns *HierarchyError
// for unknown bases, duplicate direct bases or inheritance cycles.
func NewAnalyzer(classes []Class) (*Analyzer, error) {
	a := &Analyzer{
		classes:    make(map[string]*Class, len(classes)),
		ancestors:  make(map[string]map[string]bool),
		subObjects: make(map[string]*SubobjectTable),
		overrides:  make(map[string]*OverrideTable),
	}

	for i := range classes {
		c := &classes[i]
		if c.Name == "" {
			return nil, hierarchyError("class with empty name")
		}
		if _, exists := a.classes[c.Name]; exists {
			return nil, hierarchyError("duplicate class %q", c.Name)
		}
		a.classes[c.Name] = c
		a.order = append(a.order, c.Name)
	}

	for _, name := range a.order {
		c := a.classes[name]
		seen := make(map[string]bool)
		for _, b := range c.DirectBases {
			if b.Name == "" {
				return nil, hierarchyError("class %q has an unnamed base", name)
			}
			if _, ok := a.classes[b.Name]; !ok {
				return nil, hierarchyError("class %q derives from unknown class %q", name, b.Name)
			}
			if seen[b.Name] {
				return nil, hierarchyError("class %q lists base %q more than once", name, b.Name)
			}
			seen[b.Name] = true
		}
		seenMethods := make(map[string]bool)
		for _, m := range c.Methods {
			if m.Name == "" {
				return nil, hierarchyError("class %q has a method with an empty name", name)
			}
			if seenMethods[m.Name] {
				return nil, hierarchyError("class %q declares method %q more than once", name, m.Name)
			}
			seenMethods[m.Name] = true
		}
	}

	if err := a.rejectCycles(); err != nil {
		return nil, err
	}

	for _, name := range a.order {
		a.buildAncestors(name)
	}
	return a, nil
}

// rejectCycles performs a three-color DFS over the inheritance edges.
func (a *Analyzer) rejectCycles() error {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(a.order))
	var stack []string
	var visit func(name string) error
	visit = func(name string) error {
		color[name] = gray
		stack = append(stack, name)
		for _, b := range a.classes[name].DirectBases {
			switch color[b.Name] {
			case gray:
				start := 0
				for i, n := range stack {
					if n == b.Name {
						start = i
						break
					}
				}
				cycle := append([]string(nil), stack[start:]...)
				cycle = append(cycle, b.Name)
				return hierarchyError("inheritance cycle: %v", cycle)
			case white:
				if err := visit(b.Name); err != nil {
					return err
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[name] = black
		return nil
	}
	for _, name := range a.order {
		if color[name] == white {
			if err := visit(name); err != nil {
				return err
			}
		}
	}
	return nil
}

// buildAncestors memoizes the reflexive ancestor (base) set of every class.
func (a *Analyzer) buildAncestors(name string) map[string]bool {
	if set, ok := a.ancestors[name]; ok {
		return set
	}
	set := map[string]bool{name: true}
	for _, b := range a.classes[name].DirectBases {
		for anc := range a.buildAncestors(b.Name) {
			set[anc] = true
		}
	}
	a.ancestors[name] = set
	return set
}

// IsBaseOf reports whether base is base itself or an ancestor of derived.
func (a *Analyzer) IsBaseOf(base, derived string) bool {
	set, ok := a.ancestors[derived]
	if !ok {
		return false
	}
	return set[base]
}

// ClassNames returns all class names in input order.
func (a *Analyzer) ClassNames() []string { return append([]string(nil), a.order...) }

// AnalyzeProgram validates the program's hierarchy and facts together and
// returns the pointer-flow solution with resolved virtual calls.
func AnalyzeProgram(program *Program) (*Solution, error) {
	if program == nil {
		return nil, programError("nil program")
	}
	analyzer, err := NewAnalyzer(program.Classes)
	if err != nil {
		return nil, err
	}
	return analyzer.Analyze(program.Facts)
}

// MethodDecl describes one method declaration resolved during analysis.
type MethodDecl struct {
	Class   string
	Method  string
	Virtual bool
}

func (a *Analyzer) methodDecl(class, method string) (MethodDecl, bool) {
	c, ok := a.classes[class]
	if !ok {
		return MethodDecl{}, false
	}
	for _, m := range c.Methods {
		if m.Name == method {
			return MethodDecl{Class: class, Method: method, Virtual: m.Virtual}, true
		}
	}
	return MethodDecl{}, false
}
