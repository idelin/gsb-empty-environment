# ts-unify

A small first-order unification engine (the core of Hindley-Milner style
type inference) over a type language with:

- **type variables** (`TVar`)
- **function types** (`t -> t`)
- **list types** (`List<t>`)
- **ground constructors** (`Int`, `Bool`, ...)
- **equality constraints** solved left to right by `unifyAll`

## Guarantees

- **Occurs check** — binding a variable to a type that (transitively)
  mentions it is rejected, so no infinite types are ever constructed.
- **Recursive error chain** — occurs-check failures report the full chain
  of substitutions that leads back to the variable, e.g.

  ```
  occurs check failed: cannot construct the infinite type of b
    b ~ c -> a
    a ~ List<b>   <- recursive
  ```

- **Transitive substitution** — `applySubst` follows binding chains of any
  length and normalises recursively inside structure; the result is
  idempotent (`applySubst(s, applySubst(s, t)) == applySubst(s, t)`).

## Testing approach

`src/gen.ts` generates constraint systems that are **satisfiable by
construction**: it builds a random acyclic substitution `sigma`, then emits
constraints `t ~ applySubst(sigma, t)` for random types `t`. The test suite
(`test/run.ts`) checks, across many seeds:

1. the known substitution solves every generated constraint;
2. `unifyAll` finds a solution and **the solution substituted back** makes
   both sides of every constraint structurally equal;
3. the computed solution is normalised (idempotent);
4. unsatisfiable samples (constant clash, shape mismatch, direct and
   indirect occurs violations) fail with the expected error kind and a
   non-empty recursion chain.

## Commands

```sh
npm test        # typecheck, compile, run the suite
npm run build   # compile to dist/
```
