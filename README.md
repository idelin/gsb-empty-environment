# numlit

Exact numeric literal parsing for Go — no floating point involved.

## Features

- Decimal integers (`42`, `1_000_000`), hexadecimal integers
  (`0x1F`, `0xDEAD_BEEF`), and decimals with optional fraction and
  exponent (`0.5`, `.5`, `1.25e-3`, `1E+10`).
- A `_` digit separator is accepted only strictly between two digits;
  leading, trailing, doubled, or punctuation-adjacent separators are
  syntax errors.
- Results are arbitrary-precision integers or reduced rationals
  (`num/den` in lowest terms). The sign is preserved as written, so
  `-0` equals `0` numerically but keeps `Sign() == -1`. Each `Value`
  records the source byte interval `[Start, End)`.
- A `Budget` caps significant mantissa digits and the effective decimal
  exponent. Limits are checked before any large allocation, so
  `1e999999999999` fails cheaply instead of allocating huge memory.

## Usage

```go
v, err := numlit.Parse("1_2.5e-1")      // 5/4, Kind() == numlit.Rat
num, den := v.Num(), v.Den()            // 5, 4

w, _ := numlit.Parse("0x64")            // 100, Kind() == numlit.Int
n, _ := w.Int()                         // *big.Int 100

v.Equal(w)                              // numeric comparison
v.Sign()                                // -1 or +1 as written

// Custom budget and prefix scanning:
v, err = numlit.ParseWith("1e5", numlit.Budget{MaxExponent: 3}) // ErrBudget
v, err = numlit.ParsePrefix("f(1.5)g", 2, numlit.DefaultBudget) // span [2,5)
```

Errors wrap `numlit.ErrSyntax` or `numlit.ErrBudget`; use `errors.Is`.

## Test

```sh
go test ./...
```
