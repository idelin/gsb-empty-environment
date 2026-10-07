# vpakit — visibly pushdown automata: recognition & bounded difference search

Go library for **visibly pushdown automata (VPA)**, the standard model for
protocol/parser stacks: the input alphabet is explicitly partitioned into
**call**, **return** and **internal** symbols, and a **return on an empty
stack** is handled by a dedicated empty-stack rule (not a crash).

## Semantics highlights

- **call** pushes one stack symbol, **return** pops one (empty stack: special
  rule), **internal** never touches the stack.
- A missing transition rejects the word (implicit trap state).
- **Stack-depth limits are a range bound, not rejection.** A word that would
  exceed the configured depth is `OutOfRange` — excluded from the analysis,
  never counted as accepted, rejected, or as evidence that two automata agree.

## What the library does

- `VPA.Run(word, maxDepth)` — recognise one word, with a full step-by-step
  trace (state, stack contents, push/pop notes) and outcome
  `accept` / `reject` / `out-of-range`.
- `ShortestDistinguishingWord(a, b, maxLen, maxDepth)` — BFS over the product
  of both automata; returns a shortest word (length ≤ maxLen, depth ≤
  maxDepth) accepted by exactly one of them, plus both traces and the reason.
  A negative result is explicitly **not** an equivalence proof.
- Independent verification (`verify.go`, shares no code with the above):
  - `ConfirmMinimal` re-simulates the candidate word and exhaustively checks
    every strictly shorter word;
  - `Exhaustive` enumerates all words over the small alphabet up to a length
    bound, word by word, and cross-checks acceptance counts per length.

## Run

```sh
go run ./cmd/vpadiff   # demo: parser-v1 vs parser-v2, length<=20, depth<=8
go test ./vpa/         # unit tests + exhaustive small-alphabet cross-check
```

The demo pair differs first on `open exec exec close` (length 4): parser-v1
allows unlimited `exec` per request level, parser-v2 at most one.
