# Tail-Call-Optimizing Interpreter (TypeScript)

A small expression-language interpreter pair:

- **`src/naive.ts`** — reference interpreter. Plain recursive evaluation; every
  call pushes a real frame, so its error report always contains the complete
  call stack. Used as the ground truth.
- **`src/optimized.ts`** — tail-call-optimizing interpreter. A call in tail
  position produces a `TailCall` signal and the trampoline loop **reuses the
  current runtime frame** instead of allocating a new one. Only non-tail calls
  allocate frames, so pure tail recursion runs in O(1) frames.

## Error reporting under optimization

Frame reuse means the tail-call history is gone at error time. The optimized
interpreter therefore keeps, per error report (`ErrorReport` in `src/lang.ts`):

- `nonTailStack` — the **complete** non-tail stack, oldest first. Never
  truncated.
- `recentTailCalls` — a **bounded** summary of the most recent tail calls.
  The bound is the constructor parameter `maxTailSummary`; older entries are
  dropped (ring buffer), so memory never grows with recursion depth.
- `tailElided` / `tailTotal` — how many tail calls were dropped / occurred,
  so the report honestly states what is missing.

The snapshot is captured at the innermost frame while the stack is still
intact and attached to the thrown `InterpError`.

## Language

AST in `src/lang.ts`: numbers, booleans, variables, primitive ops
(`+ - * / % = < <= > >= and or not`), `if`, `let`, `begin` (sequencing),
`call`, `raise`. Tail positions: function body, `if` branches, `let` body,
last expression of `begin`.

## Verification (`src/verify.ts`, 48 checks)

1. **Cross-check vs. the naive interpreter** on small programs (arithmetic,
   non-tail recursion, tail recursion, mutual recursion, tree recursion,
   sequencing): identical results **and** identical call-order traces.
2. **Error reports**: complete non-tail stack + bounded tail summary;
   `nonTailStack ++ recentTailCalls` equals the naive interpreter's full
   stack when nothing is elided; `maxTailSummary` of 3 and 0 exercised.
3. **Million-step tail recursion** (`countdown`, mutual `even`/`odd`,
   accumulator `sumTo`, and an error after 1,000,000 tail steps): exactly
   **1 frame allocated**, non-tail depth 1, summary capped at 5 with
   999,996 elided — space stays constant while the naive interpreter
   overflows the JS stack well below 10k steps.

## Run

```sh
npm install        # needs typescript (devDependency)
npm test           # tsc + node dist/verify.js
```
