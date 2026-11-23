package envguard

import (
	"fmt"
	"strconv"
	"strings"
)

// Literal creates (or reuses) a trusted constant rope.
func (g *Graph) Literal(text string) (NodeID, error) {
	if id, ok := g.litKey[text]; ok {
		return id, nil
	}
	if int64(len([]rune(text))) > MaxRuneLen {
		return 0, fmt.Errorf("envguard: literal exceeds max length")
	}
	o := g.reg.literal(text)
	empty := newBset(len(g.reg.origins))
	n := &node{
		kind:     nLiteral,
		len:      int64(len([]rune(text))),
		text:     text,
		origin:   o.ID,
		allVars:  empty,
		content:  empty.set(o.ID),
		dataCtl:  empty,
		branchCt: empty,
		control:  empty,
	}
	id := g.intern(n)
	g.litKey[text] = id
	return id, nil
}

// Var references a binding qualified by tenant and name, so same-named
// variables from different tenants never alias.
func (g *Graph) Var(tenant, name string) (NodeID, error) {
	o, err := g.reg.lookup(tenant, name)
	if err != nil {
		return 0, err
	}
	if id, ok := g.varKey[o.ID]; ok {
		return id, nil
	}
	value := g.reg.valueOf(o.ID)
	originSet := newBset(len(g.reg.origins)).set(o.ID)
	empty := newBset(len(g.reg.origins))
	n := &node{
		kind:    nVar,
		len:     int64(len([]rune(value))),
		origin:  o.ID,
		allVars: originSet,
		content: originSet,
		// A raw variable both supplies characters and decides its own shape.
		dataCtl:  originSet,
		branchCt: empty,
		control:  originSet,
	}
	id := g.intern(n)
	g.varKey[o.ID] = id
	return id, nil
}

func concatKey(parts []NodeID) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(strconv.Itoa(int(p)))
		b.WriteByte(',')
	}
	return b.String()
}

// Concat builds a shared concatenation. Associative flattening of nested
// concatenations keeps hash-cons keys canonical.
func (g *Graph) Concat(parts ...NodeID) (NodeID, error) {
	flat, err := g.flatten(parts)
	if err != nil {
		return 0, err
	}
	if len(flat) == 0 {
		return g.Literal("")
	}
	if len(flat) == 1 {
		return flat[0], nil
	}
	key := concatKey(flat)
	if id, ok := g.conKey[key]; ok {
		return id, nil
	}
	n := &node{kind: nConcat, parts: flat}
	var total int64
	for _, p := range flat {
		total = addLen(total, g.nodes[p].len)
		n = g.accumulate(n, g.nodes[p])
	}
	n.len = total
	id := g.intern(n)
	g.conKey[key] = id
	return id, nil
}

func (g *Graph) flatten(parts []NodeID) ([]NodeID, error) {
	var out []NodeID
	for _, p := range parts {
		if int(p) <= 0 || int(p) >= len(g.nodes) {
			return nil, fmt.Errorf("envguard: unknown node id %d", p)
		}
		if g.nodes[p].kind == nConcat {
			out = append(out, g.nodes[p].parts...)
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// Loop repeats body exactly count times. count is a host-supplied bound, not
// derived from tenant data; zero (or an empty body) yields the empty rope.
func (g *Graph) Loop(body NodeID, count int64) (NodeID, error) {
	if count < 0 {
		return 0, fmt.Errorf("envguard: negative loop count")
	}
	if count == 0 || g.nodes[body].len == 0 {
		return g.Literal("")
	}
	if mulOverflows(g.nodes[body].len, count) {
		return 0, fmt.Errorf("envguard: loop expansion exceeds max length 2^44")
	}
	if id, ok := g.loopKey[loopKey{body, count}]; ok {
		return id, nil
	}
	n := &node{
		kind:  nLoop,
		body:  body,
		count: count,
		len:   g.nodes[body].len * count,
	}
	g.copySets(n, g.nodes[body])
	id := g.intern(n)
	g.loopKey[loopKey{body, count}] = id
	return id, nil
}

// Cond selects branch a when input (tenant/name) is accepted by its declared
// language, otherwise b. When both branches build to the identical interned
// node, the selector cannot influence any output and its control dependency
// is erased.
func (g *Graph) Cond(tenant, name string, a, b NodeID) (NodeID, error) {
	o, err := g.reg.lookup(tenant, name)
	if err != nil {
		return 0, err
	}
	if a == b {
		return a, nil
	}
	if id, ok := g.condKey[condKey{o.ID, a, b}]; ok {
		return id, nil
	}
	rec := g.reg.inputs[originKey{tenant, name}]
	taken := rec.dfa.Accepts(runeSeqOf(rec.value))
	chosen := b
	if taken {
		chosen = a
	}
	an, bn := g.nodes[a], g.nodes[b]
	n := &node{
		kind:     nCond,
		child:    a,
		childB:   b,
		selector: o,
		len:      g.nodes[chosen].len,
	}
	n.allVars = an.allVars.union(bn.allVars).set(o.ID)
	n.content = an.content.union(bn.content)
	n.dataCtl = an.dataCtl.union(bn.dataCtl)
	sel := newBset(len(g.reg.origins)).set(o.ID)
	n.branchCt = an.branchCt.union(bn.branchCt).union(sel)
	n.control = n.dataCtl.union(n.branchCt)
	id := g.intern(n)
	g.condKey[condKey{o.ID, a, b}] = id
	return id, nil
}

// Fold lowercases ASCII letters; every output rune maps 1:1 to its source,
// so provenance is preserved.
func (g *Graph) Fold(child NodeID) (NodeID, error) {
	if id, ok := g.foldKey[child]; ok {
		return id, nil
	}
	n := &node{kind: nFold, child: child, len: g.nodes[child].len}
	g.copySets(n, g.nodes[child])
	id := g.intern(n)
	g.foldKey[child] = id
	return id, nil
}

// Slice takes the half-open rune range [lo, hi). Bounds are host constants,
// so slicing changes neither content nor control provenance.
func (g *Graph) Slice(child NodeID, lo, hi int64) (NodeID, error) {
	cl := g.nodes[child].len
	if lo < 0 || hi < lo || hi > cl {
		return 0, fmt.Errorf("envguard: slice [%d,%d) out of range for length %d", lo, hi, cl)
	}
	if lo == hi {
		return g.Literal("")
	}
	if lo == 0 && hi == cl {
		return child, nil
	}
	if id, ok := g.sliceKey[sliceKey{child, lo, hi}]; ok {
		return id, nil
	}
	n := &node{kind: nSlice, child: child, lo: lo, hi: hi, len: hi - lo}
	g.copySets(n, g.nodes[child])
	id := g.intern(n)
	g.sliceKey[sliceKey{child, lo, hi}] = id
	return id, nil
}
