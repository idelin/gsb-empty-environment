# TCO Interpreter

A mini Lisp-like language with two interpreters written in TypeScript:

- `src/tco.ts` — tail-call-optimizing interpreter. Function application runs
  on an explicit trampoline loop; a call in tail position reuses the current
  runtime `Frame` in place instead of pushing a new one. Only genuinely
  non-tail calls grow the stack.
- `src/naive.ts` — unoptimized reference interpreter (plain recursion, every
  call pushes a frame). Used to cross-check results and call order.

## Error reporting

`raiseError` snapshots a `CallTrace` (`src/runtime.ts`):

- **Full non-tail stack** — every `Frame` currently on `ctx.stack`, outermost
  to innermost, with function name and arguments.
- **Bounded tail-call summary** — a fixed-capacity ring buffer (`TailRing`)
  holding the *most recent* tail calls, each tagged with a global 1-based
  ordinal. The capacity is set by the `maxTailSummary` option and never grows
  with recursion depth; the trace also reports the exact total number of tail
  calls so omitted entries are accounted for.

Example (cap = 5, after a 1,000,000-step tail loop):

```
Trace (most recent call last):
  at f(1000000)
  at g(1000000)
  at loop(0)
  ... 999995 earlier tail call(s) omitted ...
  tail #999996 loop(4)
  tail #999997 loop(3)
  tail #999998 loop(2)
  tail #999999 loop(1)
  tail #1000000 loop(0)
Error: counter exhausted
```

## Space accounting

`runTco` returns `SpaceStats` proving the bound:

- `framesAllocated` / `maxStackDepth` — O(non-tail depth), not O(steps);
  a 1,000,000-step tail recursion uses exactly 1 frame at depth 1.
- `tailSummaryMax` — never exceeds `maxTailSummary` (0, 4, 8, 16 tested).

## Verification (`test/tests.ts`, 57 checks)

1. **Differential tests** — 15 small programs (tail/non-tail/mixed/mutual
   recursion, argument evaluation order, short-circuiting, 7 error kinds):
   TCO and naive interpreters must produce identical outcomes *and* identical
   `enter`/`exit`/`error` event logs (call order). For a purely non-tail
   error, the TCO non-tail stack must equal the naive interpreter's full stack.
2. **Space tests** — million-step tail recursions (self, mutual, and wrapped
   in non-tail frames) verify the counters above; the naive interpreter
   overflows the JS stack at 200k depth for contrast.
3. **Error-trace tests** — exact non-tail stack contents, most-recent
   retention with ordinals, cap = 0, cap > chain length, and boundedness at
   1,000,000 tail steps.

## Run

```
npm test        # builds with tsc, then runs the suite
```
