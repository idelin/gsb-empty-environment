package typeset

import (
	"fmt"
	"sort"
	"strings"
)

type baseEdge struct {
	class   string
	virtual bool
	index   int
}

type classNode struct {
	name    string
	bases   []baseEdge
	methods map[string]Method
	fields  map[string]Field
}

type graph struct {
	classes         map[string]*classNode
	order           []string
	ancestor        map[string]map[string]bool
	layouts         map[string]ObjectLayout
	virtual         map[string]map[string]bool
	usesVirtualFlag bool
	virtualKeys     map[string]bool
}

// Hierarchy is a validated inheritance graph with materialized object
// layouts.  It can be reused to resolve several call sets.
type Hierarchy struct {
	g *graph
}

// Resolution is the primitive result for one dynamic/static/method triple.
type Resolution struct {
	Targets     []Target
	Provenance  []Provenance
	Ambiguous   bool
	Ambiguities []Ambiguity
	NonVirtual  bool
}

// BuildHierarchy validates the inheritance graph.  Inheritance cycles are
// rejected with a *CycleError.
func BuildHierarchy(classes []Class) (*Hierarchy, error) {
	g, err := newGraph(classes)
	if err != nil {
		return nil, err
	}
	return &Hierarchy{g: g}, nil
}

// NewHierarchy is an alias for BuildHierarchy.
func NewHierarchy(classes []Class) (*Hierarchy, error) { return BuildHierarchy(classes) }

// Classes returns the class names in deterministic declaration order.
func (h *Hierarchy) Classes() []string {
	if h == nil || h.g == nil {
		return nil
	}
	return append([]string(nil), h.g.order...)
}

// Layout returns the finite subobject layout for a dynamic class.
func (h *Hierarchy) Layout(dynamic string) (ObjectLayout, error) {
	if h == nil || h.g == nil {
		return ObjectLayout{}, ErrInvalidModel
	}
	layout, ok := h.g.layouts[dynamic]
	if !ok {
		return ObjectLayout{}, fmt.Errorf("%w: %s", ErrUnknownClass, dynamic)
	}
	return cloneLayout(layout), nil
}

// Layouts returns every materialized object layout.
func (h *Hierarchy) Layouts() map[string]ObjectLayout {
	out := make(map[string]ObjectLayout)
	if h == nil || h.g == nil {
		return out
	}
	for name, layout := range h.g.layouts {
		out[name] = cloneLayout(layout)
	}
	return out
}

// Resolve resolves a call through the given static class on a complete object
// of dynamic type.  If path is non-empty it selects one subobject path from
// dynamic to static.  Without a path, every valid path is considered and a
// repeated non-virtual base is reported as ambiguous.
func (h *Hierarchy) Resolve(dynamic, static, method string, path ...string) (Resolution, error) {
	if h == nil || h.g == nil {
		return Resolution{}, ErrInvalidModel
	}
	if !h.g.hasClass(dynamic) {
		return Resolution{}, fmt.Errorf("%w: %s", ErrUnknownClass, dynamic)
	}
	if static == "" {
		static = dynamic
	}
	if !h.g.hasClass(static) {
		return Resolution{}, fmt.Errorf("%w: %s", ErrUnknownClass, static)
	}
	refs, ambiguities, err := h.g.refsForStaticClass(dynamic, static, method, path)
	if err != nil {
		return Resolution{}, err
	}
	out := Resolution{Ambiguities: append([]Ambiguity(nil), ambiguities...)}
	seen := map[string]Target{}
	for _, ref := range refs {
		targets, ambs, nonVirtual, found := h.g.resolveAt(ref.dynamic, ref.path, ref.staticClass(), method)
		if !found {
			return Resolution{}, fmt.Errorf("%w: %s::%s", ErrUnknownMethod, static, method)
		}
		out.NonVirtual = out.NonVirtual || nonVirtual
		for _, target := range targets {
			if _, ok := seen[target.Key()]; ok {
				continue
			}
			seen[target.Key()] = target
			out.Targets = append(out.Targets, target)
			out.Provenance = append(out.Provenance, h.g.provenanceFor(ref, target, ""))
		}
		out.Ambiguities = append(out.Ambiguities, ambs...)
	}
	sortTargets(out.Targets)
	sortAmbiguities(out.Ambiguities)
	out.Ambiguous = len(out.Ambiguities) != 0
	return out, nil
}

// ResolveVirtual resolves a call through the most-derived static view.
func (h *Hierarchy) ResolveVirtual(dynamic, method string) (Resolution, error) {
	return h.Resolve(dynamic, dynamic, method)
}

func newGraph(classes []Class) (*graph, error) {
	g := &graph{
		classes:     make(map[string]*classNode, len(classes)),
		ancestor:    make(map[string]map[string]bool, len(classes)),
		layouts:     make(map[string]ObjectLayout, len(classes)),
		virtual:     make(map[string]map[string]bool, len(classes)),
		virtualKeys: make(map[string]bool),
	}
	for i, class := range classes {
		name := strings.TrimSpace(class.Name)
		if name == "" {
			return nil, fmt.Errorf("%w: class %d has no name", ErrInvalidModel, i)
		}
		if _, exists := g.classes[name]; exists {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateClass, name)
		}
		node := &classNode{
			name:    name,
			methods: make(map[string]Method),
			fields:  make(map[string]Field),
		}
		bases := class.Bases
		if len(bases) == 0 {
			bases = class.BaseClasses
		}
		for _, base := range bases {
			baseName := strings.TrimSpace(base.Class)
			if baseName == "" {
				baseName = strings.TrimSpace(base.Name)
			}
			if baseName == "" {
				return nil, fmt.Errorf("%w: %s has an unnamed base", ErrInvalidModel, name)
			}
			node.bases = append(node.bases, baseEdge{
				class:   baseName,
				virtual: base.Virtual,
				index:   len(node.bases),
			})
		}
		for _, method := range class.Methods {
			key := method.key()
			if key == "" {
				return nil, fmt.Errorf("%w: %s has an unnamed method", ErrInvalidModel, name)
			}
			if _, exists := node.methods[key]; exists {
				return nil, fmt.Errorf("%w: %s declares method %s twice", ErrInvalidModel, name, key)
			}
			node.methods[key] = method
			if method.Virtual {
				g.usesVirtualFlag = true
				g.virtualKeys[key] = true
			}
			if method.NonVirtual || method.Static || !method.virtual() {
				g.usesVirtualFlag = true
			}
		}
		for _, field := range class.Fields {
			fieldName := strings.TrimSpace(field.Name)
			if fieldName == "" {
				return nil, fmt.Errorf("%w: %s has an unnamed field", ErrInvalidModel, name)
			}
			if _, exists := node.fields[fieldName]; exists {
				return nil, fmt.Errorf("%w: %s declares field %s twice", ErrInvalidModel, name, fieldName)
			}
			node.fields[fieldName] = field
		}
		g.classes[name] = node
		g.order = append(g.order, name)
	}

	for _, name := range g.order {
		node := g.classes[name]
		for _, base := range node.bases {
			if !g.hasClass(base.class) {
				return nil, fmt.Errorf("%w: %s inherits missing class %s", ErrInvalidModel, name, base.class)
			}
		}
	}
	if err := g.detectCycles(); err != nil {
		return nil, err
	}
	g.buildAncestors()
	g.buildLayouts()
	return g, nil
}

func (g *graph) hasClass(name string) bool {
	_, ok := g.classes[name]
	return ok
}

func (g *graph) methodVirtual(class, key string) bool {
	node := g.classes[class]
	if node == nil {
		return false
	}
	method, ok := node.methods[key]
	if !ok {
		return false
	}
	if method.NonVirtual || method.Static {
		return false
	}
	switch method.Kind {
	case "nonvirtual", "non-virtual", "static", "non_virtual":
		return false
	}
	if method.Virtual {
		return true
	}
	if !g.usesVirtualFlag {
		return true
	}
	return g.virtualKeys[key]
}

func (g *graph) detectCycles() error {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(g.order))
	stack := make([]string, 0, len(g.order))
	var visit func(string) error
	visit = func(name string) error {
		color[name] = gray
		stack = append(stack, name)
		for _, base := range g.classes[name].bases {
			switch color[base.class] {
			case gray:
				start := 0
				for i, item := range stack {
					if item == base.class {
						start = i
						break
					}
				}
				cycle := append([]string(nil), stack[start:]...)
				cycle = append(cycle, base.class)
				return &CycleError{Cycle: cycle}
			case white:
				if err := visit(base.class); err != nil {
					return err
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[name] = black
		return nil
	}
	for _, name := range g.order {
		if color[name] == white {
			if err := visit(name); err != nil {
				return err
			}
		}
	}
	return nil
}

func (g *graph) buildAncestors() {
	// The graph is acyclic, so declaration order need not be topological.
	memo := make(map[string]map[string]bool, len(g.order))
	var add func(string) map[string]bool
	add = func(name string) map[string]bool {
		if set, ok := memo[name]; ok {
			return set
		}
		set := map[string]bool{name: true}
		for _, base := range g.classes[name].bases {
			for ancestor := range add(base.class) {
				set[ancestor] = true
			}
		}
		memo[name] = set
		return set
	}
	for _, name := range g.order {
		g.ancestor[name] = add(name)
	}
}

func (g *graph) isAncestor(ancestor, descendant string) bool {
	set, ok := g.ancestor[descendant]
	return ok && set[ancestor]
}

func (g *graph) buildLayouts() {
	for _, name := range g.order {
		layout := ObjectLayout{DynamicType: name}
		seenVirtual := make(map[string]bool)
		var walk func(parent Path, parentClass string)
		walk = func(parent Path, parentClass string) {
			node := g.classes[parentClass]
			counts := map[string]int{}
			for _, base := range node.bases {
				counts[base.class]++
			}
			occurrences := map[string]int{}
			for _, base := range node.bases {
				occurrence := occurrences[base.class]
				occurrences[base.class]++
				step := Step{
					Class:      base.class,
					Name:       base.class,
					Virtual:    base.virtual,
					Index:      base.index,
					Occurrence: occurrence,
					Repeated:   counts[base.class] > 1,
				}
				child := appendPath(parent, step)
				identity := ""
				if base.virtual {
					identity = "virtual:" + base.class
					if g.virtual[name] == nil {
						g.virtual[name] = map[string]bool{}
					}
					g.virtual[name][base.class] = true
				} else {
					identity = "path:" + pathKey(child)
				}
				layout.Subobjects = append(layout.Subobjects, Subobject{
					Class:      base.class,
					Identity:   identity,
					Path:       child.Clone(),
					PathString: pathDisplay(name, child),
					Virtual:    pathHasVirtual(child),
				})
				if base.virtual && seenVirtual[base.class] {
					continue
				}
				if base.virtual {
					seenVirtual[base.class] = true
				}
				walk(child, base.class)
			}
		}
		layout.Subobjects = append(layout.Subobjects, Subobject{
			Class:      name,
			Identity:   "root:" + name,
			PathString: name,
		})
		walk(nil, name)
		layout.Subobjects = uniqueSubobjects(layout.Subobjects)
		g.layouts[name] = layout
	}
}

func uniqueSubobjects(subobjects []Subobject) []Subobject {
	seen := make(map[string]bool, len(subobjects))
	out := make([]Subobject, 0, len(subobjects))
	for _, subobject := range subobjects {
		if seen[subobject.Identity] {
			continue
		}
		seen[subobject.Identity] = true
		out = append(out, subobject)
	}
	return out
}

func cloneLayout(layout ObjectLayout) ObjectLayout {
	out := layout
	out.Subobjects = append([]Subobject(nil), layout.Subobjects...)
	for i := range out.Subobjects {
		out.Subobjects[i].Path = out.Subobjects[i].Path.Clone()
	}
	return out
}

func appendPath(path Path, step Step) Path {
	out := make(Path, len(path)+1)
	copy(out, path)
	out[len(path)] = step
	return out
}

func appendPaths(a, b Path) Path {
	if len(a) == 0 {
		return b.Clone()
	}
	if len(b) == 0 {
		return a.Clone()
	}
	out := make(Path, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	return out
}

func pathKey(path Path) string {
	var b strings.Builder
	for _, step := range path {
		fmt.Fprintf(&b, "%d:%s:%t;", step.Index, step.ClassName(), step.Virtual)
	}
	return b.String()
}

func pathClasses(root string, path Path) []string {
	out := make([]string, 0, len(path)+1)
	if root != "" {
		out = append(out, root)
	}
	for _, step := range path {
		out = append(out, step.ClassName())
	}
	return out
}

func pathDisplay(root string, path Path) string {
	parts := make([]string, 0, len(path)+1)
	if root != "" {
		parts = append(parts, root)
	}
	for _, step := range path {
		parts = append(parts, pathStepName(step))
	}
	return strings.Join(parts, "::")
}

// localPaths returns all inheritance paths from a class to an ancestor class.
func (g *graph) localPaths(from, to string) []Path {
	if from == to {
		return []Path{nil}
	}
	node := g.classes[from]
	if node == nil {
		return nil
	}
	counts := make(map[string]int, len(node.bases))
	for _, base := range node.bases {
		counts[base.class]++
	}
	occurrences := make(map[string]int, len(node.bases))
	var out []Path
	for _, base := range node.bases {
		if !g.isAncestor(to, base.class) {
			continue
		}
		occurrence := occurrences[base.class]
		occurrences[base.class]++
		step := Step{
			Class:      base.class,
			Name:       base.class,
			Virtual:    base.virtual,
			Index:      base.index,
			Occurrence: occurrence,
			Repeated:   counts[base.class] > 1,
		}
		for _, tail := range g.localPaths(base.class, to) {
			path := make(Path, 0, len(tail)+1)
			path = append(path, step)
			path = append(path, tail...)
			out = append(out, path)
		}
	}
	return out
}

func (g *graph) isVirtualBaseIn(dynamic, class string) bool {
	if dynamic == class {
		return false
	}
	return g.virtual[dynamic] != nil && g.virtual[dynamic][class]
}

func (g *graph) subobjectIdentity(dynamic string, path Path) string {
	if len(path) == 0 {
		return "root:" + dynamic
	}
	for i := len(path) - 1; i >= 0; i-- {
		if !path[i].Virtual {
			continue
		}
		return "virtual:" + path[i].ClassName() + "|" + pathKey(path[i+1:])
	}
	return "path:" + pathKey(path)
}

func pathHasVirtual(path Path) bool {
	for _, step := range path {
		if step.Virtual {
			return true
		}
	}
	return false
}

func (g *graph) targetFor(class, key string) (Target, bool) {
	node := g.classes[class]
	if node == nil {
		return Target{}, false
	}
	method, ok := node.methods[key]
	if !ok {
		return Target{}, false
	}
	methodName := method.Name
	if methodName == "" {
		methodName = method.Signature
	}
	return Target{
		Class:      class,
		Method:     methodName,
		Signature:  method.Signature,
		NonVirtual: !g.methodVirtual(class, key),
	}, true
}

func sortTargets(targets []Target) {
	sort.Slice(targets, func(i, j int) bool { return targets[i].Key() < targets[j].Key() })
}

func sortAmbiguities(items []Ambiguity) {
	for i := range items {
		if len(items[i].Targets) == 0 && len(items[i].Overriders) != 0 {
			items[i].Targets = append([]Target(nil), items[i].Overriders...)
		}
		if len(items[i].Candidates) == 0 && len(items[i].Overriders) != 0 {
			items[i].Candidates = append([]Target(nil), items[i].Overriders...)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.CallID != b.CallID {
			return a.CallID < b.CallID
		}
		if a.Method != b.Method {
			return a.Method < b.Method
		}
		if a.Field != b.Field {
			return a.Field < b.Field
		}
		return a.Reason < b.Reason
	})
}

func targetKeys(targets []Target) map[string]bool {
	out := make(map[string]bool, len(targets))
	for _, target := range targets {
		out[target.Key()] = true
	}
	return out
}
