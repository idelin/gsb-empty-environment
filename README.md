# ts-debug-interpreter

A small tree-walking interpreter written in TypeScript, plus a debugger with
single-step, continue, and conditional breakpoints. No dependencies; runs
directly on Node.js >= 22.6 via native type stripping.

## The language

```
let x = 1;                 // variables: numbers, strings, booleans, null
fn fact(n) {               // functions are values, closures supported
  if (n <= 1) { return 1; }
  return n * fact(n - 1);  // recursion
}
while (x < 10) { x = x + 1; }
if (x == 10) { print("done"); } else { print("oops"); }
```

Operators: `+ - * / %`, `== != < <= > >=`, `&& || !`, assignment `=`
(an expression), calls `f(a, b)`. `print(...)` is a built-in that appends to
the run's output. Division/modulo by zero is a runtime error.

## Debugger semantics

The interpreter is generator-based: it yields a `BeforeEvent` **before each
statement executes**, which drives both stepping and breakpoints.

- `step()` executes exactly one statement and pauses before the next one —
  even if that is the same AST node again (e.g. a one-statement loop body).
- `continue()` runs until a breakpoint fires or the program ends. **One
  continue cannot immediately re-hit the same node**: after resuming, hits on
  the node it paused at are suppressed until some *other* node has executed.
  Every execution is still recorded in the trace.
- Breakpoints fire **before** the node executes, so `getVariable` at a pause
  shows pre-statement state.
- `addBreakpoint(nodeId, condition?)` — the condition is parsed as an
  expression in the language and evaluated in the scope of the paused node.
- **Conditions are side-effect free**: they are evaluated by a separate pure
  evaluator (`evalPureExpr`). Assignments and function calls inside a
  condition raise a `side-effect` error; runtime failures (undefined
  variable, division by zero, ...) raise an `evaluation` error. Both are
  collected in `debugger.conditionErrors` — listed separately from program
  runtime errors (`debugger.error`) — and the breakpoint simply does not
  fire for that encounter. Program state is never mutated by conditions.
- While paused, `setVariable(name, value)` writes into the live scope chain
  (defining the variable if it does not exist), and execution resumes with
  the modified state.

## Cross-checking full runs against step traces

`runProgram(source)` and a fully stepped `Debugger` run share the same
engine, and the test suite verifies they agree:

- `tests/loops.test.ts` — identical output/trace for full vs. stepped loop
  runs; breakpoint-before-execution; per-iteration hits; same-node
  suppression on `continue`.
- `tests/recursion.test.ts` — `fact(5)` full vs. stepped equality; trace
  depth mirrors the call stack; conditional hit counts (`n == 2`, `n <= 2`,
  ...).
- `tests/conditions.test.ts` — side-effecting conditions (`x = 99`,
  `print(x)`) neither mutate state nor print; evaluation failures are listed
  separately; erroring conditions leave output and trace identical to a
  clean full run.
- `tests/modify.test.ts` — pausing mid-loop and injecting `total = 100`
  turns the final `6` into `106`; injecting `i = 10` reshapes control flow;
  the executed-statement trace still matches the unmodified run.

## Running

```sh
npm test            # picks a Node >= 22.6 (nvm/n aware) and runs the suite
node examples/demo.ts
```

## Layout

- `src/lexer.ts` — tokenizer
- `src/ast.ts` — AST types (unique `id`, source offsets, line per node)
- `src/parser.ts` — recursive-descent parser
- `src/interpreter.ts` — environment, generator evaluator, pure condition evaluator
- `src/debugger.ts` — `Debugger` (step/continue/breakpoints/variables) and `runProgram`
- `tests/` — trace/output cross-checking suite
- `examples/demo.ts` — end-to-end debugging demo
