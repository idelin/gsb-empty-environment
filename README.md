# versioned-result-cache

Immutable result cache for expensive computations that depend on
input-array versions and algorithm parameters. TypeScript, zero runtime
dependencies.

## Design

- **True underlying versioning** — `VersionRegistry` keeps the version in a
  `WeakMap` keyed by the `ArrayBuffer` itself, so every view (subarray) over
  one buffer shares the same version. A cache key is
  `buf#<id>@v<version>+<byteOffset>:<byteLength>|<canonical params>`: the
  buffer identity/version plus the view's window.
- **Param-safe keys** — `canonicalize` serializes params deterministically
  (sorted keys, type tags), so `{a:1,b:2}` hits `{b:2,a:1}` but `1` never
  collides with `"1"`.
- **Immutability** — the cache stores a private copy of every published
  result and hands each caller a fresh copy; mutating a returned array
  cannot corrupt the cache or other callers. In-flight computations also
  receive a snapshot of the input, so later overwrites cannot leak into a
  running task.
- **Request coalescing** — concurrent requests for the same key join one
  in-flight computation (`join` in the ledger).
- **Cancellation** — each subscriber cancels independently; when the last
  subscriber cancels, the computation is aborted and any late-arriving
  result is dropped (`cancel-drop`), never published.
- **Stale suppression** — if the buffer version moved while a task was
  running, its result is still delivered to its own callers but is not
  published (`stale-discard`).
- **Quota** — `maxEntries` bounds the cache; least-recently-used entries
  are evicted (`evict`).
- **Hit ledger** — every decision (`miss`, `join`, `hit`, `compute-start`,
  `publish`, `stale-discard`, `cancel-drop`, `abort`, `evict`, `error`) is
  appended to a sequence-numbered ledger that tests assert against and the
  demo prints.

## Layout

- `src/versionRegistry.ts` — per-buffer version tracking shared by views
- `src/canonicalize.ts` — deterministic param serialization
- `src/resultCache.ts` — the cache itself
- `src/ledger.ts` — append-only decision ledger
- `src/manualCalculator.ts` — controllable compute for deterministic tests
- `test/cache.test.ts` — scenario tests (late old task, input overwrite,
  caller mutation, quota reclamation, cancellation, coalescing)
- `src/demo.ts` — scripted scenario printing the hit ledger

## Commands

```sh
npm test    # build + run the scenario tests
npm run demo  # build + print a verifiable hit ledger
```
