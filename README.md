# Admission Pipeline Orchestrator

An embeddable, dependency-free TypeScript component that runs an ordered chain
of Kubernetes-style mutating admission webhooks over a JSON resource object,
with `reinvocationPolicy`-driven re-invocation passes, atomic RFC 6902 JSON
Patch application, and deterministic oscillation (state-cycle) detection.

## Build & test

The library is plain ESM with no runtime dependencies. Build declarations and
JavaScript into `dist/`, then run the suite:

```bash
npm run build      # tsc -> dist/
npm test           # tsc, then node --test dist/test/*.test.js
```

## Usage

```ts
import { runAdmissionPipeline } from './dist/index.js';

const result = runAdmissionPipeline({
  object: { metadata: { name: 'demo', labels: { app: 'demo' } }, spec: {} },
  namespaceLabels: { env: 'prod' },
  maxReinvocationRounds: 2,
  webhooks: [
    {
      name: 'set-defaults',
      reinvocationPolicy: 'IfNeeded',
      failurePolicy: 'Fail',
      objectSelector: { matchLabels: { app: 'demo' } },
      handler: () => ({
        allowed: true,
        patch: [{ op: 'add', path: '/spec/replicas', value: 1 }],
      }),
    },
  ],
});

result.object; // final, converged resource object
result.trace;  // exact, ordered invocation/skip trace
```

Each handler receives a defensive copy of the live object plus a context
(`webhookName`, zero-based `round`, zero-based `invocationCount`) and returns
`{ allowed: true, patch?: JsonPatchOperation[] }` or
`{ allowed: false, statusMessage?: string }`.

## Execution model

- **Pass 0** invokes every webhook whose `objectSelector` and
  `namespaceSelector` match the live object. Selectors are always re-evaluated
  against the current object, so a mutation inside a pass can make a later
  webhook newly match (or stop matching).
- **Re-invocation passes** are admitted only when the preceding pass produced a
  semantic (canonical-state) mutation. A live `changed` flag — set by the most
  recently invoked mutating webhook and propagated within and between passes —
  gates each `IfNeeded` webhook. `Never` webhooks run at most once. First-time
  selector matches may enter on any pass.
- **Convergence** happens on a pass with no mutation, or when no remaining
  webhook could execute. `maxReinvocationRounds` bounds the number of
  re-invocation passes (total passes = `maxReinvocationRounds + 1`).
- **Atomic patches** run on a defensive clone. Any failing operation (including
  the RFC 6902 `test` op) discards the whole batch and leaves the object
  untouched; the outcome then follows `failurePolicy`: `Fail` aborts with
  `AdmissionFailureError`, while `Ignore` records the failure on the trace and
  continues.
- **Oscillation** raises `OscillationConflictError` in two cases:
  - `state-cycle`: the object's canonical form returns to a previously observed
    full state. The error reports the ordered participating webhook sequence
    and the union of conflict JSON Pointer paths.
  - `round-limit`: mutations are still pending when the reinvocation budget is
    exhausted, reporting the last pass's mutating webhooks and their paths.

## Trace

Every evaluation appends a `TraceEntry` with `round`, registration `index`,
`webhookName`, `action` (`invoke`, `skip-selector-miss`,
`skip-never-reinvoke`, `skip-no-trigger`), `outcome` (for example `allowed`,
`no-change`, `denied-ignored`, `patch-failed-fail`), the touched JSON Pointer
paths, and an optional `detail` message.

## Layout

- `src/pipeline.ts` — orchestration loop, pass gating, oscillation detection.
- `src/json-pointer.ts` — RFC 6901 pointers, canonical JSON, atomic RFC 6902.
- `src/selector.ts` — metav1.LabelSelector-compatible matching.
- `src/errors.ts` — `AdmissionFailureError`, `OscillationConflictError`.
- `test/` — multi-webhook interaction fixtures and unit tests.
