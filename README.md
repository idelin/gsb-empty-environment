# exact-json-decimal

Zero-dependency TypeScript library for JSON with **exact decimal numbers**:

- JSON numbers parse into an exact decimal structure and keep their **original
  lexeme** (`100`, `1e2`, `1e+02`, `100.0` stay distinguishable).
- Strict grammar: illegal leading zeroes (`01`, `-00`), truncated forms
  (`1.`, `1e`) and non-finite literals (`Infinity`, `NaN`) are rejected.
- Serialization is either **canonical** (unique per numeric value, idempotent)
  or **original** (re-emits the source lexeme; preserves `-0`).
- An **incremental parser** accepts arbitrary chunks (even mid-number,
  mid-`\u`-escape, or inside a multi-byte UTF-8 byte) and never publishes a
  number before the token is terminated.
- Numeric comparison works without expanding powers of ten, so exponents far
  beyond `Number` range are handled safely inside a configurable precision
  budget.
- A small, hand-written **JSON Schema subset** validator reports each error
  with both an instance path and a rule (schema) path. No validation library
  is used.

## Requirements

Node.js with native TypeScript execution (tested with **v23.9.0**; v22.18+
works too). No npm install, no build step:

```bash
npm test     # node --test test/*.test.ts
npm run demo # node examples/demo.ts
```

## Exact decimal model

```
value = (negative ? -1 : 1) * integer(digits) * 10 ** exponent
```

- `digits: string` — significant digits, leading/trailing zeroes stripped;
  `"0"` for zero.
- `exponent: bigint` — exact, even for `1e123456789012345678901234567890`.
- `raw: string` — the original lexeme.

**Comparison** uses signs, adjusted exponents (`exponent + digits.length`)
and a lexicographic digit compare only — no digit expansion, no floating
point — so huge powers never materialize.

**Canonical form**: zero is `0` (negative zero normalizes away); plain decimal
notation for adjusted exponents in `(-6, 21]`; otherwise normalized scientific
notation (`1.25e-7`, `1.23…e38`). It is a fixpoint:
`canonical(parse(canonical(x))) === canonical(x)`. Example: `1e+02 → 100`,
and `100 → 100`.

**Precision budget** (`maxSignificantDigits = 1000`, `|exponent| ≤ 100000` by
default) bounds both mantissa size and written exponent magnitude; violation
raises `DecimalError`. Signatures are the canonical spelling; equal values
share a signature while distinct `raw` lexemes remain observable.

**Negative zero**: `-0 === 0` numerically; canonical serialization emits `0`;
original serialization emits `-0`; the sign bit is retained on the value.

## Incremental parsing

```ts
const parser = new JsonParser();
parser.feed('{"x":1e');        // number not emitted yet
parser.feed('+02}');           // number 1e+02 confirmed at '}'
for (const event of parser.end()) { /* startObject, key, value, endObject */ }
```

Events: `startObject/endObject/startArray/endArray/key/value`. A number event
fires only after its terminator (whitespace, `,`, `]`, `}` or `end()`).
`feed` accepts `string` or `Uint8Array` (streaming UTF-8); the DOM helper
`parseJson(text)` returns numbers as `Decimal`.

## Schema subset (only this is promised)

`type` (incl. arrays and `integer`), `enum`, `const`, `properties`,
`required`, `additionalProperties`, `items`, `minItems`/`maxItems`,
`minLength`/`maxLength`, `pattern`, and `minimum`/`maximum`/
`exclusiveMinimum`/`exclusiveMaximum` with exact decimal comparison.
Unsupported keywords are returned in `warnings` and ignored — never enforced.

Numeric bounds beyond `2**53` must be supplied as strings (or `Decimal`),
since a JS number literal like `9007199254740993` is already rounded by JS.

Every error is `{ keyword, instancePath, schemaPath, message }`, e.g.
`instancePath: "/id"`, `schemaPath: "/properties/id/minimum"`.

## Layout

- `src/decimal.ts` — exact decimal, grammar, budget, comparison, canonical form
- `src/parser.ts` — incremental state-machine parser + DOM builder
- `src/serializer.ts` — canonical / original serialization
- `src/schema.ts` — hand-written schema subset validator
- `test/*.test.ts` — `node:test` suite (exhaustive grammar sweeps, all split
  points + random chunk splits, byte-wise UTF-8, exponent boundaries)
- `examples/demo.ts` — runnable walkthrough

## Sample run

`npm run demo` output:

```text
=== 1. Exact decimals: raw lexeme + exact value ===
  id       raw=9007199254740993   canonical=9007199254740993
  balance  raw=-0                 canonical=0
  rate     raw=1e+02              canonical=100
  9007199254740993 > 9007199254740992 ? true

=== 2. Serialization: original vs canonical ===
  original : {"id":9007199254740993,"balance":-0,"rate":1e+02,"tags":["a","b"]}
  canonical: {"id":9007199254740993,"balance":0,"rate":100,"tags":["a","b"]}

=== 3. Incremental parsing: feed arbitrary chunks ===
  feed("{\"big\":900719") -> 2 event(s)
  feed("9254740993,\"x\":1e") -> 2 event(s)
  feed("+02}") -> 2 event(s)
  feed("") -> 0 event(s)

=== 4. Schema subset validation with dual paths ===
  valid: false
  instance=/id rule=/properties/id/minimum :: 9007199254740992 must be >= 9007199254740993
  instance=/rate rule=/properties/rate/exclusiveMaximum :: 100 must be < 100

=== 5. Equal values, different lexemes ===
  raw=100      signature=100 (signatures identical, lexemes distinct)
  raw=1e2      signature=100 (signatures identical, lexemes distinct)
  raw=1e+02    signature=100 (signatures identical, lexemes distinct)
  raw=100.0    signature=100 (signatures identical, lexemes distinct)
```

## Test results

```text
ℹ tests 37
ℹ suites 0
ℹ pass 37
ℹ fail 0
```

Coverage highlights: exhaustive 8-character-alphabet lexemes up to length 4
plus reduced alphabet length 5 (≈7.8k strings) checked against both the JSON
grammar and `JSON.parse`; all ~90 two-way chunk split points plus 200 seeded
random multi-splits and byte-wise UTF-8 feeds; exponent boundary sweep
(`1e±100000` accepted, `1e±100001` rejected); `1e123456789012345678901234567890`
comparison without expansion; `-0` policy; `1e+02` canonical fixpoint.
