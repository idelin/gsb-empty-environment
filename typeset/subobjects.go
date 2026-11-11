package typeset

// rootKey returns the identity of a most-derived object's root subobject.
func rootKey(className string) subKey { return subKey("N:" + className) }

// SubobjectTable describes every base-class subobject inside one
// most-derived object.
//
// Repeated non-virtual bases produce several entries with distinct Keys; a
// virtual base produces exactly one entry reachable through every virtual
// path. The MostDerived entry itself is the object root.
type SubobjectTable struct {
	MostDerived string

	// Root is the key of the most-derived object.
	Root subKey

	// Subobjects maps canonical subobject key to its class name.
	Subobjects map[subKey]string

	// Children maps each subobject to the immediately nested base
	// subobjects (one per inheritance edge).
	Children map[subKey][]subKey

	// Parents maps each subobject to its containing subobjects. Virtual
	// bases have several parents; the root has none.
	Parents map[subKey][]subKey
}

// Subobject is one entry of a SubobjectTable.
type Subobject struct {
	Key   subKey
	Class string
}

// SubobjectTableFor builds (and memoizes) the subobject layout for objects
// of mostDerived.
func (a *Analyzer) SubobjectTableFor(mostDerived string) (*SubobjectTable, error) {
	if _, ok := a.classes[mostDerived]; !ok {
		return nil, hierarchyError("unknown most-derived class %q", mostDerived)
	}
	if t, ok := a.subObjects[mostDerived]; ok {
		return t, nil
	}

	root := rootKey(mostDerived)
	table := &SubobjectTable{
		MostDerived: mostDerived,
		Root:        root,
		Subobjects:  map[subKey]string{root: mostDerived},
		Children:    map[subKey][]subKey{},
		Parents:     map[subKey][]subKey{},
	}

	// DFS over inheritance edges; the traversal stack records the concrete
	// edges while table identity uses the canonical (virtual-aware) key.
	type frame struct {
		parent subKey
		path   []PathStep
	}
	stack := []frame{{parent: root, path: nil}}
	queued := map[subKey]bool{root: true}

	for len(stack) > 0 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		currentClass := mostDerived
		if len(top.path) > 0 {
			currentClass = top.path[len(top.path)-1].To
		}
		c := a.classes[currentClass]

		// Iterate in reverse so input order survives the LIFO stack.
		for i := len(c.DirectBases) - 1; i >= 0; i-- {
			base := c.DirectBases[i]
			step := PathStep{From: currentClass, To: base.Name, Virtual: base.Virtual}
			childPath := append(append([]PathStep(nil), top.path...), step)
			childKey := canonicalKey(childPath)

			if _, seen := table.Subobjects[childKey]; !seen {
				table.Subobjects[childKey] = base.Name
			}
			table.Children[top.parent] = appendUniqueKey(table.Children[top.parent], childKey)
			table.Parents[childKey] = appendUniqueKey(table.Parents[childKey], top.parent)

			// A shared virtual subobject is expanded only once.
			if !queued[childKey] {
				queued[childKey] = true
				stack = append(stack, frame{parent: childKey, path: childPath})
			}
		}
	}

	a.subObjects[mostDerived] = table
	return table, nil
}

func appendUniqueKey(keys []subKey, key subKey) []subKey {
	for _, k := range keys {
		if k == key {
			return keys
		}
	}
	return append(keys, key)
}

// AllSubobjects returns the table entries in a deterministic order: the
// most-derived object first, then subobjects sorted by canonical key.
func (t *SubobjectTable) AllSubobjects() []Subobject {
	out := make([]Subobject, 0, len(t.Subobjects))
	out = append(out, Subobject{Key: t.Root, Class: t.Subobjects[t.Root]})
	var keys []subKey
	for k := range t.Subobjects {
		if k != t.Root {
			keys = append(keys, k)
		}
	}
	sortSubKeys(keys)
	for _, k := range keys {
		out = append(out, Subobject{Key: k, Class: t.Subobjects[k]})
	}
	return out
}

func sortSubKeys(keys []subKey) {
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
}
