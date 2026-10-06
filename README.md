# doc-marks

A small TypeScript library for documents made of text nodes with format marks.
It provides normalization, range-based mark operations, and undoable batch
edits, with a flat character/mark model as the ground truth for equivalence
checks.

## Model

```ts
interface DocNode {
  id: string;                 // stable node identity
  text: string;
  marks: ReadonlySet<string>; // unordered: "bold", "italic", "link:<url>", ...
  anchors: Anchor[];          // { id, affinity: "left" | "right", offset }
}
interface Doc { nodes: DocNode[] }
```

- Mark sets are **unordered**; comparisons are order-insensitive.
- **Links never nest**: a node may carry at most one `link`/`link:*` mark
  (`validateDoc`, and `addMark` throws `LinkNestingError` on overlap).
- Anchors pin to a character boundary; affinity decides which side they
  follow when a split lands exactly on them.

## Normalization

`normalize(doc)` → `{ doc, map }`:

- merges adjacent nodes with equal mark sets (anchor offsets are shifted);
- drops empty nodes **unless they carry anchors** — anchored empty nodes are
  always preserved and act as merge barriers;
- `map[i]` gives each original node's `{ nodeStart, nodeEnd, charStart }`
  range in the result (`null` for dropped nodes);
- idempotent: normalizing twice is a fixed point (`isNormalized`).

## Equivalence

`flatten(doc)` reduces a doc to rendered characters, a per-character sorted
mark sequence, and anchors at character boundaries. `equivalent(a, b)`
compares exactly that — node identity and mark insertion order are ignored.

## Mark operations

```ts
addMark(doc, start, end, "bold");
removeMark(doc, 2, 5, "italic");
applyBatch(doc, [
  { kind: "add", start: 0, end: 4, mark: "bold" },
  { kind: "add", start: 2, end: 6, mark: "italic" }, // original coordinates
]);
```

- Ranges are half-open character offsets into the rendered text.
- Nodes are split at range boundaries and regrouped into maximal same-format
  runs (split-then-merge), so results are normalized.
- All edits in a batch are expressed in **original-document coordinates**.
- The first surviving piece of a node keeps its `id`; later pieces get
  `<id>~<n>`. `tx.undo()` restores the exact original nodes — identity and
  anchors included.

## Tests

Requires a Node.js version with native TypeScript type stripping
(v23.6+; developed on v24). No dependencies to install.

```sh
npm test
```

The suite covers normalization (merge/drop/keep, mapping, idempotency),
mark operations (split/merge, anchor affinity, link nesting, reversibility),
batch edits in original coordinates with undo, and randomized property tests
checked against the flat character/mark model.
