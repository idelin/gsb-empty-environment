# Safe Template Sandbox

Zero-dependency TypeScript template lexer, parser, and AST-walking interpreter for multi-tenant notification and page-rendering services.

The package never calls `eval`, `new Function`, or Node's `vm`. Templates are parsed into a closed AST and evaluated by a recursive interpreter. Free identifiers resolve only from the host-provided context; there is no global object fallback.

## Quick Start

```ts
import {
  SafeTemplateEngine,
  builtInFilters,
  type SafeTemplateOptions,
} from './src/index.ts';

const options: SafeTemplateOptions = {
  filters: { ...builtInFilters }, // explicit whitelist; omit filters you do not trust
  escape: 'html',                // optional; default is raw output
  quotas: {
    maxAstDepth: 96,
    maxLoopIterations: 10_000,
    maxOutputBytes: 262_144,
    maxEvaluationSteps: 250_000,
  },
};

const engine = new SafeTemplateEngine(options);

const html = engine.render(
  `{% for user in users %}{{ user.name | upper }}{% if not loop.last %}, {% endif %}{% endfor %}`,
  { users: [{ name: 'ada' }, { name: 'grace' }] },
);
```

One-shot rendering is also available:

```ts
import { renderTemplate } from './src/index.ts';

const text = renderTemplate(`Hi {{ user.name }}`, { user: { name: 'Ada' } });
```

## Supported Syntax

- Text and `{{ expression }}` output tags.
- `{# comment #}` comments and `{%- -%}` whitespace control.
- Variables and paths: `user.name`, `users[0]`, `user['na' + 'me']`.
- Arrays and simple objects: `[1, 2, 3]`, `{ kind: 'email' }`.
- Arithmetic: `+`, `-`, `*`, `/`, `%`.
- Comparisons: `===`, `!==`, `==`, `!=`, `<`, `<=`, `>`, `>=`.
- Logic: `&&`, `||`, `??`, `!`, plus `and`, `or`, `not`.
- Ternary expressions: `{{ score >= 90 ? 'A' : 'B' }}`.
- Whitelisted filters: `{{ name | trim | upper }}` or `{{ price | currency('CNY') }}`.
- Calls such as `upper(name)` are allowed only when `upper` is a registered filter.
- Blocks:
  - `{% if cond %}...{% elseif other %}...{% else %}...{% endif %}`
  - `{% for item in items %}...{% endfor %}`
  - `{% for value, key in object %}...{% endfor %}`

Loop metadata is exposed as `loop` and `forloop`: `index`, `index0`, `index1`, `first`, `last`, `length`, and `key`.

## Security Model

- Property reads use `Object.hasOwn` plus an own-property descriptor check.
- Non-enumerable and accessor properties are not exposed; getters are not invoked.
- Inherited prototype members such as `toString`, `map`, `join`, and `hasOwnProperty` are rejected.
- `__proto__`, `constructor`, `prototype`, `__defineGetter__`, `__defineSetter__`, `__lookupGetter__`, and `__lookupSetter__` are always blocked, including dynamically computed keys such as `obj['con' + 'structor']`.
- Function-valued own properties are not returned to templates.
- Arbitrary calls, method calls, arrow functions, function expressions, destructuring, spread syntax, assignment, `new`, and optional chaining are rejected by the parser or evaluator.
- The context is never mutated by template syntax. Loop bindings are stored in isolated `Map` frames.
- Filters are callable only when they are own properties of the host-registered whitelist. Inherited filters are ignored. Register only synchronous, pure, bounded functions.

Absent data keys resolve to `undefined`; accessing an existing inherited prototype member throws `TemplateSecurityError`. Set `strictVariables: true` to throw for unknown root variables.

For template ergonomics, numeric `length` metadata is available on real arrays and strings. All other non-enumerable or inherited properties remain blocked.

Host context should contain plain JSON-like data. A host-provided `Proxy`, getter, or stateful filter is trusted host code and is outside the tenant-template boundary.

## Quotas

| Option | Default | Purpose |
| --- | ---: | --- |
| `maxAstDepth` | `96` | Maximum template/expression AST nesting. |
| `maxLoopIterations` | `10000` | Total iterations across all `for` loops in one render. |
| `maxOutputBytes` | `262144` | Maximum UTF-8 output size. |
| `maxEvaluationSteps` | `250000` | AST evaluation step budget. |
| `maxTemplateLength` | `1000000` | Maximum source length. |
| `maxExpressionLength` | `10000` | Maximum expression length. |

Quota failures throw `TemplateQuotaError` with a `quota` field. Parse and security failures throw `TemplateParseError` and `TemplateSecurityError`.

Quota fields can be supplied under `quotas`, under `limits`, or flat on the options object. Common aliases such as `maxDepth`, `maxIterations`, `maxSteps`, and `maxOutputSize` are accepted.

## API

- `parseTemplate(source, options?)`: tokenize and parse template text into `TemplateNode[]`.
- `parseExpression(source, options?)`: parse one restricted expression into an `Expression`.
- `tokenizeTemplate(source, options?)` and `tokenizeExpression(source, options?)`: lexer entry points.
- `renderTemplate(source, context?, options?)`: parse and render in one call.
- `renderAst(ast, context?, options?)`: evaluate a previously parsed AST.
- `new SafeTemplate(source, options)` with `.render(context)`.
- `new SafeTemplateEngine(options)` with `.render(source, context)`, `.compile(source)`, and `.registerFilter(name, fn)`.
- `builtInFilters`: optional safe filters. Spread only the filters you want to expose into `filters`.

Compatibility modules are available at `./src/lexer.ts`, `./src/parser.ts`, `./src/evaluator.ts`, and `./src/template-engine.ts`.

## Tests

```bash
npm test
```

The Node built-in test suite covers legal complex rendering, dynamic `constructor` concatenation, prototype traversal, array callback escape attempts, destructuring payloads, inherited filters, read-only context behavior, AST-depth bombs, loop bombs, output-size bombs, and evaluation budget exhaustion.
