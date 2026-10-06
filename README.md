# canvas-paste

Canvas subgraph copy/paste library (TypeScript, zero dependencies).

## Behaviour

- **Copy** snapshots the selected nodes (deep, immutable) into a `Clipboard`.
- **Paste** is atomic and works as follows:
  - every copied node receives a fresh id from the **public id sequence**
    (`IdAllocator`);
  - references between copied nodes (**internal**) are rewritten to the new
    ids, so the pasted subgraph is isomorphic to the copied one;
  - references to nodes outside the copied set (**external**) are kept only
    when the target still exists on the canvas **and** its type matches the
    reference's expected type; otherwise the reference is marked
    `status: "broken"` (with `brokenTarget` kept for diagnostics);
  - on any failure the canvas is restored and the reservation is rolled
    back, so a failed paste **never consumes public ids**.
- **Undo/redo**: `PasteCommand` caches the ids allocated on its first
  execution, so undo/redo cycles never draw additional public ids.

## Layout

- `src/model.ts` — node/ref model and the `Canvas` (capacity invariant).
- `src/ids.ts` — `IdAllocator` with transactional `reserve` / `commit` /
  `rollback`.
- `src/clipboard.ts` — `copy` / `paste` and reference fixup.
- `src/history.ts` — `PasteCommand`, `History` (undo/redo stacks).
- `test/helpers.ts` — `assertIsomorphic`: bijection + type/props +
  internal-reference structure check.
- `test/paste.test.ts` — id allocation, internal/external ref fixup, broken
  links, failure atomicity, isomorphism and undo/redo tests.

## Running the tests

Requires Node >= 22.6 (TypeScript type stripping; sources use
erasable-syntax-only TypeScript). With Node >= 23.6 no flag is needed:

```sh
npm test            # node --test test/*.test.ts
```

For Node 22.6–22.17 run with `node --experimental-strip-types --test test/*.test.ts`.
