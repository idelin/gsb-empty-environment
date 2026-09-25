# tri-value-policy-algebra

Boolean + integer-interval algebra simplification for attribute policies, with
**Kleene three-valued semantics** (`true` / `false` / `unknown`), **source
mapping** (provenance) and an **exhaustive finite-domain equivalence tester**.

Pure TypeScript, zero dependencies. Runs directly on Node.js >= 22.6 via
built-in type stripping (developed on Node 23.9); tests use the built-in
`node:test` runner.

## Semantics

Attributes are explicitly declared finite integer domains and may be
**required** (always present) or **optional** (may be missing). A condition on
a missing attribute evaluates to `unknown`; nothing is coerced and no external
identity/attribute service is consulted — evaluation reads only the declared
attributes from the provided context object.

Sound simplification laws (applied):

| Law | Rule |
| --- | --- |
| domination | `false AND x -> false`, `true OR x -> true` |
| identity | `true AND x -> x`, `false OR x -> x` |
| involution | `NOT (NOT x) -> x` |
| idempotence | `x AND x -> x`, `x OR x -> x` |
| interval algebra | conditions on the same attribute merge by intersection (AND) / union (OR); `NOT cond` complements intervals |

Deliberately **not** applied (unsound when attributes may be missing):

| Forbidden law | Counterexample |
| --- | --- |
| `x OR NOT x -> true` | `x` missing: `unknown OR NOT unknown = unknown` |
| `x AND NOT x -> false` | `x` missing: `unknown AND NOT unknown = unknown` |

Consequently, an empty interval intersection collapses to the constant `false`
(and a domain-covering union to `true`) **only for required attributes**; for
optional attributes it stays a condition that is `false` when present and
`unknown` when missing.

Every output node carries `from`: the ids of the original AST nodes that
materially produced it. Branches eliminated by domination (e.g. the `x` in
`false AND x`) are never evaluated, so their ids leave the source map.

## Layout

- `src/interval.ts` — integer interval algebra (normalize/intersect/union/complement)
- `src/ast.ts` — expression AST, builders, node ids, pretty printer
- `src/eval.ts` — Kleene three-valued evaluation
- `src/simplify.ts` — semantics-preserving simplifier with source mapping
- `src/policy.ts` — explicit-attribute policy declaration, validation, evaluation
- `src/equiv.ts` — exhaustive finite-domain equivalence tester
- `src/sample.ts` — runnable demo (`npm run sample`)
- `test/` — `node:test` suites (`npm test`)

## Run

```sh
npm run sample   # node src/sample.ts
npm test         # node --test test/*.test.ts
```

## Sample output

```text
=== 1. Original expression ===
(age IN [18, +∞] AND age IN [-∞, 65] AND dept IN {101, 102}) OR (level IN [4, +∞] AND level IN [-∞, 5]) OR (FALSE AND age IN [101, +∞])

=== 2. Simplified expression (three-valued semantics preserved) ===
(age IN [18, 65] AND dept IN [101, 102]) OR level IN [4, 5]
source map:
  #11 OR  <=  sources [1, 2, 3, 4, 5, 6, 7, 8, 10, 11]
    #4 AND  <=  sources [1, 2, 3, 4]
      #15 age IN [18, 65]  <=  sources [1, 2]
      #16 dept IN [101, 102]  <=  sources [3]
    #22 level IN [4, 5]  <=  sources [5, 6, 7]

=== 3. Equivalence over the full finite domain ===
checked 7986 inputs, equivalent: true

=== 4. Excluded middle is NOT folded when the attribute may be missing ===
original:   level IN [3, +∞] OR (NOT level IN [3, +∞])
simplified: level IN [-∞, +∞]   (still not TRUE)
level missing -> unknown
naive fold to TRUE equivalent: false, first mismatch: env {level: missing} -> original unknown vs folded true
required attribute age: age IN [18, +∞] OR (NOT age IN [18, +∞]) -> TRUE

=== 5. Policy evaluation reads only declared attributes ===
context {"age":30,"dept":102,"level":2,"ssn":"should-never-be-read","role":"external-service-field"} -> true
level missing -> true
age 70, level 5 -> true
```

Note in section 2: source id `9` (`age IN [101, +∞]`) is absent from the
source map — that branch was eliminated by `FALSE AND ...` without being read.

## Test results

`npm test` (Node 23.9, `node:test`):

```text
ℹ tests 35
ℹ suites 0
ℹ pass 35
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
```

Coverage highlights:

- Kleene truth tables for AND/OR/NOT, missing attribute -> `unknown`
- forbidden folds stay unfolded on nullable domains, fold correctly on required ones
- `FALSE AND x` branch elimination + provenance of dropped branches
- interval merge / complement / contradiction / tautology detection
- exhaustive equivalence: simplified == original on all 7986 finite inputs;
  the tester flags `x OR NOT x -> TRUE` as inequivalent with a concrete
  counterexample (`level` missing)
- policy evaluation touches only declared attributes (verified with a Proxy)
