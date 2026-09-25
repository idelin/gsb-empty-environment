# normalization-rules-engine

Declarative normalization of JSON-compatible trees with an explicitly
ordered rule set, per-field change records, static dependency analysis,
finite-domain idempotency search, and versioned rule publication with
incremental recompute.

Pure TypeScript (erasable syntax only), zero dependencies. Runs directly
on Node.js >= 23.6 via native type stripping; tests use the built-in
`node:test` runner.

## Commands

```sh
npm test          # node --test test/*.test.ts
npm run sample    # end-to-end demo: samples/run.ts
```

## Model

- Input is a JSON-compatible tree. Object key order is insignificant
  (traversal sorts keys, so output is deterministic); array order is
  significant and preserved.
- Every field is identified by its exact path (`/events/0/when`); all
  changes and errors carry that identity.
- Rules apply in array order — the order is the rule set, nothing is
  implicit.

### Rule kinds (`src/rules.ts`)

| kind         | behavior |
|--------------|----------|
| `whitespace` | `trim` / `collapse` string values. `null` and non-strings are never touched; `""` is not `null`. |
| `date`       | Parses only the explicitly declared formats (`YYYY`/`MM`/`DD` tokens), validates the real calendar (leap years), emits ISO `YYYY-MM-DD`. Unparseable/invalid strings are path-located errors; the original value is kept. No year/month/day order is ever guessed. |
| `enum-alias` | Maps declared alias strings to canonical values. |
| `copy`       | Copies a value between concrete paths; gives the dependency graph cross-field edges. |

### Engine (`src/normalize.ts`)

`applyRules(input, rules)` returns `{ output, changes, errors, inverse }`:

- `changes`: per-field records `{ seq, ruleId, path, field, before, after }`
  (`before` may be the `ABSENT` sentinel for created fields).
- `errors`: `{ ruleId, path, field, message, value }` — the offending
  value stays in the output untouched.
- `inverse`: patch ops (`replace`/`add`/`remove`) that restore the input
  exactly when applied to `output` via `applyPatchOps`.
- Normalizing twice never changes anything further (verified by tests
  and by the search below).

### Static analysis (`src/deps.ts`)

`analyzeRules(rules)` computes read/write path dependency edges
(`writes(A) ∩ reads(B) ≠ ∅ ⇒ A → B`) and reports:

- `alias-conflict` — two enum rules map the same alias to different
  canonicals on overlapping write paths.
- `trigger-cycle` — a strongly connected component of the dependency
  graph involving at least one data-relocating rule (e.g. `copy a→b`
  with `copy b→a`).
- `non-idempotent-combo` — alias chains (`a→b`, `b→c`) inside one rule
  or across overlapping rules, and whitespace-rule-before-enum-rule
  orderings whose canonicals are not whitespace-normal.

`dependencyClosure(rules, changedIds)` yields the changed rules plus
everything transitively reading their writes.

### Idempotency search (`src/idempotency.ts`)

`searchIdempotency(rules, shape, budget)` derives a finite candidate
domain per concrete target path (valid/invalid dates, aliases,
canonicals, `""`, whitespace-only strings, `null`, non-strings…) and
enumerates the product, checking `normalize²(x) = normalize(x)`:

- `proven` — the entire product was enumerated.
- `refuted` — returns a concrete witness `{ input, once, twice, path }`.
- `unknown` — the budget ran out first. **Never reported as proven.**

### Versioned publication (`src/registry.ts`)

`RuleRegistry` keeps published rule-set versions:

- `publish(rules)` validates first; an invalid set is rejected and the
  previous version stays active — a bad publish cannot corrupt results
  produced by the last good version.
- `updateRule(id, rule)` validates and publishes a new version and
  returns the affected rule set (dependency closure over the union of
  old and new write paths).
- `recompute(prevResult, affectedRuleIds)` reverts only the affected
  rules' old effects via their inverse patches and re-runs only those
  rules in pipeline order; all other fields keep their values and
  change records. Result equals a full recompute (tested).

## Sample output (excerpt)

```
=== 1. Normalize with per-field change records ===
  #0 [ws-name] /user/name: "  Ada   Lovelace " -> "Ada Lovelace"
  #3 [date-when] /events/0/when: "03/04/2025" -> "2025-03-04"
  #4 [enum-status] /status: "na" -> "N/A"
=== 2. Errors keep original values and locate the exact path ===
  [date-when] /events/1/when: invalid month 13 in "2025-13-01" (kept "2025-13-01")
=== 5. Static dependency analysis of a problematic rule set ===
  [trigger-cycle] (c1, c2) mutual trigger cycle among rules: c1, c2
  [alias-conflict] (e1, e2) alias "na" maps to "N/A" in "e1" but to "Unknown" in "e2" ...
=== 6. Idempotency counterexample search over finite domains ===
  good rules:  status=proven checked=2401/2401
  chain rule:  status=refuted witness: input={"status":"a"} once={"status":"b"} twice={"status":"c"} at /status
  tiny budget: status=unknown checked=10/2401 (never "proven")
=== 8. Local rule update recomputes only dependent fields ===
  incremental === full recompute: true
```

## Test results

`npm test` — 32 tests, all passing:

```
ℹ tests 32
ℹ pass 32
ℹ fail 0
```

Coverage: whitespace/null semantics, declared-format date parsing (no
guessing, leap years), error paths inside arrays, change-record
identity, inverse-patch restoration, double-normalization stability,
object-key-order invariance, alias conflicts, trigger cycles,
non-idempotent combos, proven/refuted/unknown search outcomes, invalid
publication safety, and incremental recompute through dependency chains.
