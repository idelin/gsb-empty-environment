# numlit

Exact numeric literal parsing for Go — no floating-point detour.

## Grammar

```
number  := sign? ( hexint | decimal )
sign    := '+' | '-'
hexint  := '0' ('x'|'X') hexdigit ( '_'? hexdigit )*
decimal := intpart ( '.' fracpart? )? ( ('e'|'E') sign? exppart )?
```

- The separator `_` is legal **only between two digits** of the same digit
  run (`1_000`, `0xDEAD_BEEF`, `1.2e3_4` are valid; `_1`, `1_`, `1__2`,
  `0x_1`, `1_.5`, `1e_5` are rejected).
- At least one mantissa digit is required overall (`.5` and `5.` are valid;
  `.` alone is not).

## Representation

- Integer literals (`42`, `0x1F`) → `*big.Int` (`Value.Kind == Int`).
- Literals with a fraction and/or exponent (`1.5`, `1e3`) → `*big.Rat`,
  reduced to lowest terms (`Value.Kind == Rat`).
- The written sign is preserved in `Value.Neg`, **including negative zero**
  (`-0`, `-0.0`, `-0x0`).
- `Value.Span` records the byte range of the literal in the input.
- `Value.Rational()` exposes the exact value of either kind;
  `Value.Equal` compares numerically across spellings
  (`0x10` == `16` == `1.6e1` == `16.00`).

## Resource budget

`Parse(s, budget)` rejects hostile input before allocating:

- `Budget.MaxDigits` — significant mantissa digits (separators and leading
  zeros excluded).
- `Budget.MaxExponent` — absolute effective decimal exponent
  (explicit exponent minus fraction digits), i.e. the largest power of ten
  materialized.

A zero `Budget{}` selects `DefaultBudget` (4096 / 4096). Budget violations
return `*numlit.Error` with `Budget == true`; syntax errors carry the byte
offset in `Pos`.

## Test

```sh
GO111MODULE=on go test ./...
```
