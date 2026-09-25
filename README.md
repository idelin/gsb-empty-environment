# obligation-merge

Policy decision engine whose **allow verdicts carry obligations** — limits
(with dependency relations), masked fields, audit tags, ordered pipeline
steps and output-format choices — merged with a **dedicated algebra per
obligation kind**. Incompatible obligations produce an **explicit failure**
with a **subset-minimal unsatisfiable core**; nothing is silently dropped.

Pure TypeScript, zero dependencies. Runs on Node.js ≥ 22.18 (native type
stripping) with the built-in `node:test` runner.

## Run

```sh
nvm use            # or any Node >= 22.18
npm test           # node --test "test/*.test.ts"
npm run sample     # node samples/run-sample.ts
```

## Design

| Concern | Implementation |
| --- | --- |
| Explicit, read-only attributes | `src/expr.ts` — conditions limited to finite-set membership (`in`), integer comparisons (`intCmp`) and boolean combinators; unknown attributes throw; evaluation is pure (no I/O, no external identity service) |
| Limit algebra | `src/limits.ts` — limits + dependency relations compile to difference constraints `x_a - x_b <= c`; Bellman-Ford yields feasibility and the strictest (tightest) bound per limit, with a provenance chain of contributing obligations |
| Mask / audit-tag algebra | `src/merge.ts` — union, canonical (sorted) form so merging is commutative by construction; `isCommutative` verifies it for order-independent kinds |
| Ordered steps | `src/merge.ts` — order-sensitive topological merge; each obligation's internal order is preserved explicitly, cycles are conflicts |
| Output formats | `src/merge.ts` — intersection of allowed choices; mutually exclusive requirements are an explicit failure, never a silent pick |
| Minimal unsat core | `src/core.ts` — deletion-based subset-minimization; `isSubsetMinimalCore` verifies every proper subset is satisfiable |
| Finite-enumeration reference | `src/reference.ts` — brute-force enumeration over the finite candidate domain cross-checks the solver (feasibility, boundedness via independent BFS, exact projections); 300 random systems in `test/reference.test.ts` |
| Suppression & withdrawal | `src/policy.ts` — a matched rule may suppress obligation kinds from other rules; suppressed obligations are recorded (`suppressed`) and restored when the rule is withdrawn (`evaluate(..., { withdrawn })`) |
| Deny semantics | `src/policy.ts` — deny overrides allow and never executes allow obligations; they are listed under `notExecuted` for transparency |

## Decision shape

- `allow` — merged obligations with per-value provenance evidence.
- `deny` — deny rule ids plus the allow obligations that were not executed.
- `failure` — typed conflict (`limit` | `orderedSteps` | `outputFormat`)
  with a subset-minimal core of the responsible obligations.

## Layout

```
src/expr.ts         attribute expressions (finite sets, integer cmp, boolean ops)
src/obligations.ts  obligation model
src/limits.ts       difference-constraint limit solver with provenance
src/merge.ts        per-kind merge algebras + commutativity check
src/core.ts         subset-minimal unsatisfiable cores
src/reference.ts    finite-enumeration reference checker
src/policy.ts       rule evaluation, suppression/withdrawal, deny semantics
samples/run-sample.ts  end-to-end scenarios
test/               node:test suites (32 tests)
```
