# multi-dim-quota

Multi-dimensional quota service in TypeScript. Runs on Node.js >= 23.6 (native
type stripping, no build step) and is verified with the built-in `node:test`
runner.

## Guarantees

- **Atomic multi-dimension deduction** — every requested dimension is checked
  first; only when *all* fit is anything debited. If user quota suffices but
  resource quota does not, neither is deducted (`InsufficientQuotaError`).
- **Idempotent operation ids** — replaying the same `operationId` with the
  same requirements returns the original permit and never deducts twice;
  replaying it with different requirements raises `OperationConflictError`.
- **Full rollback** — `cancel(id)` / `fail(id)` release every dimension of the
  permit; closing an unknown or already-closed permit raises
  `UnknownOperationError`.
- **Versioned, read-only policies** — `setPolicy` requires a strictly
  increasing version; permits citing an older version are rejected with
  `StalePolicyError`. Policies are validated and deep-frozen at definition.
- **Restricted policy language** — finite-set membership (`in`), integer
  comparisons (`eq|ne|lt|lte|gt|gte`), boolean combinators
  (`and|or|not`). Policies read only the explicit attribute record passed to
  `acquire`; there is no external identity service.

## Layout

- `src/policy.ts` — policy definition, validation, deep-freeze, evaluation
- `src/ledger.ts` — all-or-nothing multi-dimension ledger
- `src/service.ts` — idempotent permits, rollback, version gating
- `src/errors.ts` — typed error hierarchy
- `test/quota.test.ts` — `node:test` suite (19 tests)
- `examples/demo.ts` — runnable end-to-end sample

## Run

```sh
npm test     # node --test "test/**/*.test.ts"
npm run demo # node examples/demo.ts
```
