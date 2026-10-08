# indent-config

A small indentation-based configuration language with a TypeScript parser,
a concrete syntax tree (CST) with byte-faithful reprint, and atomic validated edits.

## Language

- `key: value` is a leaf, `key:` is a block header whose children are the
  following, more-indented lines.
- Blank lines and `#` comments are skipped for hierarchy but preserved in the CST.
- Tabs advance to the next multiple of 8 (8-column tab stops).
- Two indent prefixes at the same visual column with different characters are
  an `ambiguous-indent` error; a dedent that matches no outer level is a
  `bad-dedent` error; a deeper indent not following a header is
  `unexpected-indent`. Errors carry 1-based line/column plus byte offsets, and
  parsing recovers at the next top-level line.

## Layout

- `src/indent.ts` — visual column computation, prefix comparison.
- `src/parser.ts` — line splitter (keeps original `\n` / `\r\n`), parser with an
  explicit indentation stack, diagnostics, recovery to the next top level.
- `src/cst.ts` — CST nodes owning exact byte ranges; `reprint(source, root) === source`.
- `src/verify.ts` — independent explicit-stack reference model; `flatten` +
  `crossCheck` compare the parser's hierarchy and error lines against it.
- `src/edit.ts` — `Document` with `setValue` / `insertBlock` / `removeNode`.
  Each edit is applied to a candidate, reparsed, and rejected atomically when
  it introduces new errors (e.g. indentation ambiguity); rejection diagnostics
  are mapped back to original-file coordinates. Only the edited byte range is
  reprinted; all other bytes are preserved.

## Tests

```
bun test
```

Covers tab-stop math, hierarchy cross-checks against the reference model
(including a seeded randomized corpus), error recovery, CST byte fidelity,
single-leaf reprints, insert/remove of whole nested blocks, atomic rejection,
and parse → modify → reprint → reparse round trips.
