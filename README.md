# multi-header-grid

Multi-level table header engine in pure TypeScript: leaf columns can be hidden
and reordered (within or across groups), groups can be collapsed, and every
edit is committed as a transaction that atomically publishes one consistent
snapshot of header spans (rowspan/colspan), the keyboard navigation tree, and
the leaf-column → data-index mapping. No browser layout engine is involved:
the node tree and glyph metrics are fixtures.

Requires Node.js >= 23.6 (runs TypeScript directly via type stripping; tests
use the built-in `node:test` runner).

```sh
npm test          # node --test 'test/*.test.ts'
npm run demo      # node examples/demo.ts
```

## Model

- `src/types.ts` — the editable `HeaderDoc` (ownership lives in the tree;
  `hidden` / `collapsed` are facet sets) and the immutable `HeaderSnapshot`.
- `src/derive.ts` — main derivation: prunes empty groups (a group with zero
  visible leaves disappears, so no zero-colspan cell is ever emitted), turns
  collapsed groups into single terminal `group-summary` columns, computes
  rowspan/colspan, the keyboard tree (grid occupancy: up/down/left/right), and
  the data-column mapping.
- `src/ops.ts` — `hide` / `show` / `move` / `setCollapsed` ops. Every op
  records its exact inverse; `HeaderStore.commit` applies a whole transaction
  and derives exactly one snapshot, so spans, keyboard tree and data mapping
  are always published together. `undo()` replays inverses in reverse order.
- `src/selection.ts` — maps a selection region from an old snapshot version
  onto a newer one: contiguous runs of surviving column keys become candidate
  regions (a reorder can split one region into several); any missing key
  invalidates the mapping and is reported.
- `src/reference.ts` — an independent reference implementation that flattens
  the document into the ordered visible leaf sequence and rebuilds the grid by
  merging equal group ids level by level, plus snapshot diffing and invariant
  checks (contiguous data indices, positive spans, terminal-cell coverage,
  nav sanity). The differential test drives 300 random transactions and
  compares both engines after every commit, then undoes everything and
  requires the initial state back.
- `src/fixtures.ts` — the fixture node tree (including two distinct columns
  both named "Score") and fixture glyph metrics used for all width math.

## Invariants

- Header cells and data columns stay index-consistent: bottom-level terminal
  cell at column `i` is exactly `columns[i]`, and `dataIndex` is `0..n-1`.
- Hiding every leaf of a group removes the group (and empty ancestors).
- Cross-group moves reassign ownership explicitly (`leafOwner` paths).
- Identity is by id, never by display name.
