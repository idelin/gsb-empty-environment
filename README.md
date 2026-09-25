# line-diff-session

Maintains line-level diff blocks between two long texts. Supports local
edits on either side with incremental refresh, context folding, and
bidirectional line-number mapping. Pure TypeScript, zero dependencies —
runs directly on Node.js ≥ 23 via native type stripping.

## Features

- **Diff blocks**: Myers O(ND) line diff with common prefix/suffix trim, so
  local edits only re-diff the affected middle region.
- **Deterministic tie-break**: deletions are preferred over insertions on
  ties, so repeated lines (including runs of blank lines) always align
  their earliest occurrences and diffs are stable across runs.
- **Bidirectional mapping**: `aToB` / `bToA`; deleted or inserted lines map
  to `null` (never to a neighbouring line).
- **Context folding**: `folds(context)` collapses long unchanged regions,
  keeping `context` lines around each change; mapping stays valid through
  folds.
- **UTF-16 offsets**: all public offsets are UTF-16 code-unit offsets;
  edits that would split a surrogate pair are rejected.
- **LF + CRLF**: both terminators are recognized (and may be mixed); line
  comparison ignores the terminator, so identical content with different
  EOL styles produces no diff.
- **Revert-safe**: undoing an edit removes the diff blocks and restores
  the identity mapping.

## Usage

```ts
import { DiffSession } from "./src/index.ts";

const session = new DiffSession(textA, textB);
session.getChangeBlocks();   // differing regions
session.folds(3);            // collapsible unchanged regions (context = 3)
session.aToB(10);            // A line 10 -> B line, or null
session.applyEdit("a", { start: 100, end: 105, text: "new\n" }); // UTF-16 offsets
```

## Scripts

```sh
npm test    # node --test test/*.test.ts
npm run demo
```

## Layout

- `src/text.ts` — line splitting (LF/CRLF), UTF-16 offsets, surrogate safety
- `src/diff.ts` — Myers diff with deterministic tie-break, block building
- `src/mapping.ts` — bidirectional line mapping, context folding
- `src/session.ts` — `DiffSession`: edits, blocks, mapping, folds
- `test/` — node:test suites
- `examples/demo.ts` — runnable walkthrough
