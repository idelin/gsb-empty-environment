# line-pagination

Line-height based table pagination for TypeScript.

## Features

- **Paginate by line height** — rows/blocks are packed into pages of a fixed
  height; every placed row gets an `offsetY`.
- **Repeating header** — `headerHeight` is reserved at the top of every page.
- **Unbreakable merged-cell groups** — rows glued by a merged cell form an
  atomic `group(...)` that is never split across pages.
- **Overflow reported, not looped** — a group taller than
  `pageHeight - headerHeight` can never fit, so `paginate` throws
  `PaginationOverflowError` naming the group (id, index, row ids, required vs
  available height) instead of emitting empty pages forever.
- **Last-page footnote** — `footnoteHeight` is reserved on the last page only.
  The last page takes the longest row suffix that fits beside the footnote and
  the remaining prefix is packed greedily; this is provably page-count optimal
  (prefix page count is monotone non-increasing as the suffix grows). A
  trailing block too tall to share a page with the footnote yields a
  footnote-only final page.
- **Exhaustive small-scale verification** — `enumeratePartitions` /
  `verifyWithEnumeration` brute-force every contiguous partition (blocks stay
  atomic and in order) to confirm the greedy result, including last-page
  footnote reflow, uses the minimum possible number of pages. A virtual
  zero-height trailing block models the footnote-only final page.

## Usage

```ts
import { paginate, table, line, group } from "line-pagination";

const spec = table(24, [
  line(12, "r1"),
  group("merged-address", [12, 12, 12]), // merged cell spanning 3 rows
  line(12, "r2"),
]);

const result = paginate(spec, { pageHeight: 96, footnoteHeight: 18 });
for (const page of result.pages) {
  // page.blocks[].rows[].offsetY, page.footnoteHeight (last page only), ...
}
```

## Tests

```sh
npm test   # node --test (Node >= 23 runs the .ts sources directly)
```

Includes randomized cross-checks of the greedy paginator against exhaustive
partition enumeration on small inputs.
