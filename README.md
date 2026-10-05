# macroexp

A text macro expander in Go with parameters, nested calls, hygienic local
variables, and full per-token provenance.

## Syntax

```
#def NAME(a, b) body to end of line      # function-like macro
#def NAME body                           # object-like macro (zero params)
#def NAME(a, b)                          # multi-line body, terminated by #end
body lines
#end
```

Inside a macro body, `#local x` declares a hygienic local: every expansion
renames it to a fresh `x#N`, so locals never capture identifiers from
arguments or enclosing expansions. `#local x` at the declaration site expands
to the fresh name; other occurrences of `x` in the body are renamed too.

Calls look like `NAME(arg1, arg2)`; object-like macros are called bare
(`NAME` or `NAME()`). Unknown names pass through unchanged.

## Semantics

- **Arguments expand exactly once.** Each argument is pre-expanded a single
  time per call (memoized across repeated parameter references) and then
  spliced verbatim; the body rescan never re-expands argument text.
- **Nested calls** work both in bodies and in arguments. Cycle detection
  follows blue-paint semantics: `D(D(x))` is fine, while recursion through a
  macro's own replacement list (`#def A(x) A(x)`, or mutual `A -> B -> A`,
  including cycles through arguments) is reported.
- **Recursion cycles and budget exhaustion** keep the shortest call evidence
  in the error's `Chain`, and the unexpanded call text is preserved in the
  output.

## Provenance

Every output token carries:

- `Def` — where its text was defined (macro body or source position),
- `Call` — the call site of the innermost macro that emitted it,
- `Chain` — the full expansion chain of `Frame{Macro, Def, Call}`,
  outermost first.

Errors carry a `Pos` pointing into the original source — including positions
inside argument text at the call site — plus the relevant expansion chain.

## Usage

```go
exp := macroexp.New(macroexp.Options{Budget: 10000})
res, errs := exp.Expand("file.txt", src)
// res.Text is the expanded text; res.Tokens carries provenance.
```

CLI: `go run ./cmd/macroexp [-budget N] [file]`

## Tests

`go test ./...` covers same-name locals and capture avoidance, empty
arguments, repeated references (expand-once via step counting), provenance
chains, cycle evidence, budget exhaustion, and errors located inside
argument text.
