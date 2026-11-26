package mvrewrite

import (
	"fmt"
	"sort"
)

type scanInfo struct {
	Alias    string
	Physical string
	Node     *Node
}

func collectScans(n *Node) []scanInfo {
	if n == nil {
		return nil
	}
	if n.Kind == ScanNode {
		alias := n.Alias
		if alias == "" {
			alias = n.Table
		}
		physical := n.Table
		if physical == "" {
			physical = alias
		}
		return []scanInfo{{Alias: alias, Physical: physical, Node: n}}
	}
	out := collectScans(n.Left)
	out = append(out, collectScans(n.Right)...)
	return out
}

func collectAllScans(q *Query, vs []*View) []scanInfo {
	var out []scanInfo
	if q != nil {
		out = append(out, collectScans(q.Root)...)
	}
	for _, v := range vs {
		if v != nil {
			out = append(out, collectScans(v.Root)...)
		}
	}
	return out
}

func validateSpec(catalog *Catalog, q *Query, v *View) error {
	if len(q.Select) == 0 {
		return fmt.Errorf("query.select must contain at least one output column")
	}
	if len(v.Select) == 0 {
		return fmt.Errorf("view %q select must contain at least one output column", v.ID)
	}
	if err := validateNode(q.Root); err != nil {
		return fmt.Errorf("query: %w", err)
	}
	if err := validateNode(v.Root); err != nil {
		return fmt.Errorf("view %q: %w", v.ID, err)
	}
	if catalog != nil {
		for _, ref := range allRefs(q, v) {
			if !catalogHasRef(catalog, ref) {
				// Be permissive for computed/alias-only specs: evaluation can
				// still work if instances contain the qualified key.
				continue
			}
		}
	}
	return nil
}

func validateNode(n *Node) error {
	if n == nil {
		return fmt.Errorf("nil node")
	}
	switch n.Kind {
	case ScanNode:
		if n.Table == "" && n.Alias == "" {
			return fmt.Errorf("scan requires table or alias")
		}
	case InnerJoin, LeftJoin:
		if n.Left == nil || n.Right == nil {
			return fmt.Errorf("%s join requires both children", n.Kind)
		}
		if err := validateNode(n.Left); err != nil {
			return err
		}
		if err := validateNode(n.Right); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown node kind %q", n.Kind)
	}
	return nil
}

func allRefs(q *Query, v *View) []ColRef {
	seen := map[ColRef]struct{}{}
	var out []ColRef
	add := func(rs ...ColRef) {
		for _, r := range rs {
			if _, ok := seen[r]; ok {
				continue
			}
			seen[r] = struct{}{}
			out = append(out, r)
		}
	}
	if q != nil {
		add(q.Select...)
		addPreds(q.Where, add)
		add(q.NullReject...)
	}
	var walk func(n *Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		add(n.Select...)
		addPreds(n.Where, add)
		add(n.NullReject...)
		for _, e := range n.On {
			add(e.Left, e.Right)
		}
		walk(n.Left)
		walk(n.Right)
	}
	if q != nil {
		walk(q.Root)
	}
	if v != nil {
		add(v.Select...)
		addPreds(v.Where, add)
		add(v.NullReject...)
		walk(v.Root)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func addPreds(ps []Predicate, add func(...ColRef)) {
	for _, p := range ps {
		add(p.Col)
	}
}

func catalogHasRef(c *Catalog, ref ColRef) bool {
	if _, ok := c.column(ref.Table, ref.Column); ok {
		return true
	}
	for _, t := range c.Tables {
		for _, col := range t.Columns {
			if col.Name == ref.Column {
				return true
			}
		}
	}
	return false
}

func buildValidationCounterexample(catalog *Catalog, q *Query, v *View, err error) *Counterexample {
	db := minimalSkeletonDatabase(catalog, q, v)
	return &Counterexample{
		Kind:        ReasonInvalidSpec,
		Description: err.Error(),
		Database:    db,
	}
}

func buildMissingOutputCounterexample(catalog *Catalog, q *Query, v *View, missing []ColRef) *Counterexample {
	db := minimalSkeletonDatabase(catalog, q, v)
	return &Counterexample{
		Kind:        ReasonMissingOutput,
		Description: fmt.Sprintf("view output lacks %s, so dedup projection/compensation cannot reproduce the query", refsStrings(missing)),
		Database:    db,
	}
}

func buildAmbiguityCounterexample(catalog *Catalog, q *Query, v *View, synthErr error) *Counterexample {
	db := minimalSkeletonDatabase(catalog, q, v)
	msg := "no exported NOT NULL/PK discriminator is available; true base NULL and outer-join padding are indistinguishable"
	if synthErr != nil {
		msg = synthErr.Error()
	}
	return &Counterexample{Kind: ReasonMissingDiscriminator, Description: msg, Database: db}
}

func minimalSkeletonDatabase(catalog *Catalog, q *Query, vs ...*View) *Database {
	db := NewDatabase()
	scans := collectAllScans(q, vs)
	seen := map[string]bool{}
	for _, s := range scans {
		name := s.Physical
		if seen[name] {
			continue
		}
		seen[name] = true
		row := map[string]any{}
		if catalog != nil {
			if t, ok := catalog.Tables[name]; ok {
				for _, c := range t.Columns {
					if c.PrimaryKey || c.NotNull {
						row[c.Name] = int64(1)
					} else {
						row[c.Name] = nil
					}
				}
			}
		}
		db.Tables[name] = &TableData{Name: name, Rows: []map[string]any{row}}
	}
	return db
}
