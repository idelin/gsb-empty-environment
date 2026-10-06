# coroutine-interpreter

A small step-driven interpreter (TypeScript) supporting `yield`,
`try`/`finally` and cooperative cancellation.

## Semantics

- **Explicit stepping**: `step()` runs until the next `yield` or until the
  program completes, returning `{ type: 'yield' | 'return' | 'throw', ... }`.
- **Cancellation at resume points**: `cancel()` never interrupts
  synchronously. A `CancelError` is injected at the start of the next
  `step()` — exactly at the resume point.
- **Finally may yield, runs once**: `finally` blocks can suspend on `yield`
  (cleanup may be asynchronous), but each finally executes at most once.
  Re-cancellation while a finally is suspended abandons it (it is never
  re-entered) and unwinding continues to outer finally blocks.
- **Cleanup exceptions override**: if a finally throws while an exception or
  cancellation is in flight, the cleanup exception replaces the in-flight
  one, and the original is preserved on the new error's `cause` property.

## Instruction set

Programs are flat instruction arrays (see `src/program.ts` for a labelled
assembler):

| Instruction | Meaning |
| --- | --- |
| `yield v` | suspend, emitting `v` |
| `return v` | complete with value `v` (runs pending finally blocks) |
| `throw e` | raise `e` (runs pending finally blocks) |
| `pushFinally h` | enter a try whose finally starts at instruction `h` |
| `popFinally` | leave the try normally: run the finally once, then continue |
| `endFinally` | end of a finally body: resume the in-flight completion |

## Build & test

Requires any `tsc` >= 4.5 on `PATH` (no runtime dependencies):

```sh
npm run build   # tsc -p tsconfig.json
npm test        # build + node dist/test/interpreter.test.js
```

The tests in `test/interpreter.test.ts` drive the interpreter through the
explicit `step()`/`cancel()` interface and cover: normal return, injection
of cancellation at resume points, nested cleanup ordering, re-cancellation
during a suspended finally (runs-once guarantee), cleanup-exception
override with `cause` chaining, and finally on normal/return/throw paths.
