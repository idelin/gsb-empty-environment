# chat-list-layout

TypeScript layout engine for chat lists with variable-height items that are
measured on demand. It keeps the scroll position stable while history is
prepended, heights are corrected, and items are removed.

## Concepts

- **`ChatListLayout`** (`src/layout.ts`) — the layout model. Unmeasured items
  contribute `estimatedHeight`; `measure(key, height)` applies a height
  correction as a point update. Prefix sums live in a Fenwick tree
  (`src/fenwick.ts`), so `topOf`, `totalHeight` and offset-based lookup are
  O(log n). `locate(offset)` positions by offset: an offset exactly on an
  item boundary belongs to the item starting there; out-of-range offsets are
  clamped.
- **Anchoring** (`src/anchor.ts`) — `captureAnchor` records the first visible
  item, the scroll offset inside it, and its neighbours. `resolveAnchor`
  restores the visual position after a mutation: if the anchor still exists
  its top plus inner offset is restored; if it was deleted its original
  successor is top-aligned; with no successor the predecessor's bottom edge
  is used; an empty list resets to 0.
- **`ChatViewport`** (`src/viewport.ts`) — wraps the layout and runs every
  mutation (prepend/append/insert/remove/measure) through the anchor
  machinery. `scrollTo(offset)` positions by offset, clamped to
  `[0, maxOffset]` where `maxOffset = max(0, totalHeight - viewportHeight)`.

## Example

```ts
import { ChatListLayout, ChatViewport } from "chat-list-layout";

const layout = new ChatListLayout({ estimatedHeight: 56 });
const viewport = new ChatViewport(layout, { viewportHeight: 480 });

viewport.append([{ key: "m1" }, { key: "m2" }]);
viewport.scrollTo(120);
viewport.prepend([{ key: "m0", height: 80 }]); // anchor keeps m? stable
viewport.measure("m1", 132); // height correction above the viewport
```

## Testing

Tests run with the built-in Node test runner via type stripping — Node
>= 22.6 (`--experimental-strip-types`) or >= 23.6 (no flag needed):

```sh
npm test
```

- `test/layout.test.ts` — Fenwick tree and layout unit tests, including
  boundary rules for `locate`.
- `test/anchor.test.ts` — deterministic anchoring scenarios: prepends,
  height corrections, anchor deletion fallbacks, scroll clamping.
- `test/differential.test.ts` — continuous random batches of updates and
  boundary scrolling, cross-checked against `test/naive.ts`, a reference
  model that recomputes full prefix sums on every query.

`npm run typecheck` runs `tsc --noEmit` when the `typescript` package is
available.
