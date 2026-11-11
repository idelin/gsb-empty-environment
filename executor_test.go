package netsec

import (
	"sort"
	"testing"
)

// executor mirrors GraphQL execution semantics against the finite fixture
// schema without touching real data. Every resolver invocation runs the
// (instrumented) callback exactly once and contributes its weighted cost.
// List fields produce exactly the lengths assigned for the run, so
// enumerating the length assignments explores every possible execution shape.
type executor struct {
	schema    *Schema
	doc       *Document
	variables map[string]any
	// lengths maps "TypeName.field" to the chosen size of every list
	// dimension for that field (the same size is used for each dimension),
	// and "abstract:TypeName" to the chosen concrete branch index.
	lengths   map[string]int
	callbacks map[string]int
}

func newExecutor(schema *Schema, doc *Document, lengths map[string]int) *executor {
	return &executor{
		schema:    schema,
		doc:       doc,
		variables: map[string]any{},
		lengths:   lengths,
		callbacks: map[string]int{},
	}
}

// totalCallbacks is the raw number of field resolver invocations.
func (e *executor) totalCallbacks() int {
	total := 0
	for _, count := range e.callbacks {
		total += count
	}
	return total
}

// weightedCost sums invocations * per-instance cost across every field.
func (e *executor) weightedCost() int64 {
	total := int64(0)
	paths := make([]string, 0, len(e.callbacks))
	for path := range e.callbacks {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		def := e.fieldDefByPath(path)
		total += int64(e.callbacks[path]) * def.Cost
	}
	return total
}

func (e *executor) fieldDefByPath(path string) *FieldDef {
	for _, typ := range e.schema.Types {
		for _, def := range typ.Fields {
			if typ.Name+"."+def.Name == path {
				return def
			}
		}
	}
	return nil
}

// elementCount returns the number of leaf objects a list field yields for the
// current run, multiplying every list dimension's chosen size.
func (e *executor) elementCount(parentName string, def *FieldDef) int64 {
	if len(def.ListMaxLen) == 0 {
		return 1
	}
	path := parentName + "." + def.Name
	size := e.lengths[path]
	count := int64(1)
	for range def.ListMaxLen {
		count *= int64(size)
	}
	return count
}

// runObject executes one parent instance and returns its weighted cost.
func (e *executor) runObject(parentType *Type, selections []Selection) int64 {
	groups, err := e.collect(parentType, selections)
	if err != nil {
		panic(err)
	}
	total := int64(0)
	for _, key := range sortedGroupKeys(groups) {
		group := groups[key]
		first := group.members[0].field
		def := parentType.Fields[first.Name]

		// One resolver invocation per parent instance; merging keeps this at
		// one even when the response key repeats through fragments.
		e.callbacks[parentType.Name+"."+def.Name]++
		total += def.Cost

		if def.Type.IsLeaf() {
			continue
		}

		merged := make([]Selection, 0)
		for _, member := range group.members {
			merged = append(merged, member.field.SelectionSet...)
		}

		elements := e.elementCount(parentType.Name, def)
		for i := int64(0); i < elements; i++ {
			total += e.runReturn(def.Type, merged)
		}
	}
	return total
}

// runReturn executes one object position for either a concrete or abstract
// type. Abstract types take the concrete branch chosen for this run.
func (e *executor) runReturn(typ *Type, selections []Selection) int64 {
	switch typ.Kind {
	case KindObject:
		return e.runObject(typ, selections)
	case KindInterface, KindUnion:
		branch := e.lengths["abstract:"+typ.Name]
		concreteName := typ.PossibleTypes[branch%len(typ.PossibleTypes)]
		return e.runObject(e.schema.Types[concreteName], selections)
	default:
		return 0
	}
}

func (e *executor) collect(parentType *Type, selections []Selection) (map[string]*fieldGroup, error) {
	a := &admitter{
		schema:    e.schema,
		doc:       e.doc,
		variables: e.variables,
		scopes:    map[string]bool{},
	}
	return a.collectFields(parentType, selections)
}

// enumerateSteps describes one finite enumeration axis.
type enumerateStep struct {
	key string
	max int
}

func listSteps(schema *Schema) []enumerateStep {
	steps := []enumerateStep{}
	for typeName, typ := range schema.Types {
		for fieldName, def := range typ.Fields {
			if len(def.ListMaxLen) > 0 {
				largest := 0
				for _, dim := range def.ListMaxLen {
					if dim > largest {
						largest = dim
					}
				}
				steps = append(steps, enumerateStep{
					key: typeName + "." + fieldName,
					max: largest,
				})
			}
		}
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i].key < steps[j].key })
	return steps
}

func abstractSteps(schema *Schema) []enumerateStep {
	steps := []enumerateStep{}
	for _, typ := range schema.Types {
		if typ.Kind == KindInterface || typ.Kind == KindUnion {
			steps = append(steps, enumerateStep{
				key: "abstract:" + typ.Name,
				max: len(typ.PossibleTypes) - 1,
			})
		}
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i].key < steps[j].key })
	return steps
}

// enumerate runs fn over every combination of list lengths (0..max) and
// abstract branch choices. Fixture maxima are small, so the product stays
// bounded.
func enumerate(t *testing.T, schema *Schema, fn func(lengths map[string]int)) {
	t.Helper()
	steps := append(listSteps(schema), abstractSteps(schema)...)
	var recurse func(index int, current map[string]int)
	recurse = func(index int, current map[string]int) {
		if index == len(steps) {
			fn(current)
			return
		}
		step := steps[index]
		for value := 0; value <= step.max; value++ {
			next := map[string]int{}
			for k, v := range current {
				next[k] = v
			}
			next[step.key] = value
			recurse(index+1, next)
		}
	}
	recurse(0, map[string]int{})
}

func runEnumeration(t *testing.T, schema *Schema, doc *Document) (maxActual int64, runs int) {
	t.Helper()
	op := doc.Operations[0]
	root := schema.Types[schema.QueryType]
	enumerate(t, schema, func(lengths map[string]int) {
		exec := newExecutor(schema, doc, lengths)
		actual := exec.runObject(root, op.SelectionSet)
		if actual > maxActual {
			maxActual = actual
		}
		runs++
	})
	return maxActual, runs
}

func findContribution(report *Report, responsePath []string) *Contribution {
	for _, contribution := range report.Contributions {
		if equalStrings(contribution.ResponsePath, responsePath) {
			return contribution
		}
	}
	return nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
