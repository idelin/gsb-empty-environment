# Debug Object Model

A TypeScript model of debugger-side object inspection: stable reference
handles over a graph of debuggee objects that may be shared or cyclic.

## Design

- **Virtual object graph** (`src/virtualGraph.ts`): a controlled stand-in
  for a debuggee heap. `VNode` objects have stable identity, can be shared
  by many parents, can form cycles, and support accessor (getter)
  properties whose invocations are counted by `VirtualGraph`.
- **Debug model** (`src/model.ts`): issues handles and serves expansions.

## Guarantees

- **Stable reference ids**: within a pause generation, the same object
  always interns to the same handle id (`obj@<generation>:<n>`). Shared and
  cyclic objects are returned as references to that id, never copied into
  independent objects. Expansion is lazy, so cycles terminate naturally.
- **Pause generations**: `pause()` starts a new generation and `resume()`
  ends it; both invalidate every outstanding handle. Resolving a stale
  handle throws `StaleHandleError`; malformed or never-issued ids throw
  `UnknownHandleError`. `isValid()` checks without throwing.
- **Paged, key-sorted children**: `expand(id, { offset, count })` returns a
  `PropertyPage` with `total` and `hasMore`. Keys sort with array indices
  numerically first, then strings by code unit, so pages are deterministic
  and disjoint slices tile the full key set.
- **Lazy getters**: accessor properties report `kind: 'accessor'` with a
  `(getter)` preview and are never invoked by default. Opt in per call with
  `expand(id, { invokeGetters: true })` or per property with
  `evaluateGetter(id, name)`.

## Tests

`test/model.test.ts` builds cycles, shared subgraphs, and tracked getters
on the virtual object graph and verifies identity, staleness, ordering,
paging, and getter laziness.

Requires Node >= 22.6 (type stripping). Run:

```sh
npm test
```
