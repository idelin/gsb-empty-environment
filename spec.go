package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// FieldSpec declares one field of a type: its name and static (declared) type.
type FieldSpec struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// TypeSpec declares a type: its name, the trigger methods it runs on
// deserialization (e.g. "readObject"), and its fields.
type TypeSpec struct {
	Name     string      `json:"name"`
	Triggers []string    `json:"triggers,omitempty"`
	Fields   []FieldSpec `json:"fields,omitempty"`
}

// Spec is the whitelist / type-graph input.
type Spec struct {
	// RootTypes are the types the whitelist accepts as deserialization root.
	RootTypes []string `json:"rootTypes"`
	// DangerFields names the two root fields; the danger condition is that
	// one identical runtime object is reachable from both.
	DangerFields []string `json:"dangerFields"`
	// MaxObjects bounds the search (1..8, default 8).
	MaxObjects int `json:"maxObjects,omitempty"`
	// TriggeredOnly tightens the danger condition: the shared object must
	// additionally have at least one trigger method.
	TriggeredOnly bool `json:"triggeredOnly,omitempty"`
	// Types is the type graph.
	Types []TypeSpec `json:"types"`
	// Subtypes maps a static type to the runtime types that may substitute
	// for it. A static type without an entry allows only itself.
	Subtypes map[string][]string `json:"subtypes,omitempty"`
}

// model is the validated, index-based form of a Spec.
type model struct {
	spec       *Spec
	typeIdx    map[string]int
	fieldIdx   []map[string]int // per type: field name -> position
	allowed    [][]int          // allowed[staticType] -> substitutable actual types
	allowedSet []map[int]bool
	hasTrig    []bool
	roots      []int
	maxObj     int
}

func loadSpec(path string) (*Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var spec Spec
	if err := dec.Decode(&spec); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &spec, nil
}

func buildModel(spec *Spec) (*model, error) {
	if len(spec.Types) == 0 {
		return nil, fmt.Errorf("spec declares no types")
	}
	m := &model{spec: spec, typeIdx: map[string]int{}}
	for i, t := range spec.Types {
		if t.Name == "" {
			return nil, fmt.Errorf("type #%d has an empty name", i)
		}
		if _, dup := m.typeIdx[t.Name]; dup {
			return nil, fmt.Errorf("duplicate type %q", t.Name)
		}
		m.typeIdx[t.Name] = i
	}
	m.fieldIdx = make([]map[string]int, len(spec.Types))
	for i, t := range spec.Types {
		fm := map[string]int{}
		for j, f := range t.Fields {
			if f.Name == "" {
				return nil, fmt.Errorf("type %q: field #%d has an empty name", t.Name, j)
			}
			if _, dup := fm[f.Name]; dup {
				return nil, fmt.Errorf("type %q: duplicate field %q", t.Name, f.Name)
			}
			if _, ok := m.typeIdx[f.Type]; !ok {
				return nil, fmt.Errorf("type %q: field %q has unknown static type %q", t.Name, f.Name, f.Type)
			}
			fm[f.Name] = j
		}
		m.fieldIdx[i] = fm
	}
	m.hasTrig = make([]bool, len(spec.Types))
	for i, t := range spec.Types {
		m.hasTrig[i] = len(t.Triggers) > 0
	}
	m.allowed = make([][]int, len(spec.Types))
	m.allowedSet = make([]map[int]bool, len(spec.Types))
	for i, t := range spec.Types {
		list, ok := spec.Subtypes[t.Name]
		if !ok {
			list = []string{t.Name}
		}
		if len(list) == 0 {
			return nil, fmt.Errorf("subtypes[%q] is empty", t.Name)
		}
		set := map[int]bool{}
		for _, name := range list {
			j, ok := m.typeIdx[name]
			if !ok {
				return nil, fmt.Errorf("subtypes[%q] mentions unknown type %q", t.Name, name)
			}
			if !set[j] {
				set[j] = true
				m.allowed[i] = append(m.allowed[i], j)
			}
		}
		m.allowedSet[i] = set
	}
	if len(spec.DangerFields) != 2 || spec.DangerFields[0] == spec.DangerFields[1] {
		return nil, fmt.Errorf("dangerFields must list exactly two distinct field names")
	}
	if len(spec.RootTypes) == 0 {
		return nil, fmt.Errorf("rootTypes is empty")
	}
	for _, name := range spec.RootTypes {
		i, ok := m.typeIdx[name]
		if !ok {
			return nil, fmt.Errorf("unknown root type %q", name)
		}
		for _, df := range spec.DangerFields {
			if _, ok := m.fieldIdx[i][df]; !ok {
				return nil, fmt.Errorf("root type %q does not declare danger field %q", name, df)
			}
		}
		m.roots = append(m.roots, i)
	}
	m.maxObj = spec.MaxObjects
	if m.maxObj == 0 {
		m.maxObj = 8
	}
	if m.maxObj < 1 || m.maxObj > 8 {
		return nil, fmt.Errorf("maxObjects must be between 1 and 8, got %d", m.maxObj)
	}
	return m, nil
}
