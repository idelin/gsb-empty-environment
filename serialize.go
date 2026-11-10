package main

import "fmt"

// ObjJSON is the serialized form of one object: a reference number (id)
// plus, for each field, either null or the id of the referenced object.
// Shared references and cycles are expressed by reusing ids.
type ObjJSON struct {
	ID     int             `json:"id"`
	Type   string          `json:"type"`
	Fields map[string]*int `json:"fields"`
}

// SharedJSON describes one object reachable from both danger fields.
type SharedJSON struct {
	ID       int      `json:"id"`
	Type     string   `json:"type"`
	Triggers []string `json:"triggers,omitempty"`
}

// Result is the output of `solve` and the input of `verify`.
type Result struct {
	Dangerous  bool         `json:"dangerous"`
	MinObjects int          `json:"minObjects"`
	Shared     []SharedJSON `json:"shared,omitempty"`
	Objects    []ObjJSON    `json:"objects,omitempty"`
	Render     []string     `json:"render,omitempty"`
	Note       string       `json:"note,omitempty"`
}

func buildResult(m *model, sol *solution) *Result {
	if sol == nil {
		return &Result{
			Dangerous: false,
			Note: fmt.Sprintf(
				"no dangerous object graph with at most %d objects; the whitelist holds within this bound",
				m.maxObj),
		}
	}
	res := &Result{Dangerous: true, MinObjects: len(sol.nodes)}
	for _, v := range sol.shared {
		t := m.spec.Types[sol.nodes[v].typ]
		res.Shared = append(res.Shared, SharedJSON{ID: v, Type: t.Name, Triggers: t.Triggers})
	}
	for i, nd := range sol.nodes {
		t := m.spec.Types[nd.typ]
		fields := map[string]*int{}
		for fi, f := range t.Fields {
			if r := nd.refs[fi]; r >= 0 {
				v := r
				fields[f.Name] = &v
			} else {
				fields[f.Name] = nil
			}
		}
		res.Objects = append(res.Objects, ObjJSON{ID: i, Type: t.Name, Fields: fields})
	}
	res.Render = render(m, sol.nodes)
	return res
}

// render produces a human-readable reference-numbered dump such as
// "#0: Session{primary=#1, backup=#1}".
func render(m *model, nodes []node) []string {
	var out []string
	for i, nd := range nodes {
		t := m.spec.Types[nd.typ]
		s := fmt.Sprintf("#%d: %s{", i, t.Name)
		for fi, f := range t.Fields {
			if fi > 0 {
				s += ", "
			}
			s += f.Name + "="
			if nd.refs[fi] >= 0 {
				s += fmt.Sprintf("#%d", nd.refs[fi])
			} else {
				s += "null"
			}
		}
		s += "}"
		out = append(out, s)
	}
	return out
}
