# muttest

Mutation testing tool for a tiny integer language with addition,
subtraction, comparisons and conditional branches. All values are
**signed 8-bit** integers; arithmetic overflow **wraps around**
(two's complement), e.g. `127 + 1 == -128`.

Mutants are single-point: exactly one AST node is changed per mutant.
Because the input domain is finite (each input variable ranges over a
configurable interval, default the full `[-128, 127]`), the tool
classifies every mutant **exactly** by exhaustive enumeration:

- **Killed** — some test in the suite makes original and mutant
  disagree (the killing test and both outputs are shown).
- **Equivalent** — original and mutant produce identical output on
  *every* input in the domain.
- **Survived** — not killed and not equivalent; a concrete
  **counterexample** input inside the domain (missed by the test
  suite) is reported, together with both outputs.

## The language

```
input a, b          # declare inputs (comma separated)
d = a - b           # assignment
if d < 0            # condition: any expression, nonzero is true
  r = 0 - d
else                # optional
  r = d
end                 # closes the if
return r            # return value (falling off the end returns 0)
```

- Operators: `+`, `-` (wrapping int8), unary `-`,
  comparisons `< <= > >= == !=` (evaluate to `1`/`0`).
- Literals: `-128..127` (`-128` only via unary minus, e.g. `x + -128`).
- `#` starts a comment. Statements are newline-separated.

## Mutation operators

- Arithmetic swap: `+` ↔ `-`
- Comparison replacement: each of `< <= > >= == !=` → the five others
- Constant adjustment: `c` → `c+1`, `c-1` (wrapping), and `0`
- Variable replacement: `v` → every other variable in the program

## Usage

```
go test ./...
go run . -prog examples/absdiff.imp -tests examples/tests.txt
go run . -prog examples/absdiff.imp -domain -8..7   # smaller domain
```

Test suite format: one test per line, either named (`a=3, b=-4`) or
positional (`3, -4`, in `input` declaration order).

Exit code is `1` if any mutant survives (test suite insufficient),
`0` otherwise. The report groups mutants into killed / survived /
equivalent and prints a mutation score excluding equivalent mutants.
