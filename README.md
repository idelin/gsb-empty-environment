# Decision Table Analyzer

Analyzes priority-ordered decision tables whose conditions mix **ranges**,
**enum sets** and **negations**. For every rule it finds a *hit witness* —
a minimal input on which the rule actually wins (highest priority wins) —
or proves the rule can never win and explains why. Field domains are finite
but the combined input space can be huge; the analyzer never enumerates it.

## What it reports

For each rule, exactly one of:

- **WINS** — the rule strictly wins somewhere. Emits the *minimal witness*:
  the lexicographically smallest input (fields in declared order, values in
  domain order) that hits this rule and no rule of equal-or-higher priority.
- **TIES** — the rule reaches top priority but never alone: some same-priority
  rule always co-fires. Emits a minimal top-priority input and the partners.
- **COVERED** — the rule can never win: every input it matches is captured by
  higher-priority rules. Emits a *coverage explanation*: a greedy set cover
  naming the higher-priority rules and the exact cells each one covers.
- **VACUOUS** — the rule's own conditions are unsatisfiable.

Additionally, every pair of same-priority rules that can fire together is
reported as a **conflict** with an input that hits both. A conflict is marked
`masked` when every co-hit input is shadowed by a higher-priority rule.

## Usage

Requires Node.js >= 23.6 (runs TypeScript natively), or Node >= 22.6 with
`--experimental-strip-types`.

```sh
node src/cli.ts examples/table.json        # human-readable report
node src/cli.ts --json examples/table.json # machine-readable report
node --test                                # run the test suite
```

## Table format

```json
{
  "fields": [
    { "name": "age", "type": "int", "min": 0, "max": 120 },
    { "name": "tier", "type": "enum", "values": ["free", "pro", "enterprise"] }
  ],
  "rules": [
    { "name": "vip", "priority": 100,
      "when": { "tier": "enterprise", "income": { "range": [500000, null] } } }
  ]
}
```

Conditions per field (omit a field for "any"):

| JSON                            | Meaning                        |
| ------------------------------- | ------------------------------ |
| `"pro"` / `42` / `["a", "b"]`   | shorthand for `in`             |
| `{ "in": [...] }`               | value in set                   |
| `{ "notIn": [...] }`            | value not in set (negation)    |
| `{ "range": [lo, hi] }`         | inclusive range; `null` = open |
| `{ "notRange": [lo, hi] }`      | outside the range (negation)   |
| `{ "any": true }`               | unconstrained                  |

Higher `priority` wins; equal priorities are potential conflicts.

## How it works

- Every condition compiles to a `ValSet`: sorted disjoint intervals over the
  field's domain index space (`src/valset.ts`, `src/model.ts`). A rule is a
  *box* — one `ValSet` per field.
- **Witness search** (`src/solver.ts` `findAvoiding`): to escape a competing
  rule it suffices to dodge it on one field, so the solver backtracks over
  (competitor, field) pairs, shrinking the allowed region — complete, and
  independent of the input-space size.
- **Minimal witness** (`minimalWitness`): per field, binary-search the
  smallest value that keeps the instance feasible — `O(log domain)`
  feasibility checks per field, never a domain scan.
- **Coverage proof** (`explainCover`): greedy set cover over boxes with exact
  box subtraction, producing the covering rules plus the cells each covers.
- **Conflicts** (`src/analyze.ts`): pairwise box intersection of
  equal-priority rules; the witness is the minimal co-hit input, preferring
  one not shadowed by higher-priority rules.

## Layout

- `src/valset.ts` — interval-set algebra
- `src/model.ts` — table schema, validation, condition compilation
- `src/solver.ts` — box algebra, witness search, cover explanation
- `src/analyze.ts` — per-row analysis and conflict detection
- `src/render.ts` / `src/cli.ts` — reporting and CLI
- `examples/table.json` — demo table exercising every verdict
- `test/analyzer.test.ts` — `node:test` suite
