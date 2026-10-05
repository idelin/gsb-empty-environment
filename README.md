# Exception Revocation Solver

Checks a base policy plus temporary allow/deny exceptions (evaluated
first-match by priority) and computes the **minimum-total-cost set of
exceptions to revoke** so that a required safety property is restored.
The result is verified by exhaustive enumeration of the whole finite input
domain, with a per-retained-exception verification summary and a
per-revoked-exception necessity certificate.

## Model

- **Scenario** (`src/model.ts`): finite attribute domains, a base policy
  (`defaultEffect` + prioritized `baseRules`), up to **24** temporary
  `exceptions` (each with a non-negative revocation `cost`), and one safety
  `property` (`when` / `mustBe`).
- **Evaluation**: base rules and exceptions share a single priority order
  (higher first, ties by id); the first matching rule decides, otherwise the
  policy default applies.

## Solving

- `solveFast` (`src/solver.ts`) — exact, polynomial. For a single property,
  every property-relevant request forces a conjunction of revocations: all
  unsafe exceptions ranked above the first safe entry (safe exception, safe
  base rule, or safe default) in its decision sequence. The union of these
  mandatory revocations is the unique minimal, hence cost-minimal, set.
  A request that hits an unsafe base rule or unsafe default before any safe
  entry is unfixable → the scenario is reported **infeasible**.
- `solveExact` — general branch-and-bound over revoke/keep assignments
  (sound for multiple properties). Used to cross-check `solveFast`
  (`--check`) and validated against brute force over all 2^n subsets in
  tests.

## Verification

`verifyPlan` (`src/verify.ts`) re-evaluates **every** request in the finite
domain against the post-revocation policy and reports:

- violations before/after revocation (after must be 0),
- per **retained** exception: how many requests it decides, how many are
  property-relevant, and that 0 of them violate the property,
- per **revoked** exception: a necessity certificate — a witness request
  that breaks the property if this exception alone were restored.

## Usage

```sh
npm run solve                          # built-in demo scenario
npm run solve -- examples/scenario.json --check
node --disable-warning=ExperimentalWarning src/cli.ts scenario.json --json
npm test                               # node:test suite, incl. brute-force cross-checks
```

Requires Node.js ≥ 23.6 (runs TypeScript via native type stripping; no
build step). `tsconfig.json` is provided for editor type-checking
(`erasableSyntaxOnly`).

## Scenario format

See `examples/scenario.json`:

```json
{
  "name": "demo",
  "domains": { "role": ["admin", "guest"], "resource": ["docs", "secrets"] },
  "policy": {
    "defaultEffect": "deny",
    "baseRules": [{ "id": "base-admin", "effect": "allow", "priority": 100, "when": { "role": ["admin"] } }],
    "exceptions": [{ "id": "ex-1", "effect": "allow", "priority": 90, "when": { "resource": ["secrets"] }, "cost": 4 }]
  },
  "property": { "when": { "resource": ["secrets"], "role": ["guest"] }, "mustBe": "deny", "description": "guests never reach secrets" }
}
```

Exit codes: `0` verified optimal plan, `1` verification failed, `2`
infeasible, `3` cross-check mismatch.
