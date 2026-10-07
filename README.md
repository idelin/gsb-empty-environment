# reorder

Go library for reordering a document model—modelled as a sequence of nodes
(paragraphs) with stable IDs—by applying a batch of delete, move and
modify operations atomically.

## Model

```go
type Node struct{ ID, Content string }
type Batch struct {
    Deletes  []string
    Moves    []Move    // {ID, After}: place ID immediately after After; "" = front
    Modifies []Modify  // {ID, Content}: replace content
}
result, err := reorder.Apply(base, batch)
```

Semantics:

- **Delete** removes nodes by ID.
- **Move** targets a predecessor by ID. If the predecessor was deleted in
  the same batch, the nearest surviving predecessor along the *old* order
  is used; if none survives, the node moves to the front. A moved node's
  own vacated position is skipped during this resolution.
- **Modify** merges content changes into the result, including on moved
  nodes.
- Move constraints must be acyclic (`ErrCyclicMove`); a node cannot be
  its own predecessor (`ErrSelfMove`); conflicting operations on the same
  node (delete+move, delete+modify, duplicates) return `ErrConflict`.
- Unmoved survivors keep their old relative order; multiple moves to the
  same anchor keep their old relative order; chained moves
  (`a` after `b`, `b` after `d`) are honoured.

`Apply` never mutates the input. See `reorder_test.go` for
node-sequence model checks of combined delete+move+modify batches.
