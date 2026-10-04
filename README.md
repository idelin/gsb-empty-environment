# cache-key-auditor

Audits a build's cache key against the inputs the build *actually* uses:
environment variables, tool versions, and conditionally read files.
It searches for **counterexamples** — pairs of inputs that change the output
without changing the cache key — and returns them in **minimal** form,
split into **artifact-affecting** (cache-unsound) and **log-only** (harmless)
differences.

## Model

A build is described by a `BuildSpec` (see `src/model.ts`):

- `domain` — every input mapped to its finite, enumerable set of values
  (env vars, tool versions, config files that may exist or not).
- `keyInputs` — the inputs hashed into the cache key.
- `build(env)` — a pure function producing `{ artifact, log }`. It must read
  inputs via property access on `env` (`env["tool:node"]`, `"file:x" in env`)
  so the auditor can trace actual and conditional reads through a `Proxy`.

## How it works

1. **Enumerate** the cartesian product of the domain (bounded by
   `--max-environments`, default 200k) and run the build on each environment,
   tracing reads.
2. **Group by key** and pair up runs whose outputs differ under the same key.
3. **Minimize** each pair greedily (coordinate alignment) until it is
   1-minimal: aligning any remaining differing input destroys the
   counterexample. Pairs are direction-canonicalized and deduplicated.
4. **Classify**: artifact differs → `artifact-affecting`; only the log
   differs → `log-only`.
5. **Sensitivity analysis** per input (varying it while holding everything
   else fixed) yields the effect table plus derived findings:
   - *missing from key*: read and output-affecting, but not hashed;
   - *declared but ineffective*: hashed, never changes anything;
   - *never read*: dead configuration surface.

The counterexample list is representative, not exhaustive: one minimal pair
per (difference class, differing inputs, key). The per-input effect table is
exhaustive over the domain.

## Usage

```sh
npm run build                                  # compile (offline tsc in node_modules)
node dist/src/cli.js dist/examples/demo.build.js        # text report
node dist/src/cli.js dist/examples/demo.build.js --json # machine-readable
npm test                                       # compile + run tests
```

Exit code is `1` when artifact-affecting counterexamples exist (CI-friendly),
`0` otherwise, `2` on usage errors.

Write your own spec by exporting `spec: BuildSpec` from a module; see
`examples/demo.build.ts`.
