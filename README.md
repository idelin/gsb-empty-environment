# diagnostic-version-vector

Editor diagnostics manager for continuously edited files whose background
diagnostic computations may resolve **out of order**. Written in TypeScript
(erasable-syntax-only, runs directly on Node ≥ 23.6 type stripping).

## Core idea

A diagnostic set is never validated by the primary file's version alone.
Every computation builds a **version vector**: a map from every file it
touched (the primary file plus each dependency read through
`ComputeContext.read`) to a `FileStamp { version, contentHash }`, or `null`
for "file was absent at compute time".

A diagnostic set is displayed only while its vector still matches the
current document state:

- recorded stamp → file open with equal `(version, contentHash)`;
- recorded `null` → file still absent.

`version` is monotonic within one open session but resets on reopen, so the
`contentHash` component is what makes stamps comparable across close/reopen
cycles — and what rejects a stale result when a file was reopened to the
same version number with different content.

## Behavior guarantees

- **Out-of-order results**: a stale result is discarded on arrival, whether
  or not a newer result was already published (`src/diagnosticManager.ts`
  `completeRequest` re-validates the vector).
- **Dependency drift**: editing, closing, or deleting any recorded
  dependency eagerly invalidates displayed diagnostics and cancels pending
  requests that read it; unrelated files never invalidate.
- **Cancellation failure tolerated**: edits cancel pending request tokens,
  but a computer that ignores cancellation still cannot publish — the
  version vector is the single source of truth.
- **Reuse (exact condition)**: cached diagnostics are reused without
  recomputation iff `vectorMatches(cached.vector, currentState)` — the file
  and all recorded dependencies are open with identical stamps and all
  files recorded absent are still absent. This survives close/reopen with
  unchanged content (and dependency delete/restore with identical content),
  while any drift forces recomputation. The display layer is invalidated
  eagerly; the reuse cache is validated lazily at request time.

## Layout

- `src/versionVector.ts` — `FileStamp`, `VersionVector`, `vectorMatches`
- `src/diagnosticManager.ts` — `DiagnosticManager`, `ComputeContext`
- `src/cancellation.ts` — `CancellationTokenSource`
- `test/diagnosticManager.test.ts` — 17 tests covering the guarantees above

## Run

```sh
npm test        # node --test (no build step, no dependencies)
npm run typecheck  # requires a local typescript install
```
