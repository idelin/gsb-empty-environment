# template-engine

A small TypeScript template engine with variables, conditionals and loops,
implemented as a **direct AST interpreter** (lexer → parser → tree-walking
evaluator; no code generation).

## Syntax

```
Hello {{ user.name }}!
{% if user.admin %}admin{% elif user.active %}active{% else %}guest{% endif %}
{% for item in items %}{{ item }}{% endfor %}
```

- `{{ expr }}` — output a dotted path (`user.name`) or a literal
  (string, number, `true` / `false` / `null`).
- `{% if %}` / `{% elif %}` / `{% else %}` / `{% endif %}` — conditionals.
- `{% for x in xs %}` / `{% endfor %}` — loop over an array.

## Semantics

- **Lazy branches**: only the taken branch of a conditional is evaluated;
  expressions in unselected branches (and conditions after a matched branch)
  are never touched, so they may reference variables that do not exist.
- **Loop scoping**: the loop variable is bound in a fresh child scope per
  iteration. It shadows any outer variable of the same name, and the outer
  binding is intact again after the loop.
- **Undefined variables**: reading one throws `TemplateRenderError` with the
  template source position (line/column) of the expression and the template
  call stack (enclosing `{% for %}` / `{% if %}` constructs, innermost first).
- **Source provenance**: `renderFragments` returns one fragment per output
  chunk, each carrying the span of the template node that produced it.

## API

```ts
import { renderTemplate, renderFragments, parseTemplate } from "./src/index.ts";

renderTemplate("Hi {{ name }}", { name: "Ada" });      // "Hi Ada"
renderFragments("A{{ x }}B", { x: 1 });                // Fragment[] with spans
parseTemplate("{% if a %}x{% endif %}");               // AST
```

## Requirements & tests

Requires Node.js ≥ 23.6 (runs TypeScript natively via type stripping;
`.nvmrc` pins 23.9.0). No dependencies to install.

```sh
nvm use        # optional, picks the pinned Node
npm test
```

The test suite (`tests/engine.test.ts`) covers variables, lazy conditional
branches, loop shadowing/restore, error positions and template call stacks,
fragment provenance, and cross-checks the interpreter against an independent
reference AST evaluator for nested loops, empty loops and shadowing.
