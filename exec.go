package cteopt

// Stats tracks how often each operator is opened and how many rows it
// produces, making repeated computation observable.
type Stats struct {
	Opens        map[*Node]int
	RowsProduced map[*Node]int
}

// Executor runs a plan DAG against in-memory tables. A Materialize node
// computes its child once, stores the rows in a temporary memory buffer,
// and replays the buffer for every subsequent reader.
type Executor struct {
	Tables map[string][]Row
	Stats  *Stats
	buf    map[*Node][]Row
}

func NewExecutor(tables map[string][]Row) *Executor {
	return &Executor{
		Tables: tables,
		Stats:  &Stats{Opens: map[*Node]int{}, RowsProduced: map[*Node]int{}},
		buf:    map[*Node][]Row{},
	}
}

type iterator func() (Row, bool)

// Collect drains the plan and returns all result rows.
func (e *Executor) Collect(root *Node) []Row {
	it := e.open(root)
	var out []Row
	for r, ok := it(); ok; r, ok = it() {
		out = append(out, r)
	}
	return out
}

func (e *Executor) open(n *Node) iterator {
	e.Stats.Opens[n]++
	var it iterator
	switch n.Op {
	case OpScan:
		rows := e.Tables[n.Label]
		i := 0
		it = func() (Row, bool) {
			if i < len(rows) {
				r := rows[i]
				i++
				return r, true
			}
			return nil, false
		}
	case OpFilter:
		child := e.open(n.Children[0])
		it = func() (Row, bool) {
			for r, ok := child(); ok; r, ok = child() {
				if n.Pred(r) {
					return r, true
				}
			}
			return nil, false
		}
	case OpProject:
		child := e.open(n.Children[0])
		it = func() (Row, bool) {
			if r, ok := child(); ok {
				return n.Proj(r), true
			}
			return nil, false
		}
	case OpJoin:
		right := e.open(n.Children[1])
		table := map[any][]Row{}
		for r, ok := right(); ok; r, ok = right() {
			k := n.KeyRight(r)
			table[k] = append(table[k], r)
		}
		left := e.open(n.Children[0])
		var cur Row
		var matches []Row
		mi := 0
		it = func() (Row, bool) {
			for {
				if mi < len(matches) {
					joined := append(append(Row{}, cur...), matches[mi]...)
					mi++
					return joined, true
				}
				l, ok := left()
				if !ok {
					return nil, false
				}
				cur = l
				matches = table[n.KeyLeft(l)]
				mi = 0
			}
		}
	case OpAggregate:
		child := e.open(n.Children[0])
		var keys []any
		groups := map[any]float64{}
		for r, ok := child(); ok; r, ok = child() {
			k := n.GroupBy(r)
			if _, seen := groups[k]; !seen {
				keys = append(keys, k)
			}
			switch n.AggOp {
			case "count":
				groups[k]++
			case "sum":
				groups[k] += r[n.AggField].(float64)
			}
		}
		i := 0
		it = func() (Row, bool) {
			if i < len(keys) {
				r := Row{keys[i], groups[keys[i]]}
				i++
				return r, true
			}
			return nil, false
		}
	case OpUnionAll:
		left := e.open(n.Children[0])
		right := e.open(n.Children[1])
		it = func() (Row, bool) {
			if r, ok := left(); ok {
				return r, true
			}
			return right()
		}
	case OpMaterialize:
		if buf, done := e.buf[n]; done {
			i := 0
			it = func() (Row, bool) {
				if i < len(buf) {
					r := buf[i]
					i++
					return r, true
				}
				return nil, false
			}
		} else {
			child := e.open(n.Children[0])
			var acc []Row
			it = func() (Row, bool) {
				if r, ok := child(); ok {
					acc = append(acc, r)
					return r, true
				}
				e.buf[n] = acc
				return nil, false
			}
		}
	}
	return func() (Row, bool) {
		r, ok := it()
		if ok {
			e.Stats.RowsProduced[n]++
		}
		return r, ok
	}
}
