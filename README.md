# exact-decimal-schema

Exact decimal (string-based) number validation, constraint intersection, and
schema-subset containment in TypeScript. **No binary floating point anywhere**:
parsing, normalization, comparison, and multiple-of checks are pure
string/BigInt operations. Runs on Node.js ≥ 23.6 (native type stripping), no
dependencies, no third-party validators.

```bash
npm test     # node --test test/*.test.ts
npm run demo # node examples/demo.ts
```

## Decimal core (`src/decimal.ts`)

A `Decimal` is `(-1)^neg * coeff * 10^exp` with a normalized BigInt
coefficient (no leading/trailing zeros) plus the preserved raw literal.

- `parseDecimal("1.2300e2")` → canonical `"123"`; scientific notation is
  normalized by definition, never through a float.
- `isMultipleOf(parseDecimal("0.3"), parseDecimal("0.1"))` → `true`
  (IEEE754 says `0.3 / 0.1 = 2.9999999999999996`).
- `precisionOf(parseDecimal("1e3"))` → `4`: digit counts follow the numeric
  value (`1000`), not the 3-character literal. Precision is SQL-style total
  digits (integer + fractional digits of the normalized value).
- Negative zero equals positive zero (`compareDecimal("-0", "0") === 0`) but
  the original text is kept in `.raw`.
- **Budget control**: `1e999999999999999999` throws `ExponentBudgetError`
  *before* any exponent-length zero string or power-of-ten is allocated
  (default budget 100 000, configurable via `{ maxExponent }`). Comparisons
  align digit strings instead of materializing powers of ten.

## Constraint intersection (`src/solver.ts`)

`solve(constraints)` intersects an exact decimal range (inclusive/exclusive
bounds), `multipleOf`, `precision` (total digits) and `scale` (decimal
places) in a scaled-integer domain: on grid `u = 10^-g` every admissible
value is `j·u`; `multipleOf` becomes `j ≡ 0 (mod q)`; precision becomes a
finite union of digit-length regions. The result is either

- `{ feasible: true, witness }` — the minimal-|value| witness, re-verified
  against all constraints before returning, or
- `{ feasible: false, evidence }` — precise no-solution evidence naming the
  intersected integer interval, the lattice modulus, and the nearest
  straddling multiples.

## Instance validation (`src/validate.ts`)

`validate(instance, schema)` implements **only** this subset — anything else
is a `SchemaError`, never silently ignored:

`type` (`number|integer|string|boolean|object|array|null`), `properties`,
`required`, `items`, `minItems`, `maxItems`, `minLength`, `maxLength`,
`enum`, `const`, plus the number keywords `minimum`, `maximum`,
`exclusiveMinimum`, `exclusiveMaximum` (numeric, draft 2019-09 style),
`multipleOf`, `precision`, `scale`.

Numbers may be JSON numbers or decimal strings; both are parsed exactly.
`integer` accepts `1e3` and `2.0` (numeric value, not text). Every error
carries both locations:

```
instancePath=/price  schemaPath=#/properties/price/multipleOf
```

## Contract containment (`src/containment.ts`)

`contains(A, B)` decides whether every value valid under B is valid under A,
on the same exact lattice model:

- range modes are decided exactly by the solver;
- `multipleOf` containment is decided by lattice divisibility
  (`v = k·L_B` is a multiple of `m_A` iff `k ≡ 0 (mod T)`);
- precision/scale modes use exact bounds on achievable digit counts.

On failure it returns a **minimal counterexample** (smallest `|value|`),
verified to satisfy B and violate A. An infeasible B is vacuously contained.
Pathological precision×lattice combinations may return `"undetermined"`
(bounded search budget) rather than a wrong answer.

## Reference verification (`test/reference.test.ts`)

An independent finite scaled-integer model (scale both operands to a common
exponent, compare plain BigInts) cross-checks `compareDecimal`,
`isMultipleOf`, and `precisionOf` over 1 500 generated cases, re-verifies
300 random solver witnesses, and confirms no-solution evidence by bounded
brute-force enumeration.

## Sample output

```
=== 0.3 is an integer multiple of 0.1 (exact, no binary float) ===
isMultipleOf(0.3, 0.1) = true
for contrast, IEEE754: 0.3 / 0.1 = 2.9999999999999996

=== constraint intersection: precise no-solution evidence ===
[0.15,0.25] ∩ 1ℤ -> empty integer interval: j in [1, 0] (value = j * 10^-0) derived from range [0.15, 0.25]

=== contract containment with minimal counterexample ===
contained = false | counterexample = 0.01

=== huge exponents are budget-rejected before allocation ===
ExponentBudgetError: decimal exponent 999999999999999999 exceeds budget 100000; rejected before allocating any exponent-length digit run
```

## Test results

```
ℹ tests 40
ℹ pass 40
ℹ fail 0
```
