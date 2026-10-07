# focus-traversal

TypeScript tool that computes sequential focus (tab) traversal order over a
virtual node tree.

## Rules

- `tabIndex > 0` — visited first, ordered by value, ties broken by document order.
- `tabIndex === 0` (or `focusable: true` without a tabIndex) — visited next, in
  depth-first document order.
- `tabIndex < 0` — never in the traversal order; programmatically focusable only.
- A `hidden` node makes its entire subtree unfocusable (no exemptions).
- A `disabled` node forms a disabled group: its subtree is unfocusable, except
  nodes marked `exempt: true`, which lift the barrier for their own subtree.
- Node ids must be unique; duplicates are rejected at construction, insertion,
  and rename with `DuplicateIdError`.

## API

```ts
import { FocusTree } from "./src/focus-tree.ts";

const tree = new FocusTree([
  { id: "save", tabIndex: 1 },
  { id: "cancel", tabIndex: 0 },
  { id: "logo", tabIndex: -1 },
]);

tree.traversalOrder();     // ["save", "cancel"]
tree.programmaticOnly();   // ["logo"]

// Dynamic attribute updates trigger full-tree re-expansion on next read.
tree.updateAttributes("cancel", { tabIndex: 2 });
tree.updateAttributes("save", { hidden: true });
tree.addNode(null, { id: "extra", focusable: true });
tree.renameId("extra", "extra-2"); // throws DuplicateIdError if taken
```

## Test

Requires Node >= 22.6 (native TypeScript type stripping):

```sh
npm test
```
