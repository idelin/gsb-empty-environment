package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// ---------- Raw spec (JSON) ----------

type rawSpec struct {
	QueueField string `json:"queue_field"`
	Fields     []struct {
		Name   string            `json:"name"`
		Values []json.RawMessage `json:"values"`
	} `json:"fields"`
	Rules []struct {
		Name string                     `json:"name"`
		When map[string]json.RawMessage `json:"when"`
		Set  map[string]json.RawMessage `json:"set"`
	} `json:"rules"`
}

func parseSpec(data []byte) (*rawSpec, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var s rawSpec
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("invalid spec JSON: %v", err)
	}
	return &s, nil
}

// canon normalizes a JSON value to a canonical string so that, e.g.,
// 1 and "1" stay distinct while 1 and 1.0 do not collide accidentally.
func canon(raw json.RawMessage) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func displayValue(canonical string) string {
	if strings.HasPrefix(canonical, "\"") {
		var s string
		if err := json.Unmarshal([]byte(canonical), &s); err == nil {
			return s
		}
	}
	return canonical
}

// ---------- Compiled model ----------

type condition struct {
	field int
	vals  map[int]bool // allowed value indices
}

type action struct {
	field  int
	valIdx int
}

type rule struct {
	name  string
	conds []condition
	acts  []action
}

func (r *rule) matches(state []int) bool {
	for _, c := range r.conds {
		if !c.vals[state[c.field]] {
			return false
		}
	}
	return true
}

type model struct {
	fieldNames  []string
	sizes       []int
	valCanon    [][]string // canonical JSON per field per value index
	valIndex    []map[string]int
	queueField  int
	queueStride int
	rules       []rule
	nStates     int
}

func compileModel(s *rawSpec, maxStates int) (*model, error) {
	if len(s.Fields) == 0 {
		return nil, fmt.Errorf("spec must declare at least one field")
	}
	qf := s.QueueField
	if qf == "" {
		qf = "queue"
	}
	m := &model{queueField: -1}
	knownField := map[string]bool{}
	for _, f := range s.Fields {
		if f.Name == "" {
			return nil, fmt.Errorf("field with empty name")
		}
		if knownField[f.Name] {
			return nil, fmt.Errorf("duplicate field %q", f.Name)
		}
		knownField[f.Name] = true
		if len(f.Values) == 0 {
			return nil, fmt.Errorf("field %q must declare at least one value", f.Name)
		}
		idx := map[string]int{}
		var canons []string
		for _, rv := range f.Values {
			c, err := canon(rv)
			if err != nil {
				return nil, fmt.Errorf("field %q: bad value: %v", f.Name, err)
			}
			if _, dup := idx[c]; dup {
				return nil, fmt.Errorf("field %q: duplicate value %s", f.Name, displayValue(c))
			}
			idx[c] = len(canons)
			canons = append(canons, c)
		}
		m.fieldNames = append(m.fieldNames, f.Name)
		m.sizes = append(m.sizes, len(canons))
		m.valCanon = append(m.valCanon, canons)
		m.valIndex = append(m.valIndex, idx)
		if f.Name == qf {
			m.queueField = len(m.fieldNames) - 1
		}
	}
	if m.queueField < 0 {
		return nil, fmt.Errorf("queue field %q is not declared in fields", qf)
	}
	if m.sizes[m.queueField] > 64 {
		return nil, fmt.Errorf("queue field has %d values; at most 64 supported", m.sizes[m.queueField])
	}
	n := 1
	for _, sz := range m.sizes {
		n *= sz
		if n > maxStates {
			return nil, fmt.Errorf("state space exceeds the -max-states cap of %d; shrink the finite domains or raise the cap", maxStates)
		}
	}
	m.nStates = n
	m.queueStride = 1
	for i := m.queueField + 1; i < len(m.sizes); i++ {
		m.queueStride *= m.sizes[i]
	}
	seenRule := map[string]bool{}
	for i, rs := range s.Rules {
		name := rs.Name
		if name == "" {
			name = fmt.Sprintf("rule-%d", i+1)
		}
		if seenRule[name] {
			return nil, fmt.Errorf("duplicate rule name %q", name)
		}
		seenRule[name] = true
		r := rule{name: name}
		for fname := range rs.When {
			if !knownField[fname] {
				return nil, fmt.Errorf("rule %q: unknown field %q in when", name, fname)
			}
		}
		for fname := range rs.Set {
			if !knownField[fname] {
				return nil, fmt.Errorf("rule %q: unknown field %q in set", name, fname)
			}
		}
		// Iterate in field declaration order for deterministic behavior.
		for fi, fname := range m.fieldNames {
			if raw, ok := rs.When[fname]; ok {
				allowed, err := m.parseValueSet(fi, raw)
				if err != nil {
					return nil, fmt.Errorf("rule %q: %v", name, err)
				}
				r.conds = append(r.conds, condition{field: fi, vals: allowed})
			}
			if raw, ok := rs.Set[fname]; ok {
				c, err := canon(raw)
				if err != nil {
					return nil, fmt.Errorf("rule %q: bad set value for field %q: %v", name, fname, err)
				}
				vi, ok := m.valIndex[fi][c]
				if !ok {
					return nil, fmt.Errorf("rule %q: value %s not in domain of field %q", name, displayValue(c), fname)
				}
				r.acts = append(r.acts, action{field: fi, valIdx: vi})
			}
		}
		m.rules = append(m.rules, r)
	}
	return m, nil
}

// parseValueSet accepts either a scalar or a list of allowed values.
func (m *model) parseValueSet(fi int, raw json.RawMessage) (map[int]bool, error) {
	trim := bytes.TrimSpace(raw)
	out := map[int]bool{}
	add := func(rv json.RawMessage) error {
		c, err := canon(rv)
		if err != nil {
			return err
		}
		vi, ok := m.valIndex[fi][c]
		if !ok {
			return fmt.Errorf("value %s not in domain of field %q", displayValue(c), m.fieldNames[fi])
		}
		out[vi] = true
		return nil
	}
	if len(trim) > 0 && trim[0] == '[' {
		var items []json.RawMessage
		if err := json.Unmarshal(trim, &items); err != nil {
			return nil, fmt.Errorf("bad value list for field %q: %v", m.fieldNames[fi], err)
		}
		if len(items) == 0 {
			return nil, fmt.Errorf("empty value list for field %q", m.fieldNames[fi])
		}
		for _, it := range items {
			if err := add(it); err != nil {
				return nil, err
			}
		}
		return out, nil
	}
	if err := add(trim); err != nil {
		return nil, err
	}
	return out, nil
}

// ---------- State encoding ----------

func (m *model) decodeInto(key int, buf []int) {
	for i := len(m.sizes) - 1; i >= 0; i-- {
		buf[i] = key % m.sizes[i]
		key /= m.sizes[i]
	}
}

func (m *model) encode(state []int) int {
	key := 0
	for i, sz := range m.sizes {
		key = key*sz + state[i]
	}
	return key
}

func (m *model) queueIdxOf(key int) int {
	return (key / m.queueStride) % m.sizes[m.queueField]
}

func (m *model) stateMap(key int) map[string]string {
	buf := make([]int, len(m.sizes))
	m.decodeInto(key, buf)
	out := make(map[string]string, len(buf))
	for i, vi := range buf {
		out[m.fieldNames[i]] = displayValue(m.valCanon[i][vi])
	}
	return out
}

func (m *model) stateString(key int) string {
	buf := make([]int, len(m.sizes))
	m.decodeInto(key, buf)
	parts := make([]string, len(buf))
	for i, vi := range buf {
		parts[i] = m.fieldNames[i] + "=" + displayValue(m.valCanon[i][vi])
	}
	return strings.Join(parts, ", ")
}

func (m *model) queueDisplay(qIdx int) string {
	return displayValue(m.valCanon[m.queueField][qIdx])
}

// ---------- Transition graph ----------

type graph struct {
	succ  [][]int // successor state per state
	srule [][]int // rule index used for each successor edge
	pred  [][]int // reverse adjacency
	term  []bool  // no rule matches
}

func buildGraph(m *model) *graph {
	n := m.nStates
	g := &graph{
		succ:  make([][]int, n),
		srule: make([][]int, n),
		pred:  make([][]int, n),
		term:  make([]bool, n),
	}
	buf := make([]int, len(m.sizes))
	tmp := make([]int, len(m.sizes))
	for key := 0; key < n; key++ {
		m.decodeInto(key, buf)
		var seen map[int]bool
		for ri := range m.rules {
			r := &m.rules[ri]
			if !r.matches(buf) {
				continue
			}
			copy(tmp, buf)
			for _, a := range r.acts {
				tmp[a.field] = a.valIdx
			}
			sk := m.encode(tmp)
			if seen == nil {
				seen = make(map[int]bool, 4)
			}
			if seen[sk] {
				continue // duplicate successor via another rule; keep first
			}
			seen[sk] = true
			g.succ[key] = append(g.succ[key], sk)
			g.srule[key] = append(g.srule[key], ri)
		}
		g.term[key] = len(g.succ[key]) == 0
	}
	for v := 0; v < n; v++ {
		for _, y := range g.succ[v] {
			g.pred[y] = append(g.pred[y], v)
		}
	}
	return g
}
