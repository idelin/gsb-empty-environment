# minlet-rename

A rename-refactoring library for a tiny language with `let` declarations,
blocks, and identifier references. Renaming is performed by **binding
identity**: the declaration and exactly those references that resolve to it
are rewritten — same-named references belonging to other scopes are left
untouched. A rename is rejected when the new name would change how any
reference in the program resolves (capture or shadowing changes).

## The language

```
program := stmt*
stmt    := "let" ident "=" expr ";" | block | expr ";"
block   := "{" stmt* "}"
expr    := primary (("+" | "*") primary)*
primary := ident | number | "(" expr ")"
```

### Scoping rules

- A block introduces a new scope; inner bindings shadow outer ones.
- A `let` becomes visible only **after** its own statement. A reference that
  textually precedes the declaration in the same block does not see it — it
  resolves to an outer binding or is unbound. The initializer never sees its
  own declaration (`let x = x;` resolves the right-hand `x` outward).
- A later `let` with the same name in the same block shadows the earlier one
  for subsequent statements.
- Unbound references are legal and resolve to `null` in the binding graph.

## API

```ts
import { rename } from './src/index.ts';

const result = rename(source, offset, newName);
// { ok: true, newSource, edits } | { ok: false, reason }
```

`offset` may point at the declaration name or at any reference of the same
binding. Supporting exports: `lex`, `parse`, `bind` (builds the binding
graph), and `checkIsomorphic`.

## How safety is enforced

1. Parse and bind the original source; find the target binding and collect
   the declaration-name span plus the spans of all references resolving to it.
2. Apply the textual edits.
3. **Reparse the renamed source and rebuild the binding graph.** Declaration
   and reference ids are source-order ordinals, so the two graphs are
   directly comparable. The rename is accepted only if the graphs are
   isomorphic: every reference — including unbound ones and references before
   a declaration — must resolve to the same declaration ordinal as before.
4. On mismatch the rename is rejected with a diagnostic: either a renamed
   reference would be captured by another binding of the new name, or an
   existing reference to the new name would change resolution.

## Requirements and tests

Written in erasable-syntax TypeScript. Running the tests requires Node
>= 23.6 (native type stripping) or Node >= 22.6 with
`--experimental-strip-types`:

```sh
node --test 'test/*.test.ts'
```
