# ts-breakpoint-anchor

A TypeScript breakpoint library that anchors breakpoints to **statements**
instead of raw text lines, and carries the (line, character) anchors through
a chain of edits using an explicit statement identity model.

## Identity model

Each edit produces a new *version* of the source. Statements are extracted
per version and given a content-based **key**:

- simple statement: normalized token text (comments/whitespace removed),
- compound statement (`if`/`for`/`function`/...): normalized header text, so
  edits inside the body don't churn the identity of the enclosing statement.

Keys are position-independent. Between versions, identity (a persistent
`sid`) is propagated by `reconcile()`:

1. **LCS alignment** over the key sequences keeps identity for statements
   whose relative order is unchanged. Duplicated keys pair positionally, so
   copying a statement never steals the original's identity.
2. Remaining old/new statements with equal keys pair up as **moves** —
   identity (and breakpoints) follow the statement to its new location.
3. Unmatched old statements are **deleted** (identity retired); unmatched
   new statements get fresh identities.

## Breakpoint semantics

- **Bound** — the breakpoint's statement exists; the anchor tracks it
  through the edit chain (insert/remove lines around it, move it).
- **Delete → unbound** — deleting the statement retires its identity. The
  breakpoint keeps its last known position for display but is *not* moved
  onto whatever unrelated statement now occupies that line.
- **Delete → restore → rebind** — if a statement with the exact key
  reappears (and the key was absent in the previous version), the
  breakpoint rebinds at the restored location.
- **Copy** — duplicating a statement gives the copy a fresh identity; the
  breakpoint stays on the original occurrence. Deleting one of two
  identical copies does not make the breakpoint jump to the survivor.
- **Cross-version mapping** — every version snapshots all breakpoints;
  `getBreakpointAtVersion(id, version)` maps state across versions.

## API

```ts
import { BreakpointStore } from './src/index.ts';

const store = new BreakpointStore(source);        // version 1
const bp = store.addBreakpoint(4, 3);             // 1-based line/character
store.applyEdit(editedSource);                    // version 2, re-anchored
store.getBreakpoint(bp.id);                       // current state
store.getBreakpointAtVersion(bp.id, 1);           // state as of version 1
```

Positions are 1-based `(line, character)`, matching `tsc` conventions.

## Limitations

The built-in statement extractor is a tokenizer-based segmenter, not a full
TypeScript parser. Known limits: regex literals are detected heuristically;
statements nested inside expressions (e.g. arrow bodies inside call
arguments) are not extracted; class bodies are treated as object literals
(field initializers are still extracted as statements). The identity model
(`reconcile`, `BreakpointStore`) is parser-agnostic — any extractor
producing `{ key, start, end }` statements can be plugged in.

## Tests

Requires Node ≥ 22.6 (type stripping):

```sh
npm test
```
