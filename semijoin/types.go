package semijoin

import (
	"errors"
	"fmt"
)

type Semantic int

const (
	Exists Semantic = iota
	NotExists
	NotNullIn
)

type State int

const (
	Initial State = iota
	Final
)

type VariableKind int

const (
	KeyCount VariableKind = iota
	NullCount
)

type Nullable struct {
	Value any
	Valid bool
}

type Variable struct {
	ID    string
	Shard int
	State State
	Kind  VariableKind
	Key   any
	Min   int64
	Max   int64
}

type OuterRow struct {
	Group        string
	Shard        int
	Key          Nullable
	InitialCount int64
	FinalCount   int64
	Delta        int64
}

type LinearTerm struct {
	Coefficient int64
	VariableID  string
}

type LinearConstraint struct {
	Terms    []LinearTerm
	Constant int64
	Op       string
}

type PageVariable struct {
	VariableID string
	State      State
}

type Page struct {
	ID      string
	Cost    int64
	Reveals []PageVariable
}

type Problem struct {
	Semantic    Semantic
	Outer       []OuterRow
	Variables   []Variable
	Constraints []LinearConstraint
	Pages       []Page
}

type PageValues map[PageVariable]int64

type Assignment map[string]int64

type Database struct {
	Variables Assignment
	Pages     map[string]PageValues
}

type Witness struct {
	First        Database
	Second       Database
	Pages        []string
	FirstOutput  map[string]int64
	SecondOutput map[string]int64
}

type PolicyBranch struct {
	Observed PageValues
	Child    *PolicyNode
}

type PolicyNode struct {
	ReadPage  string
	Branches  []PolicyBranch
	Leaf      bool
	Remaining map[string]int64
}

type Policy struct {
	Groups []string
	Root   *PolicyNode
	Worst  int64
}

type Result struct {
	Determined           bool
	MinimumWorstReadCost int64
	Policies             []Policy
	DeterministicOutputs map[string]int64
	Witness              *Witness
}

type variableInfo struct {
	variable  Variable
	index     int
	domain    []int64
	uncertain bool
}

type component struct {
	vars      map[int]bool
	cons      []int
	keys      map[canonicalKey]bool
	nulls     map[int]bool
	groups    map[string]bool
	worlds    []world
	outputs   []map[string]int64
	ambiguous bool
}

type world struct {
	values Assignment
	pages  map[string]PageValues
}

type superComponent struct {
	comps   map[int]bool
	groups  []string
	pages   []int
	worlds  []world
	outputs []map[string]int64
	policy  *Policy
}

type model struct {
	problem *Problem
	infos   []*variableInfo
	byID    map[string]*variableInfo
	pvIndex map[PageVariable]int
	edges   map[int]map[int]bool
	comps   []*component
	supers  []*superComponent
	outer   map[string][]OuterRow
}

type canonicalKey struct {
	kind byte
	text string
}

func canonical(value any) (canonicalKey, error) {
	if value == nil {
		return canonicalKey{kind: 'n'}, nil
	}
	switch v := value.(type) {
	case string:
		return canonicalKey{kind: 's', text: v}, nil
	case int:
		return canonicalKey{kind: 'i', text: fmt.Sprintf("%d", v)}, nil
	case int8:
		return canonicalKey{kind: 'i', text: fmt.Sprintf("%d", v)}, nil
	case int16:
		return canonicalKey{kind: 'i', text: fmt.Sprintf("%d", v)}, nil
	case int32:
		return canonicalKey{kind: 'i', text: fmt.Sprintf("%d", v)}, nil
	case int64:
		return canonicalKey{kind: 'i', text: fmt.Sprintf("%d", v)}, nil
	case uint:
		return canonicalKey{kind: 'u', text: fmt.Sprintf("%d", v)}, nil
	case uint8:
		return canonicalKey{kind: 'u', text: fmt.Sprintf("%d", v)}, nil
	case uint16:
		return canonicalKey{kind: 'u', text: fmt.Sprintf("%d", v)}, nil
	case uint32:
		return canonicalKey{kind: 'u', text: fmt.Sprintf("%d", v)}, nil
	case uint64:
		return canonicalKey{kind: 'u', text: fmt.Sprintf("%d", v)}, nil
	case bool:
		if v {
			return canonicalKey{kind: 'b', text: "1"}, nil
		}
		return canonicalKey{kind: 'b', text: "0"}, nil
	default:
		return canonicalKey{}, fmt.Errorf("unsupported key type %T", value)
	}
}

func (s Semantic) valid() bool {
	return s >= Exists && s <= NotNullIn
}

func (s State) valid() bool {
	return s == Initial || s == Final
}

func (k VariableKind) valid() bool {
	return k == KeyCount || k == NullCount
}

func (p *Problem) validate() error {
	if p == nil {
		return errors.New("nil problem")
	}
	if !p.Semantic.valid() {
		return errors.New("invalid semantic")
	}
	if len(p.Pages) > 10 {
		return errors.New("at most ten candidate pages are supported")
	}
	seenVars := make(map[string]bool)
	for i, v := range p.Variables {
		if v.ID == "" || seenVars[v.ID] {
			return fmt.Errorf("variable %d has empty or duplicate ID", i)
		}
		seenVars[v.ID] = true
		if !v.State.valid() || !v.Kind.valid() {
			return fmt.Errorf("variable %s has invalid state or kind", v.ID)
		}
		if v.Min < 0 || v.Max < v.Min {
			return fmt.Errorf("variable %s has invalid count interval", v.ID)
		}
		if v.Kind == KeyCount {
			if _, err := canonical(v.Key); err != nil {
				return fmt.Errorf("variable %s: %w", v.ID, err)
			}
		}
	}
	groupPostCount := make(map[string]int64)
	for i, row := range p.Outer {
		if row.Group == "" {
			return fmt.Errorf("outer row %d has empty group", i)
		}
		if row.InitialCount < 0 || row.FinalCount < 0 {
			return fmt.Errorf("outer group %s has negative initial or final multiplicity", row.Group)
		}
		if row.Delta > 0 && row.FinalCount > (1<<63-1)-row.Delta ||
			row.Delta < 0 && row.FinalCount < (-1<<63)-row.Delta {
			return fmt.Errorf("outer group %s post-batch multiplicity overflows int64", row.Group)
		}
		if row.FinalCount+row.Delta < 0 {
			return fmt.Errorf("outer group %s has negative post-batch multiplicity", row.Group)
		}
		groupPostCount[row.Group] += row.FinalCount + row.Delta
		if groupPostCount[row.Group] < 0 {
			return fmt.Errorf("outer group %s has negative aggregate post-batch multiplicity", row.Group)
		}
		if row.Key.Valid {
			if _, err := canonical(row.Key.Value); err != nil {
				return fmt.Errorf("outer group %s: %w", row.Group, err)
			}
		}
	}
	for i, c := range p.Constraints {
		if len(c.Terms) == 0 {
			return fmt.Errorf("constraint %d has no terms", i)
		}
		switch c.Op {
		case "<=", ">=", "=":
		default:
			return fmt.Errorf("constraint %d has invalid op %q", i, c.Op)
		}
		termSeen := make(map[string]bool)
		for j, term := range c.Terms {
			if term.Coefficient == 0 || term.VariableID == "" || termSeen[term.VariableID] {
				return fmt.Errorf("constraint %d term %d is invalid", i, j)
			}
			if !seenVars[term.VariableID] {
				return fmt.Errorf("constraint %d references unknown variable %s", i, term.VariableID)
			}
			termSeen[term.VariableID] = true
		}
	}
	seenPages := make(map[string]bool)
	seenReveals := make(map[PageVariable]bool)
	for i, page := range p.Pages {
		if page.ID == "" || seenPages[page.ID] {
			return fmt.Errorf("page %d has empty or duplicate ID", i)
		}
		seenPages[page.ID] = true
		if page.Cost < 0 {
			return fmt.Errorf("page %s has negative cost", page.ID)
		}
		if len(page.Reveals) == 0 {
			return fmt.Errorf("page %s reveals nothing", page.ID)
		}
		revealSeen := make(map[PageVariable]bool)
		for j, rv := range page.Reveals {
			if rv.VariableID == "" || !rv.State.valid() || revealSeen[rv] {
				return fmt.Errorf("page %s reveal %d is invalid or duplicate", page.ID, j)
			}
			revealSeen[rv] = true
			if !seenVars[rv.VariableID] {
				return fmt.Errorf("page %s references unknown variable %s", page.ID, rv.VariableID)
			}
			if seenReveals[rv] {
				return fmt.Errorf("page variable %s/%d is revealed on multiple pages", rv.VariableID, rv.State)
			}
			seenReveals[rv] = true
		}
	}
	return nil
}
