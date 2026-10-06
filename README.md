# binding-rename

A rename-refactoring library for a small language with `let` declarations,
blocks, and identifier references. Renames are performed by **binding
identity**: only the declaration and the references that resolve to that exact
binding are rewritten — same-spelled identifiers owned by other scopes are
never touched. A rename is rejected when the new name would change how any
identifier resolves (capture or shadowing changes), and every accepted rename
is verified by re-parsing the result and checking that the binding graphs are
isomorphic.

## The language

```
program := stmt*
stmt    := "let" ident "=" expr ";" | block | expr ";"
block   := "{" stmt* "}"
expr    := ident | number | "(" expr ")" | expr ("+"|"-"|"*"|"/") expr
```

Scoping rules:

- `let` introduces a block-scoped binding.
- **Declare-before-use**: a binding is visible only *after* its `let`
  statement (its `visibleFrom` is the end of the statement). References
  earlier in the same block — including references inside the declaration's
  own initializer — do not resolve to it; they resolve to an outer binding or
  remain unbound.
- Inner blocks may shadow outer bindings; within one block, a later
  same-named `let` shadows the earlier one for subsequent references.

## API

```ts
import { rename } from "./src/index.ts";

const result = rename(source, offset, newName);
// { ok: true, source, edits } | { ok: false, reason }
```

`offset` may point at the declaration name or at any reference of the
binding.

## How it works

1. **Parse & bind** (`src/parser.ts`, `src/binder.ts`): builds scopes and a
   resolution map from every reference to its binding (or `null`).
2. **Collect edits by identity** (`src/rename.ts`): the declaration plus all
   references whose resolution is exactly that binding.
3. **Reject resolution-changing names**: for each of the binding's
   references, simulate resolution under the new name — it must still resolve
   to the same binding (no intervening `newName` binding may capture it);
   and every existing occurrence spelled `newName` (bound elsewhere or
   unbound) must not start resolving to the renamed binding.
4. **Verify by re-parse** (`src/verify.ts`): the edited source is re-parsed
   and re-bound, and the old/new binding graphs are checked for isomorphism —
   identical tree shape, edits confined to the intended nodes, and every
   reference resolving to the binding at the same tree path (unbound stays
   unbound). Any mismatch rejects the rename.

## Requirements & tests

Requires Node.js >= 22.6 (uses `--experimental-strip-types` to run TypeScript
directly; no dependencies, no build step).

```sh
npm test
```
