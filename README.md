# indented-config

A small indentation-based configuration language with a full-fidelity
concrete syntax tree (CST), validated edits and incremental reprint.
Written in dependency-free TypeScript (erasable syntax only).

## Language

```
# a comment
server:                  # block opener: 'key:' with no value
    host: example.com    # leaf entry:   'key: value'
    tls:
        enabled: false   # nested block, one level deeper

database:
    url: postgres://localhost/db
```

Rules:

- Blocks are formed by indentation only. A `key:` line (no value) opens a
  block when the next logical line is more indented.
- Tabs advance to **8-column tab stops**; the visual column of a prefix is
  computed with tab stops, not character counts.
- **Ambiguity**: two indentation prefixes with the same visual column but
  different characters (e.g. 8 spaces vs. one tab) are an error.
- **Dedent** must land exactly on an enclosing indentation level.
- Blank lines and `#` comments carry no structure (but are preserved).
- Any error is reported with line/column and parsing **recovers at the
  next top-level (column 0) logical line**.

## API

- `parse(src)` → `{ file: CstFile, diagnostics: Diagnostic[] }`
  Full-fidelity CST: every physical line keeps its raw bytes, so
  `reprint(file) === src` always holds, even for erroneous documents.
- `semantics(file)` → `SemNode[]` — the block/value tree.
- `referenceAnalyze(src)` — independent explicit-indent-stack reference
  model used to differentially validate hierarchy and error recovery.
- Edits (`src/edit.ts`), all atomic and validated by re-parsing:
  - `setValue(doc, path, value)` — change a leaf value; only that line is
    re-serialized, every other byte is preserved.
  - `removeBlock(doc, path)` — delete an entry with its whole nested block.
  - `addBlock(doc, path, blockText)` — insert a nested block (relative
    indentation) under a parent, re-indented to match its siblings.
  - On success you get the new document plus the changed byte range (in
    both old and new coordinates). If an edit would introduce an error
    (e.g. an ambiguous indent prefix), it is **rejected atomically** and
    the diagnostics are mapped back to ranges in the *original* file.

## Tests

Requires Node ≥ 22.18 (native TypeScript type stripping). No dependencies.

```
npm test          # or: node tests/run.ts
```

- `tests/parser.test.ts` — grammar, tab stops, ambiguity, dedent matching,
  diagnostics with line/col, error recovery, byte-for-byte reprint.
- `tests/reference.test.ts` — differential fuzz (3000 seeded documents)
  of the parser against the explicit-stack reference model: hierarchy,
  error sets and recovery to the next top level.
- `tests/edit.test.ts` — local reprint (only the touched region changes),
  atomic rejection with original-file diagnostics, add/remove of whole
  nested blocks, and randomized parse → modify → reprint → reparse round
  trips checking semantics and untouched bytes.
