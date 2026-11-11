package parquetcol

import "fmt"

type readResult uint8

const (
	resValue readResult = iota
	resAbsent
	resEnd
)

type leafCursor struct {
	col   *columnData
	index int
	valAt int
}

func (c *leafCursor) rep() int { return c.col.reps[c.index] }
func (c *leafCursor) def() int { return c.col.defs[c.index] }

// assemble rebuilds records from shredded columns. Every record produces at
// least one entry per column (empty groups use synthetic leaves), so the row
// count drives the outer loop.
func assemble(schema *Schema, cols map[*Node]*columnData, numRecords int) ([]Value, error) {
	cursors := make(map[*Node]*leafCursor, len(schema.leaves))
	for _, leaf := range schema.leaves {
		cursors[leaf] = &leafCursor{col: cols[leaf]}
	}
	records := make([]Value, 0, numRecords)
	for row := 0; row < numRecords; row++ {
		fields := map[string]Value{}
		for _, child := range schema.Root.Children {
			v, res, err := readNode(child, cursors)
			if err != nil {
				return nil, err
			}
			if res == resValue {
				fields[child.Name] = v
			}
		}
		records = append(records, Struct(fields))
	}
	for _, leaf := range schema.leaves {
		c := cursors[leaf]
		if c.index != len(c.col.reps) {
			return nil, fmt.Errorf("parquetcol: column %q has %d unconsumed entries",
				leaf.Name, len(c.col.reps)-c.index)
		}
	}
	return records, nil
}

func firstLeaf(node *Node, cursors map[*Node]*leafCursor) *leafCursor {
	cur := node
	for !cur.IsLeaf {
		cur = cur.Children[0]
	}
	c := cursors[cur]
	if c.index >= len(c.col.reps) {
		return nil
	}
	return c
}

func skipNode(node *Node, cursors map[*Node]*leafCursor) {
	var walk func(n *Node)
	walk = func(n *Node) {
		if n.IsLeaf {
			cursors[n].index++
			return
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(node)
}

// readNode reads one occurrence of node for the current record.
func readNode(node *Node, cursors map[*Node]*leafCursor) (Value, readResult, error) {
	probe := firstLeaf(node, cursors)
	if probe == nil {
		return Null(), resEnd, nil
	}

	if node.IsLeaf {
		switch node.Repetition {
		case Repeated:
			return readRepeatedLeaf(node, probe)
		case Optional:
			c := probe
			def := c.def()
			c.index++
			if def < node.maxDef {
				return Null(), resAbsent, nil
			}
			if node.synthetic {
				return Struct(map[string]Value{}), resValue, nil
			}
			val := c.col.vals[c.valAt]
			c.valAt++
			return materializeLeaf(node, val), resValue, nil
		default:
			c := probe
			c.index++
			if node.synthetic {
				return Struct(map[string]Value{}), resValue, nil
			}
			val := c.col.vals[c.valAt]
			c.valAt++
			return materializeLeaf(node, val), resValue, nil
		}
	}

	switch node.Repetition {
	case Repeated:
		return readRepeatedGroup(node, cursors)
	case Optional:
		if isListWrapper(node) {
			return readListWrapper(node, cursors, probe)
		}
		if probe.def() <= node.defDepth {
			skipNode(node, cursors)
			return Null(), resAbsent, nil
		}
		v, err := readGroupFields(node, cursors)
		return v, resValue, err
	default:
		v, err := readGroupFields(node, cursors)
		return v, resValue, err
	}
}

// readListWrapper handles the canonical optional LIST wrapper group.
func readListWrapper(node *Node, cursors map[*Node]*leafCursor, probe *leafCursor) (Value, readResult, error) {
	listNode := node.Children[0]
	// Null list: definition level does not reach the repeated child.
	if probe.def() < listNode.defDepth {
		skipNode(node, cursors)
		return Null(), resAbsent, nil
	}
	return readRepeatedGroup(listNode, cursors)
}

func readGroupFields(node *Node, cursors map[*Node]*leafCursor) (Value, error) {
	fields := map[string]Value{}
	for _, child := range node.Children {
		if child.synthetic {
			c := firstLeaf(child, cursors)
			if c != nil {
				c.index++
			}
			continue
		}
		v, res, err := readNode(child, cursors)
		if err != nil {
			return Null(), err
		}
		if res == resEnd {
			return Null(), fmt.Errorf("parquetcol: unexpected end of column while reading %q", child.Name)
		}
		if res == resValue {
			fields[child.Name] = v
		}
	}
	return Struct(fields), nil
}

// readRepeatedGroup reads one list whose element is a group (or the canonical
// LIST "list" node). It stops at an empty-list marker or the next record.
func readRepeatedGroup(node *Node, cursors map[*Node]*leafCursor) (Value, readResult, error) {
	probe := firstLeaf(node, cursors)
	if probe == nil {
		return Null(), resEnd, nil
	}
	// Empty-list marker: def stops at this repeated node, no occurrence.
	if probe.def() == node.defDepth {
		skipNode(node, cursors)
		return List([]Value{}), resValue, nil
	}

	items := []Value{}
	for {
		probe = firstLeaf(node, cursors)
		if probe == nil {
			break
		}
		// The next record (or an empty/null marker for it) starts at the
		// inherited repetition level with a definition level not entering an
		// element of this list.
		if probe.rep() <= node.repDepth && probe.def() <= node.defDepth {
			break
		}
		if len(node.Children) == 1 {
			child := node.Children[0]
			v, res, err := readNode(child, cursors)
			if err != nil {
				return Null(), resEnd, err
			}
			if res == resEnd {
				break
			}
			if res == resAbsent {
				items = append(items, Null())
			} else {
				items = append(items, v)
			}
			continue
		}
		item, err := readGroupFields(node, cursors)
		if err != nil {
			return Null(), resEnd, err
		}
		items = append(items, item)
	}
	return List(items), resValue, nil
}

func readRepeatedLeaf(node *Node, c *leafCursor) (Value, readResult, error) {
	if c.def() == node.defDepth {
		c.index++
		return List([]Value{}), resValue, nil
	}
	items := []Value{}
	for c.index < len(c.col.reps) {
		if c.rep() <= node.repDepth && c.def() <= node.defDepth {
			break
		}
		rep := c.rep()
		def := c.def()
		c.index++
		if rep < node.repDepth {
			return Null(), resEnd, nil
		}
		if def < node.maxDef {
			items = append(items, Null())
			continue
		}
		val := c.col.vals[c.valAt]
		c.valAt++
		items = append(items, materializeLeaf(node, val))
	}
	return List(items), resValue, nil
}

func materializeLeaf(node *Node, v Value) Value {
	if node.Physical == TypeByteArray && node.IsString && v.kind == kindBytes {
		return String(string(v.bs))
	}
	return v
}
