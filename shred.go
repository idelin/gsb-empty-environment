package parquetcol

import (
	"fmt"
)

// columnData holds the shredded Dremel representation of one leaf column for a
// set of records.
type columnData struct {
	node *Node
	reps []int
	defs []int
	vals []Value // defined entries only, in column order
}

func newColumnData(node *Node) *columnData {
	return &columnData{node: node}
}

// shredRecord shreds one record value (a Struct) into the leaf columns.
func shredRecord(schema *Schema, record Value, cols map[*Node]*columnData) error {
	if record.IsNull() || record.kind != kindStruct {
		return fmt.Errorf("parquetcol: top-level record must be a struct, got %s", record.GoString())
	}
	return shredNode(schema.Root, record, 0, 0, cols)
}

// shredNode recursively appends entries for node. rep/def are the repetition
// and definition levels reached by the path so far (excluding node itself).
func shredNode(node *Node, v Value, rep, def int, cols map[*Node]*columnData) error {
	if node.IsLeaf {
		if node.Repetition == Repeated {
			if v.IsNull() {
				emitNullMarker(node, rep, def, cols)
				return nil
			}
			if v.kind != kindList {
				return fmt.Errorf("parquetcol: field %q expects a list, got %s", node.Name, v.GoString())
			}
			if len(v.list) == 0 {
				emitNullMarker(node, rep, def, cols)
				return nil
			}
			for i, item := range v.list {
				itemRep := rep
				if i > 0 {
					itemRep = node.repDepth + 1
				}
				col := cols[node]
				col.reps = append(col.reps, itemRep)
				leafDef := def + 1
				if item.IsNull() {
					col.defs = append(col.defs, leafDef)
					continue
				}
				if err := checkLeafType(node, item); err != nil {
					return err
				}
				col.defs = append(col.defs, node.maxDef)
				col.vals = append(col.vals, item)
			}
			return nil
		}
		col := cols[node]
		col.reps = append(col.reps, rep)
		if v.IsNull() {
			if node.Repetition == Required {
				return fmt.Errorf("parquetcol: required field %q is missing", node.Name)
			}
			col.defs = append(col.defs, def)
			return nil
		}
		if err := checkLeafType(node, v); err != nil {
			return err
		}
		leafDef := def
		if node.Repetition != Required {
			leafDef++
		}
		col.defs = append(col.defs, leafDef)
		if node.synthetic {
			col.vals = append(col.vals, Bool(false))
		} else {
			col.vals = append(col.vals, v)
		}
		return nil
	}

	if node.Repetition == Repeated {
		if v.IsNull() {
			emitNullMarker(node, rep, def, cols)
			return nil
		}
		if v.kind != kindList {
			return fmt.Errorf("parquetcol: field %q expects a list, got %s", node.Name, v.GoString())
		}
		if len(v.list) == 0 {
			emitNullMarker(node, rep, def, cols)
			return nil
		}
		for i, item := range v.list {
			itemRep := rep
			if i > 0 {
				itemRep = node.repDepth + 1
			}
			if len(node.Children) == 1 && node.Children[0].IsLeaf {
				// Parquet LIST wrapper: the repeated "list" group has a single
				// primitive element leaf, so the item is the leaf value.
				if err := shredNode(node.Children[0], item, itemRep, def+1, cols); err != nil {
					return err
				}
				continue
			}
			if len(node.Children) == 1 {
				// LIST wrapper whose element is a group: the item is that
				// element group's value.
				if err := shredNode(node.Children[0], item, itemRep, def+1, cols); err != nil {
					return err
				}
				continue
			}
			if err := shredGroupChildren(node, item, itemRep, def+1, cols); err != nil {
				return err
			}
		}
		return nil
	}

	{
		if v.IsNull() {
			if node.Repetition == Optional {
				emitNullMarker(node, rep, def, cols)
				return nil
			}
			return fmt.Errorf("parquetcol: required field %q is missing", node.Name)
		}
		if v.kind != kindStruct {
			return fmt.Errorf("parquetcol: field %q expects a struct, got %s", node.Name, v.GoString())
		}
		nextDef := def
		if node.Repetition != Required {
			nextDef++
		}
		return shredGroupChildren(node, v, rep, nextDef, cols)
	}
}

// shredGroupChildren walks the children of a present group node.
func shredGroupChildren(node *Node, v Value, rep, def int, cols map[*Node]*columnData) error {
	if v.IsNull() || v.kind != kindStruct {
		return fmt.Errorf("parquetcol: field %q expects a struct, got %s", node.Name, v.GoString())
	}
	for _, child := range node.Children {
		if isListWrapper(child) {
			listChild := child.Children[0]
			childVal, ok := v.stct[child.Name]
			if !ok {
				childVal = Null()
			}
			nextDef := def
			if child.Repetition != Required {
				nextDef++
			}
			if childVal.IsNull() {
				// Null list: stop at the optional wrapper so it stays
				// distinguishable from an empty repeated list.
				emitNullMarker(child, rep, def, cols)
				continue
			}
			if err := shredNode(listChild, childVal, rep, nextDef, cols); err != nil {
				return err
			}
			continue
		}
		childVal, ok := v.stct[child.Name]
		if !ok && child.synthetic {
			// Present empty group: materialize the synthetic sentinel once.
			childVal = Bool(false)
		}
		if err := shredNode(child, childVal, rep, def, cols); err != nil {
			return err
		}
	}
	return nil
}

// emitNullMarker appends one absent entry to every leaf beneath a group node.
func emitNullMarker(node *Node, rep, def int, cols map[*Node]*columnData) {
	var walk func(n *Node)
	walk = func(n *Node) {
		if n.IsLeaf {
			col := cols[n]
			col.reps = append(col.reps, rep)
			col.defs = append(col.defs, def)
			return
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(node)
}

func checkLeafType(node *Node, v Value) error {
	want := kindBytes
	switch node.Physical {
	case TypeBoolean:
		want = kindBool
	case TypeInt32:
		want = kindInt32
	case TypeInt64:
		want = kindInt64
	case TypeFloat:
		want = kindFloat
	case TypeDouble:
		want = kindDouble
	case TypeByteArray:
		if v.kind != kindBytes && v.kind != kindString {
			return fmt.Errorf("parquetcol: field %q expects bytes/string, got %s", node.Name, v.GoString())
		}
		return nil
	}
	if v.kind != want {
		return fmt.Errorf("parquetcol: field %q expects physical type %d, got %s", node.Name, node.Physical, v.GoString())
	}
	return nil
}
