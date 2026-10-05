package permdiff

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Cond 是一条授权条件的语法树。
type Cond interface {
	// Eval 在三值逻辑下求值，并同时构建解释树。
	Eval(a Assignment) (Tri, *Explain)
	// Attrs 返回条件引用到的属性名（有序去重）。
	Attrs() []string
	// Predicates 返回条件中的全部原子谓词。
	Predicates() []Predicate
	String() string
}

// Leaf 是包裹单个谓词的条件。
type Leaf struct {
	P Predicate
}

func (c Leaf) Eval(a Assignment) (Tri, *Explain) {
	v := c.P.Eval(a)
	return v, &Explain{Node: c.P.String(), Value: v}
}

func (c Leaf) Attrs() []string         { return []string{c.P.Attr()} }
func (c Leaf) Predicates() []Predicate { return []Predicate{c.P} }
func (c Leaf) String() string          { return c.P.String() }

// And 是合取条件。
type And struct {
	Items []Cond
}

func (c And) Eval(a Assignment) (Tri, *Explain) {
	ex := &Explain{Node: "AND", Value: True}
	for _, item := range c.Items {
		v, sub := item.Eval(a)
		ex.Value = AndTri(ex.Value, v)
		ex.Children = append(ex.Children, sub)
	}
	return ex.Value, ex
}

func (c And) String() string          { return joinCond("AND", c.Items) }
func (c And) Attrs() []string         { return attrsOf(c.Items) }
func (c And) Predicates() []Predicate { return predsOf(c.Items) }

// Or 是析取条件。
type Or struct {
	Items []Cond
}

func (c Or) Eval(a Assignment) (Tri, *Explain) {
	ex := &Explain{Node: "OR", Value: False}
	for _, item := range c.Items {
		v, sub := item.Eval(a)
		ex.Value = OrTri(ex.Value, v)
		ex.Children = append(ex.Children, sub)
	}
	return ex.Value, ex
}

func (c Or) String() string          { return joinCond("OR", c.Items) }
func (c Or) Attrs() []string         { return attrsOf(c.Items) }
func (c Or) Predicates() []Predicate { return predsOf(c.Items) }

// Not 是否定条件。
type Not struct {
	Item Cond
}

func (c Not) Eval(a Assignment) (Tri, *Explain) {
	v, sub := c.Item.Eval(a)
	nv := NotTri(v)
	return nv, &Explain{Node: "NOT", Value: nv, Children: []*Explain{sub}}
}

func (c Not) String() string          { return "NOT (" + c.Item.String() + ")" }
func (c Not) Attrs() []string         { return c.Item.Attrs() }
func (c Not) Predicates() []Predicate { return c.Item.Predicates() }

func joinCond(op string, items []Cond) string {
	parts := make([]string, len(items))
	for i, c := range items {
		parts[i] = "(" + c.String() + ")"
	}
	return strings.Join(parts, " "+op+" ")
}

func attrsOf(items []Cond) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range items {
		for _, a := range c.Attrs() {
			if !seen[a] {
				seen[a] = true
				out = append(out, a)
			}
		}
	}
	sort.Strings(out)
	return out
}

func predsOf(items []Cond) []Predicate {
	var out []Predicate
	for _, c := range items {
		out = append(out, c.Predicates()...)
	}
	return out
}

// Decide 是授权判定：仅当条件求值为 True 时允许。
// False 与 Unknown（例如由缺失属性引起）都导致拒绝。
func Decide(c Cond, a Assignment) (bool, *Explain) {
	v, ex := c.Eval(a)
	return v == True, ex
}

// ParseCond 从 JSON 解析条件。支持的形式：
//   {"and": [cond, ...]}
//   {"or": [cond, ...]}
//   {"not": cond}
//   {"intRange": {"attr": "level", "min": 3, "max": 7}}
//   {"bool": {"attr": "mfa", "value": true}}
//   {"subsetOf": {"attr": "groups", "set": ["eng", "ops"]}}
//   {"containsAll": {"attr": "groups", "set": ["eng"]}}
//   {"contains": {"attr": "groups", "elem": "eng"}}
//   {"intersects": {"attr": "groups", "set": ["eng"]}}
func ParseCond(data []byte) (Cond, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("条件必须是 JSON 对象: %v", err)
	}
	if len(raw) != 1 {
		return nil, fmt.Errorf("条件对象必须恰好包含一个操作符，得到 %d 个", len(raw))
	}
	for op, body := range raw {
		switch op {
		case "and", "or":
			var items []json.RawMessage
			if err := json.Unmarshal(body, &items); err != nil {
				return nil, fmt.Errorf("%s 的操作数必须是数组: %v", op, err)
			}
			conds := make([]Cond, 0, len(items))
			for _, it := range items {
				c, err := ParseCond(it)
				if err != nil {
					return nil, err
				}
				conds = append(conds, c)
			}
			if op == "and" {
				return And{Items: conds}, nil
			}
			return Or{Items: conds}, nil
		case "not":
			c, err := ParseCond(body)
			if err != nil {
				return nil, err
			}
			return Not{Item: c}, nil
		case "intRange":
			var p struct {
				Attr string `json:"attr"`
				Min  int    `json:"min"`
				Max  int    `json:"max"`
			}
			if err := json.Unmarshal(body, &p); err != nil {
				return nil, err
			}
			if p.Attr == "" || p.Min > p.Max {
				return nil, fmt.Errorf("intRange 需要 attr 且 min <= max")
			}
			return Leaf{P: IntRange{A: p.Attr, Min: p.Min, Max: p.Max}}, nil
		case "bool":
			var p struct {
				Attr  string `json:"attr"`
				Value bool   `json:"value"`
			}
			if err := json.Unmarshal(body, &p); err != nil {
				return nil, err
			}
			if p.Attr == "" {
				return nil, fmt.Errorf("bool 需要 attr")
			}
			return Leaf{P: BoolIs{A: p.Attr, Val: p.Value}}, nil
		case "subsetOf", "containsAll", "intersects":
			var p struct {
				Attr string   `json:"attr"`
				Set  []string `json:"set"`
			}
			if err := json.Unmarshal(body, &p); err != nil {
				return nil, err
			}
			if p.Attr == "" {
				return nil, fmt.Errorf("%s 需要 attr", op)
			}
			s := make(map[string]bool, len(p.Set))
			for _, e := range p.Set {
				s[e] = true
			}
			switch op {
			case "subsetOf":
				return Leaf{P: SetSubsetOf{A: p.Attr, Set: s}}, nil
			case "containsAll":
				return Leaf{P: SetContainsAll{A: p.Attr, Set: s}}, nil
			default:
				return Leaf{P: SetIntersects{A: p.Attr, Set: s}}, nil
			}
		case "contains":
			var p struct {
				Attr string `json:"attr"`
				Elem string `json:"elem"`
			}
			if err := json.Unmarshal(body, &p); err != nil {
				return nil, err
			}
			if p.Attr == "" {
				return nil, fmt.Errorf("contains 需要 attr")
			}
			return Leaf{P: SetContains{A: p.Attr, Elem: p.Elem}}, nil
		default:
			return nil, fmt.Errorf("未知操作符 %q", op)
		}
	}
	return nil, fmt.Errorf("空条件")
}
