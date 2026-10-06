# boxing-compiler

A small language with nested functions, mutable locals, and escaping closures,
plus a TypeScript compiler pass that lowers captured mutable variables to
heap-allocated boxes. No runtime dependencies.

## Layout

- `src/ast.ts` — surface AST, transformed (boxed) AST, and builder helpers.
- `src/interp.ts` — reference tree-walking interpreter. Closures capture
  environment objects directly, so sharing falls out of pointer semantics.
- `src/transform.ts` — boxing analysis + rewrite. A local is boxed iff it is
  **captured** (referenced from a descendant function) **and mutated**
  (assigned anywhere). Captured-but-immutable variables are captured by value;
  everything else stays in stack-frame slots. Capture lists are transitive so
  intermediate functions forward variables they don't use themselves.
- `src/machine.ts` — executes the transformed program with explicit call
  frames. Unboxed locals live in the frame and cannot escape; boxed locals are
  `{ kind: 'box' }` cells allocated each time the `let` executes, so loop
  iterations and separate call instances get distinct boxes while sibling
  closures created from the same activation share one box. The machine reports
  `boxesAllocated` so tests can pin down allocation behavior.
- `tests/compare.test.ts` — differential suite: every program runs through
  both the reference interpreter and transform+machine; traces and results
  must be identical. Covers sibling closures sharing a box, per-call-instance
  boxes, closures created in loops (per-iteration vs. shared outer variable),
  recursion (self-reference, escaping closures mid-recursion), value captures,
  pass-through captures, boxed parameters, and stack-only locals.

## Language semantics notes

- Only function bodies and while-loop *iterations* introduce scopes; a `let`
  in a loop body is fresh per iteration.
- No shadowing: names declared within one function must be distinct (the
  transform rejects duplicates).

## Running

Requires Node >= 23.6 (or >= 22.6 with `--experimental-strip-types`) for
native TypeScript type stripping:

```sh
node --test "tests/**/*.test.ts"
# or
npm test
```
