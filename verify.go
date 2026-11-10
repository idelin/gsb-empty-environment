package main

import (
	"fmt"
	"sort"
)

// check is one named verification result; err == nil means pass.
type check struct {
	name string
	err  error
}

// verifyGraph independently validates a serialized graph against the spec:
// structure, whitelist legality, the danger condition, and minimality
// (by re-running the search). It works purely on the parsed JSON form.
func verifyGraph(m *model, res *Result) []check {
	var checks []check
	add := func(name string, err error) {
		checks = append(checks, check{name, err})
	}

	objs := res.Objects
	n := len(objs)
	if n == 0 {
		add("non-empty graph", fmt.Errorf("graph has no objects"))
		return checks
	}
	add("non-empty graph", nil)

	if n > m.maxObj {
		add("object bound", fmt.Errorf("%d objects exceed maxObjects=%d", n, m.maxObj))
	} else {
		add("object bound", nil)
	}

	var idErr error
	for i, o := range objs {
		if o.ID != i {
			idErr = fmt.Errorf("objects[%d] has id %d, want %d (ids must be 0..n-1 in order)", i, o.ID, i)
			break
		}
	}
	add("ids sequential", idErr)

	var typeErr error
	for _, o := range objs {
		if _, ok := m.typeIdx[o.Type]; !ok {
			typeErr = fmt.Errorf("object #%d has unknown type %q", o.ID, o.Type)
			break
		}
	}
	add("types known", typeErr)
	if typeErr != nil {
		return checks
	}

	var fieldErr error
	for _, o := range objs {
		ti := m.typeIdx[o.Type]
		decl := m.spec.Types[ti].Fields
		if len(o.Fields) != len(decl) {
			fieldErr = fmt.Errorf("object #%d (%s) has %d fields, type declares %d",
				o.ID, o.Type, len(o.Fields), len(decl))
			break
		}
		for _, f := range decl {
			v, ok := o.Fields[f.Name]
			if !ok {
				fieldErr = fmt.Errorf("object #%d (%s) is missing declared field %q", o.ID, o.Type, f.Name)
				break
			}
			if v == nil {
				continue
			}
			if *v < 0 || *v >= n {
				fieldErr = fmt.Errorf("object #%d field %q references invalid id %d", o.ID, f.Name, *v)
				break
			}
			staticT := m.typeIdx[f.Type]
			actualT := m.typeIdx[objs[*v].Type]
			if !m.allowedSet[staticT][actualT] {
				fieldErr = fmt.Errorf("object #%d field %q: type %q is not a permitted substitute for static type %q",
					o.ID, f.Name, objs[*v].Type, f.Type)
				break
			}
		}
		if fieldErr != nil {
			break
		}
		for name := range o.Fields {
			if _, ok := m.fieldIdx[ti][name]; !ok {
				fieldErr = fmt.Errorf("object #%d (%s) has undeclared field %q", o.ID, o.Type, name)
				break
			}
		}
		if fieldErr != nil {
			break
		}
	}
	add("fields match declared shape", fieldErr)
	add("whitelist legality", fieldErr)
	if fieldErr != nil {
		return checks
	}

	rootOK := false
	for _, r := range m.roots {
		if m.spec.Types[r].Name == objs[0].Type {
			rootOK = true
			break
		}
	}
	var rootErr error
	if !rootOK {
		rootErr = fmt.Errorf("root type %q is not in rootTypes", objs[0].Type)
	}
	add("root type allowed", rootErr)

	adj := make([][]int, n)
	for i, o := range objs {
		ti := m.typeIdx[o.Type]
		for _, f := range m.spec.Types[ti].Fields {
			if v := o.Fields[f.Name]; v != nil {
				adj[i] = append(adj[i], *v)
			}
		}
	}
	walk := func(starts []int) map[int]bool {
		seen := map[int]bool{}
		stack := append([]int(nil), starts...)
		for len(stack) > 0 {
			v := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[v] {
				continue
			}
			seen[v] = true
			stack = append(stack, adj[v]...)
		}
		return seen
	}

	all := walk([]int{0})
	var reachErr error
	for i := range objs {
		if !all[i] {
			reachErr = fmt.Errorf("object #%d is unreachable from the root", i)
			break
		}
	}
	add("all objects reachable", reachErr)

	dangerA, dangerB := m.spec.DangerFields[0], m.spec.DangerFields[1]
	var startsA, startsB []int
	if v := objs[0].Fields[dangerA]; v != nil {
		startsA = []int{*v}
	}
	if v := objs[0].Fields[dangerB]; v != nil {
		startsB = []int{*v}
	}
	setA, setB := walk(startsA), walk(startsB)
	var shared []int
	for v := range setA {
		if !setB[v] {
			continue
		}
		if m.spec.TriggeredOnly && !m.hasTrig[m.typeIdx[objs[v].Type]] {
			continue
		}
		shared = append(shared, v)
	}
	sort.Ints(shared)
	var dangerErr error
	if len(shared) == 0 {
		dangerErr = fmt.Errorf("no object is reachable from both %q and %q", dangerA, dangerB)
	}
	add("danger condition", dangerErr)

	sol, err := solve(m)
	switch {
	case err != nil:
		add("minimality", fmt.Errorf("re-search failed: %v", err))
	case sol == nil:
		add("minimality", fmt.Errorf("search found no dangerous graph, but the submitted graph claims one"))
	case len(sol.nodes) != n:
		add("minimality", fmt.Errorf("not minimal: a dangerous graph with %d objects exists, submitted %d",
			len(sol.nodes), n))
	default:
		add("minimality", nil)
	}

	return checks
}
