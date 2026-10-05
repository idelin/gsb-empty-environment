# errmerge

Diagnostic merging for parallel / speculative parsers (Go).

When many branches fail at different input positions, `Merge` combines their
diagnostics into one report:

1. **Farthest failure position wins** — a diagnostic at a farther token
   index discards all nearer ones.
2. **Expected sets merge at a tie** — diagnostics at the same position union
   their expected-token sets (sorted, deduplicated).
3. **Shortest source chain per context** — for each distinct calling context
   (innermost rule), only the shortest rule call chain is kept.

Backtracking safety: positions in a `Diag` come only from *failure events*
(a token expected but not found), never from consumption bookkeeping. Nodes
are pure functions over positions (`func(p, pos) Result`), so a branch that
is abandoned after consuming input cannot leak its progress into the
farthest position; the next alternative restarts at the original position.

## Combinators

- `Tok(kind)` — match one token; records an expectation on failure.
- `Rule(name, n)` — named rule; tracks the call chain and enforces
  `Parser.MaxDepth` (depth-limit failures are fatal and never swallowed).
- `Seq(...)` / `Alt(...)` / `Many(n)` / `Opt(n)` / `Lazy(&n)` — sequencing,
  ordered choice with diagnostic merging, repetition, option, recursion.
- `Parser.Parse(n)` — runs at position 0, requires full input, adds an
  `<EOF>` expectation when a prefix parse leaves trailing tokens.

## Tests

`parser_test.go` defines an ambiguous-prefix grammar
(`let x = 1` vs `let x : int = 1`, recursive paren expressions) and verifies
backtracking, expected-set merging, EOF reporting, recursion depth limits,
and that abandoned branches contribute failures — never consumption — to the
merged diagnostic.

```sh
go test ./...
```
