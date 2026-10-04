package fieldauth

import "fmt"

// Derived describes a field whose value is recomputed automatically
// from other fields whenever an update is authorized. Derived fields
// cannot be patched directly; their recomputed values participate in
// rule checks like any other changed field.
type Derived struct {
	Name    string
	Depends []string
	Compute func(rec Record) interface{}
}

// topoOrder returns derived field indexes in dependency order
// (dependencies first). Edges only exist between derived fields;
// depending on base fields needs no ordering.
func topoOrder(derived []Derived) ([]int, error) {
	index := make(map[string]int, len(derived))
	for i, d := range derived {
		if _, dup := index[d.Name]; dup {
			return nil, fmt.Errorf("fieldauth: duplicate derived field %q", d.Name)
		}
		index[d.Name] = i
	}
	indegree := make([]int, len(derived))
	dependents := make([][]int, len(derived))
	for i, d := range derived {
		for _, dep := range d.Depends {
			j, ok := index[dep]
			if !ok {
				continue // base field dependency
			}
			indegree[i]++
			dependents[j] = append(dependents[j], i)
		}
	}
	var queue []int
	for i := range derived {
		if indegree[i] == 0 {
			queue = append(queue, i)
		}
	}
	var order []int
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		order = append(order, i)
		for _, j := range dependents[i] {
			indegree[j]--
			if indegree[j] == 0 {
				queue = append(queue, j)
			}
		}
	}
	if len(order) != len(derived) {
		return nil, fmt.Errorf("fieldauth: cycle among derived fields")
	}
	return order, nil
}
