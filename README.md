# closure-boxing-lab

A small language with nested functions, mutable locals, and escaping closures,
plus a TypeScript compiler library that performs **capture analysis and
boxing**, and a test suite that runs every program through both the original
AST interpreter and the transformed-code runtime and compares the results.

## Layout

- `src/ast.ts` — source AST (nested `fn` expressions, `let`/`const`, loops, arrays)
- `src/interp.ts` — reference tree-walking interpreter over the original AST;
  defines the language semantics (by-reference capture, fresh bindings per
  loop iteration)
- `src/compiler/resolve.ts` — scope resolution + capture analysis: marks every
  binding referenced from a nested function and computes each function's
  (transitive) free-variable set
- `src/compiler/tast.ts` — transformed AST with explicit storage operations
  (`slot.*`, `box.*`, `upval.*`)
- `src/compiler/transform.ts` — the boxing transform
- `src/runtime/executor.ts` — runtime for the transformed AST: explicit call
  stack of frames (stack slots die with the frame) + heap boxes, with
  allocation/depth counters for observability
- `tests/run.ts` — comparison suite

## Boxing policy

| binding                | storage                                             |
| ---------------------- | --------------------------------------------------- |
| captured, mutable      | heap box, created where the `let` executes          |
| captured, const        | stack slot; children capture a copy **by value**    |
| not captured           | plain stack slot                                    |

Because `box.new` executes where the `let` executes:

- **sibling closures** created in the same scope execution share one box;
- **different call instances** of the enclosing function get separate boxes;
- **closures created in a loop** get a fresh box per iteration for variables
  declared inside the loop body, and share the box of anything declared
  outside the loop.

Captured variables are threaded through intermediate functions as upvalues
(flat closure conversion), so a deeply nested function only retains what it
actually references.

## Running

```
npm test        # tsc + node dist/tests/run.js
```

The suite compares interpreter vs. compiled execution (return value + printed
output) and additionally asserts runtime invariants: box identity between
sibling closures, per-instance / per-iteration box separation, zero boxes for
non-captured locals, by-value const capture, compile-time rejection of const
assignment, and a balanced call stack. Covered scenarios include closures
created in loops and recursive calls (self-name recursion, shared boxed
accumulator across recursion levels, per-frame boxes for captured locals).
