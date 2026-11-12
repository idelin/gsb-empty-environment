package provenance

import (
	"fmt"
	"sort"
)

// Tuple is one output row of a node, annotated with its exact provenance.
type Tuple struct {
	Key  string
	Val  float64
	Prov *Expr
	Desc string
}

// SourceRecord is an input row of the data-security host. ID is the stable
// source identifier that deletion requests refer to.
type SourceRecord struct {
	ID  string
	Key string
	Val float64
}

// Node is a materialized operator of the provenance algebra.
type Node interface {
	Name() string
	Output() []Tuple
}

// Pipeline executes nodes eagerly and keeps them so deletion impact can be
// analyzed over every derived object.
type Pipeline struct {
	store *Store
	nodes []Node
}

// NewPipeline returns a pipeline interning expressions in store.
func NewPipeline(store *Store) *Pipeline {
	return &Pipeline{store: store}
}

// Store returns the expression store backing the pipeline.
func (p *Pipeline) Store() *Store { return p.store }

// Nodes lists the executed nodes in construction order.
func (p *Pipeline) Nodes() []Node { return p.nodes }

func (p *Pipeline) add(n Node) { p.nodes = append(p.nodes, n) }

// Copy materializes source records, tagging each with Var(ID).
type Copy struct {
	name string
	out  []Tuple
}

// NewCopy executes a copy node over recs.
func NewCopy(p *Pipeline, name string, recs []SourceRecord) *Copy {
	c := &Copy{name: name, out: make([]Tuple, len(recs))}
	for i, r := range recs {
		c.out[i] = Tuple{
			Key:  r.Key,
			Val:  r.Val,
			Prov: p.store.Var(r.ID),
			Desc: fmt.Sprintf("copy(%s)", r.ID),
		}
	}
	p.add(c)
	return c
}

// Name implements Node.
func (c *Copy) Name() string { return c.name }

// Output implements Node.
func (c *Copy) Output() []Tuple { return c.out }

// Filter keeps the tuples of in matching pred; provenance passes through
// unchanged, so filtered-out sources vanish from downstream dependencies.
type Filter struct {
	name string
	out  []Tuple
}

// NewFilter executes a filter node.
func NewFilter(p *Pipeline, name string, in Node, pred func(Tuple) bool) *Filter {
	f := &Filter{name: name}
	for _, t := range in.Output() {
		if pred(t) {
			t.Desc = "filter(" + t.Desc + ")"
			f.out = append(f.out, t)
		}
	}
	p.add(f)
	return f
}

// Name implements Node.
func (f *Filter) Name() string { return f.name }

// Output implements Node.
func (f *Filter) Output() []Tuple { return f.out }

// AggKind selects the aggregation function.
type AggKind int

const (
	// AggCount counts group members.
	AggCount AggKind = iota
	// AggSum sums member values.
	AggSum
)

// Aggregate groups tuples by key. The group provenance is the Sum of member
// provenances, preserving one contribution per member, duplicates included.
type Aggregate struct {
	name string
	out  []Tuple
}

// NewAggregate executes an aggregate node over in.
func NewAggregate(p *Pipeline, name string, in Node, kind AggKind) *Aggregate {
	type group struct {
		provs []*Expr
		val   float64
	}
	groups := make(map[string]*group)
	for _, t := range in.Output() {
		g, ok := groups[t.Key]
		if !ok {
			g = &group{}
			groups[t.Key] = g
		}
		g.provs = append(g.provs, t.Prov)
		g.val += t.Val
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	a := &Aggregate{name: name}
	for _, k := range keys {
		g := groups[k]
		val := g.val
		if kind == AggCount {
			val = float64(len(g.provs))
		}
		a.out = append(a.out, Tuple{
			Key:  k,
			Val:  val,
			Prov: p.store.Sum(g.provs...),
			Desc: fmt.Sprintf("agg(%s)", k),
		})
	}
	p.add(a)
	return a
}

// Name implements Node.
func (a *Aggregate) Name() string { return a.name }

// Output implements Node.
func (a *Aggregate) Output() []Tuple { return a.out }

// Join matches left and right tuples on Key. Every matching pair yields one
// output tuple whose provenance is the product of the pair's provenances,
// so a join match disappears exactly when either side loses a contributor.
type Join struct {
	name string
	out  []Tuple
}

// NewJoin executes an equi-join on Key. Output values are left plus right.
func NewJoin(p *Pipeline, name string, left, right Node) *Join {
	index := make(map[string][]Tuple)
	for _, r := range right.Output() {
		index[r.Key] = append(index[r.Key], r)
	}
	j := &Join{name: name}
	for _, l := range left.Output() {
		for _, r := range index[l.Key] {
			j.out = append(j.out, Tuple{
				Key:  l.Key,
				Val:  l.Val + r.Val,
				Prov: p.store.Prod(l.Prov, r.Prov),
				Desc: fmt.Sprintf("join(%s,%s)", l.Desc, r.Desc),
			})
		}
	}
	p.add(j)
	return j
}

// Name implements Node.
func (j *Join) Name() string { return j.name }

// Output implements Node.
func (j *Join) Output() []Tuple { return j.out }
