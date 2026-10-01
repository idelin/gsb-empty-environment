# json-template-engine

A TypeScript JSON template engine with **path-only reads**, a **whitelisted
function set**, **loops/conditionals**, **dual provenance** (output → template
+ input), **incremental rendering** keyed by **stable entity ids**, and
**transactional publish** with full rollback. No runtime dependencies; tested
with the Node.js built-in test runner.

## Requirements

Node.js **≥ 23.6** (runs TypeScript natively via type stripping), or
Node.js 22.6+ with `--experimental-strip-types`. Only erasable TypeScript
syntax is used, so no build step is needed.

```sh
npm test        # node --test "test/*.test.ts"
npm run demo    # node examples/demo.ts
```

## Template syntax

Templates are JSON-compatible trees. Reads are path-only; there is no
arbitrary code execution.

```jsonc
{
  "title": "${app.title | upper}",          // interpolation + whitelisted fn
  "note": "literal \\${app.title}",          // \${ keeps a literal ${...}
  "users": {
    "$for": "app.users",                     // loop over an array path
    "$as": "u",                              // loop variable (default: "item")
    "$index": "i",                           // optional numeric index variable
    "$body": {
      "name": "${u.name}",                   // single-expression string: typed output
      "tag": "${u.profile.tag | default('none')}",
      "admin": { "$if": "u.admin", "$then": "yes", "$else": "no" },
      "hobbies": { "$for": "u.hobbies", "$as": "h", "$body": "${h}" }
    }
  },
  "card": { "$ref": "userCard" }             // named partial (see options.partials)
}
```

- **Paths**: `a.b.0.c`; loop variables (`u.name`); `$root.x` escapes to the root.
- **Expressions**: `path | fn('lit', 1, true) | other(...)`. Pipe args are
  literals only (string/number/boolean/null).
- **Typed values**: a string that is exactly one `${expr}` yields the raw JSON
  value (number/boolean/null/array/object preserved); embedded expressions are
  stringified (`null` → `"null"`, objects → JSON).
- **Escaping**: `\${` renders a literal `${`.
- **Directives**: `$for/$as/$index/$body`, `$if/$then/$else`, `$ref`. Unknown
  `$`-keys and unknown functions are rejected at parse time with the template
  path.

### Whitelisted functions

`upper`, `lower`, `trim`, `default`, `json`, `length`, `join`, `first`,
`last`, `not`, `eq`. Extra functions can be whitelisted via
`options.functions`. A function receiving a wrong-typed value throws a
`TemplateError` carrying both `templatePath` and `inputPath`.

### Missing vs. explicit null

- **Missing** (path does not exist) follows `options.onMissing`:
  `"empty"` (default) → `""`, `"null"` → `null` for typed expressions,
  `"error"` → throw.
- **Explicit null** is a real value: `default(x)` rescues *only* missing
  values, never `null`. In text, null stringifies to `"null"`; missing
  stringifies to `""`.

## Data operations & entity ids

Input is a JSON-compatible tree (object keys unordered, arrays ordered).
Arrays internally attach a **stable entity id** to every element; identity is
never derived from content, so two identical objects remain distinct.

```ts
engine.transaction((tx) => {
  tx.set("app.users.0.name", "ann");   // upsert (object keys may be created)
  tx.insert("app.users", 1, {...});    // fresh entity id
  tx.remove("app.users", 2);
  tx.move("app.users", 0, 3);          // entity id travels with the element
});
```

## Incremental rendering & provenance

Each loop iteration expands into a cached **fragment** keyed by
`(template node, entity id)`. Dependencies are recorded as **symbolic paths**
(entity ids instead of indices), so fragments survive reorders; validation is
reference equality against the persistent (path-copied) data tree. Nested
loops nest fragments: an inner reorder reuses the inner fragments and only
re-expands the affected outer iteration. Loops using `$index` are
position-sensitive and include the index in their cache key.

Every published output node carries **dual provenance**:

```jsonc
{ "out": "/users/0/name",          // JSON pointer into the output
  "template": "/users/$body/name", // template node
  "input": ["/app/users/0/name"] } // input reads (concrete paths)
```

`engine.fullRender()` re-renders bypassing the cache; the test suite asserts
incremental output **and** provenance equal a full re-render after nested
moves, missing-value flips, and variable-length edits.

## Transactions & budgets

`engine.transaction(fn)` applies ops, renders incrementally, and **atomically
publishes** `{ output, provenance }`. Any failure — bad op, missing-value
error, function failure, budget exceeded — restores the data snapshot and
keeps the previously published result. A failed render never poisons the
fragment cache (fragments are validated by reference against current data).

Budgets (`options.budget`) guard against runaway expansion:

- `maxExpansions` — total loop-iteration + `$ref` expansions per render,
  **shared** across all loops (including nested) and partials.
- `maxDepth` — template recursion depth; stops recursive partials.
- `maxOutputNodes` — total nodes in the published output tree.

## Errors

`TemplateError` carries `templatePath` and (when available) `inputPath`;
`DataError` (from tx ops) carries `path`. All are JSON-pointer locations.

## Layout

- `src/json.ts` — JSON types, pointer helpers
- `src/expr.ts` — expression/pipe/literal parser
- `src/template.ts` — template parser → typed AST with node ids
- `src/functions.ts` — whitelisted built-ins
- `src/store.ts` — persistent data tree, entity ids, symbolic paths, tx ops
- `src/render.ts` — renderer: fragments, deps, provenance, budgets
- `src/engine.ts` — public API: `setData`, `transaction`, `fullRender`
- `test/` — node:test suites; `examples/demo.ts` — runnable walkthrough
