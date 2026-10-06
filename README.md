# provenance-template-engine

A small TypeScript template engine with variables, conditionals, loops,
lexically scoped loop bindings, provenance-tracked output, and precise
error reporting (template position + include call stack). Correctness is
verified by running two independent execution engines — a direct AST
interpreter (reference model) and a JavaScript code generator — against
each other on randomized nested templates.

## Syntax

```
Hello {{ user.name }}!                     variable interpolation (dotted paths)
{{#if user.admin}}...{{else}}...{{/if}}    conditional (else optional)
{{#each items as it}}{{ @index }}:{{ it }}{{/each}}   loop (`as it` optional, default `item`)
{{> partialName }}                         include another template
```

## Semantics

- **Lazy branches** — only the taken `{{#if}}` branch is evaluated; variables
  in the untaken branch are never resolved, so `{{#if ok}}yes{{else}}{{missing}}{{/if}}`
  renders `yes` without touching `missing`.
- **Loop scoping** — each iteration pushes a fresh scope frame holding the loop
  variable and `@index`. These shadow outer bindings inside the loop body and
  are popped on exit, restoring the outer binding. Nested loops may reuse the
  same variable name; each level restores correctly.
- **Strict variables** — reading an undefined variable (anywhere, including
  `{{#if}}` conditions) throws `UndefinedVariableError` carrying the template
  name, 1-based line/column, and a snapshot of the include call stack:
  ```
  UndefinedVariableError: variable 'value' is not defined
    at field:1:1
    included from card:1:7 (-> field)
    included from main:1:5 (-> card)
  ```
- **Provenance fragments** — `render()` returns `{ text, fragments }`; every
  fragment keeps the `Span` (template, start/end line+col+offset) of the text
  run or tag it was produced from.
- **Empty loops** — iterating an empty array emits nothing and leaves the
  scope stack untouched; iterating a non-array throws `TemplateTypeError`.

## Dual-engine verification

`TemplateEngine` runs in one of two modes with identical observable behaviour:

- `interpret` — direct tree-walking interpreter over the AST (reference model);
- `compile` — compiles the AST to a JavaScript function. Control flow becomes
  native `if`/callback structure; all value semantics (scope resolution,
  truthiness, iteration, includes) go through the shared runtime in
  `src/runtime.ts`, so both engines agree by construction on values and by
  differential testing on control flow.

`test/differential.ts` generates random templates (deep nesting, empty loops,
deliberate variable shadowing, includes) plus random data, renders with both
engines, and compares the full outcome: output text, provenance fragments, or
the exact error (class, message, span, call stack). Run with more iterations
via `node dist/test/run.js 20000`.

## Build & test

No network access is required; `scripts/build.mjs` locates a locally installed
TypeScript (override with `TYPESCRIPT_PATH`).

```
npm run build   # type-check + emit to dist/
npm test        # build, then run unit + differential tests
```

## Layout

- `src/ast.ts` — AST node types and source spans
- `src/parser.ts` — lexer + parser, syntax errors with positions
- `src/runtime.ts` — fragments, scope chain, shared runtime helpers
- `src/interpreter.ts` — reference model (direct AST interpretation)
- `src/compiler.ts` — AST → JS codegen
- `src/engine.ts` — `TemplateEngine` public API
- `src/errors.ts` — error hierarchy with span + call-stack formatting
- `test/unit.ts` — semantic unit tests
- `test/differential.ts` — interpreter-vs-compiler differential fuzzer
