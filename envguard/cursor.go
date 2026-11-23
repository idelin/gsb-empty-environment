package envguard

// cursor is an explicit-stack sequential walker over a rope. Advancing is
// amortized constant work and never expands loops or shared nodes, which is
// what makes streaming normalization/checks viable at the 2^44 length limit.
type frame struct {
	id NodeID
	i  int64 // local rune offset inside id
	// for concat walking
	part int
}

type cursor struct {
	g     *Graph
	stack []frame
	pos   int64
}

func (g *Graph) newCursor(id NodeID) *cursor {
	return &cursor{g: g, stack: []frame{{id: id}}}
}

// newCursorAt starts a stream at global rune offset start.
func (g *Graph) newCursorAt(id NodeID, start int64) *cursor {
	c := g.newCursor(id)
	if start > 0 {
		c.seek(start)
	}
	return c
}

// seek advances the stream by skipping count runes.
func (c *cursor) seek(count int64) {
	for k := int64(0); k < count; k++ {
		if _, _, ok := c.next(); !ok {
			return
		}
	}
}

// next returns the next rune and its global offset, or ok=false at the end.
func (c *cursor) next() (r rune, off int64, ok bool) {
	for len(c.stack) > 0 {
		top := len(c.stack) - 1
		f := &c.stack[top]
		n := c.g.nodes[f.id]
		switch n.kind {
		case nLiteral, nVar:
			if f.i >= n.len {
				c.stack = c.stack[:top]
				continue
			}
			r, _ := c.g.runeAt(f.id, f.i)
			off = c.pos
			f.i++
			c.pos++
			return r, off, true
		case nConcat:
			if f.part >= len(n.parts) {
				c.stack = c.stack[:top]
				continue
			}
			child := n.parts[f.part]
			f.part++
			c.push(child, 0)
		case nLoop:
			if f.i >= n.len {
				c.stack = c.stack[:top]
				continue
			}
			bl := c.g.nodes[n.body].len
			if bl == 0 {
				c.stack = c.stack[:top]
				continue
			}
			local := f.i % bl
			body := c.g.nodes[n.body]
			if body.kind == nLiteral || body.kind == nVar ||
				body.kind == nFold || body.kind == nSlice || body.kind == nSanitize {
				r, _ := c.g.runeAt(n.body, local)
				off = c.pos
				f.i++
				c.pos++
				return r, off, true
			}
			f.i++
			c.push(n.body, local)
		case nCond:
			c.push(c.g.chosenBranch(n), f.i)
			f.i = c.g.nodes[f.id].len // descend once
		case nFold:
			if f.i >= n.len {
				c.stack = c.stack[:top]
				continue
			}
			r, _ := c.g.runeAt(f.id, f.i)
			off = c.pos
			f.i++
			c.pos++
			return r, off, true
		case nSlice:
			if f.i >= n.len {
				c.stack = c.stack[:top]
				continue
			}
			r, _ := c.g.runeAt(f.id, f.i)
			off = c.pos
			f.i++
			c.pos++
			return r, off, true
		case nSanitize:
			if f.i >= n.len {
				c.stack = c.stack[:top]
				continue
			}
			r, _ := c.g.runeAt(f.id, f.i)
			off = c.pos
			f.i++
			c.pos++
			return r, off, true
		}
	}
	return 0, 0, false
}

func (c *cursor) push(id NodeID, i int64) {
	c.stack = append(c.stack, frame{id: id, i: i})
}
