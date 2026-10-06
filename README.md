# debug-variable-model

A debug-time variable inspection model (DAP-style) for TypeScript.

## Semantics

- **Shared & cyclic objects** — identity is preserved. Within one pause
  generation the same object always maps to the same `reference` handle and
  `objectId`, so a shared child reached from two parents is one object, never
  copied into independent nodes. Cycles are observable by comparing
  `objectId`s, and `expand(..., { ancestors })` flags back-edges with
  `cycle: true`.
- **Pause generations** — `beginPause()` bumps the generation. Handles minted
  under an older generation are invalid: expanding one raises
  `StaleHandleError` (previous generation retained for diagnostics; older
  ones are pruned and raise `InvalidHandleError`).
- **Paginated, key-sorted children** — `expand(ref, { start, count })` returns
  a page of children sorted by key: integer-like keys numerically first, then
  string keys in code-unit order. The result carries `total` for paging UI.
- **Lazy getters** — accessor properties are not invoked by default; they
  surface as `kind: 'getter'` placeholders. Pass `invokeGetters: true` to
  evaluate them; throwing getters are reported in the node's `value`.

## Layout

- `src/model.ts` — the model (`DebugVariableModel`, `VariableNode`, errors).
- `test/model.test.ts` — tests driven by a virtual object graph
  (shared child, self/array cycles, counted getter) verifying identity,
  cycles, stale handles, sorting/pagination, and getter laziness.

## Test

Requires Node >= 22.6 (runs TypeScript via type stripping; sources use
erasable syntax only):

```sh
npm test
```
