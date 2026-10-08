# footnote-doc

A TypeScript library for Markdown-like documents: it parses **headings**,
**footnote references** (`[^name]`) and **footnote definitions**
(`[^name]: content`), produces diagnostics, and supports **incremental
updates** that report exactly which references were affected.

## Syntax

```markdown
# Heading level 1

A reference to [^later], defined further down.

[^later]: definitions may appear after their references
  continuation lines are indented by a tab or 2+ spaces
```

- Definitions may be **forward** (appear after the references that use them).
- Name matching is case-insensitive and collapses internal whitespace.
- A **duplicate definition** reports `duplicate-definition` and the **first
  definition is kept**; later duplicates never rewire existing references.
- A reference with no definition reports `undefined-reference`.
- Definitions whose contents reference each other (directly or transitively,
  including self-references) report `circular-footnote`.

## API

```ts
import { Document, parseDocument } from 'footnote-doc';

// Full parse (pure function)
const result = parseDocument(text);
result.headings; result.references; result.definitions; result.diagnostics;

// Incremental document
const doc = Document.from(text);
const { affectedReferences, diagnostics } = doc.applyEdit({
  range: { start: { line: 2, character: 0 }, end: { line: 3, character: 0 } },
  newText: '',
});
```

`applyEdit` re-parses only the blocks whose text changed (blocks are runs of
non-blank lines, matched across edits by common prefix/suffix), then
re-resolves symbols and recomputes diagnostics over the assembled state.

### Affected references

`affectedReferences` lists references whose **semantic** state changed:

- `added` / `removed` — the reference itself appeared or disappeared;
- `resolution-changed` — it flipped between resolved and unresolved
  (e.g. deleting a definition re-diagnoses **all** of its references);
- `target-changed` — the definition it resolves to changed content.

Pure position shifts are not reported, but all returned positions always
reflect the current text. Positions are 0-based `{ line, character }` with
half-open ranges.

## Correctness strategy

Every incremental update is cross-checked against a full re-parse
(`parseDocument`) in the test suite, covering forward references, circular
footnotes, and position changes — including a seeded fuzz test that applies
hundreds of random edits and asserts the incremental state (headings,
references, definitions, diagnostics) is identical to a full parse.

## Requirements & tests

Node.js >= 22.6 (runs TypeScript via type stripping; on 22.x keep
`--experimental-strip-types`, on >= 23.6 it works out of the box):

```sh
npm test
```

## Limitations

- LF line endings (`\n`) only.
- No backslash escaping of `[^`.
- Definition continuation lines must be indented and cannot span blank lines.
