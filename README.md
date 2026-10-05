# config-merge

Layered configuration merging for TypeScript: defaults, JSON files, environment
variables and CLI arguments, with list operations, full provenance tracking and
secret masking.

Requires Node.js >= 22.6 (runs TypeScript directly via type stripping).

```sh
npm test
```

## Quick start

```ts
import { loadConfig } from './src/index.ts';

const config = loadConfig({
  schema: {
    port: 'number',
    debug: 'boolean',
    tags: 'string[]',
    db: { host: 'string', password: 'secret:string' },
  },
  defaults: { port: 80, tags: ['base'], db: { host: 'localhost' } },
  files: ['config.json'],
  env: process.env,
  envOptions: { prefix: 'APP_' },        // APP_PORT=8080, APP_DB__HOST=...
  argv: process.argv.slice(2),           // --port 9090 --tags+=a,b
});

config.get('port');            // 9090
config.explainText('db.password');
// db.password = "********"  <- env:APP_DB__PASSWORD
//   overridden: "********"  <- file:config.json:4:5
```

Priority order: **defaults < files (in order) < env < argv**.

## List operations

Keys accept a suffix to control how arrays merge (objects and `--flags`):

| Suffix   | Operation | Example                          |
|----------|-----------|----------------------------------|
| `!`/`=`  | replace   | `{ "tags!": ["x"] }`, `--tags!=x` |
| `+`      | append    | `{ "tags+": ["y"] }`, `--tags+=y` |
| `-`      | remove    | `{ "tags-": ["z"] }`, `--tags-=z` |

Every final element keeps the source it came from, and every replace/append/
remove is recorded as an event (`config.explain('tags')`).

## Provenance

`config.explain(path)` returns the winning `source` plus the `history` chain of
overridden values. `config.explainText(path)` renders a human-readable report.
`config.diff(other)` compares two merged configs (secret-aware).

## Secrets

Schema types prefixed with `secret:` are wrapped in `SecretValue`:

- `String(value)`, `JSON.stringify`, `util.inspect` and `config.toJSON()` all
  show `********`.
- `value.reveal()` accesses the raw value explicitly.
- `value.equals(other)` and `config.diff()` compare without exposing it.

## Validation

All errors are `ConfigError` with `code`, `path` and `source` (including
`file:line:column` for JSON files):

- `DUPLICATE_KEY` — repeated keys in a JSON file (both locations reported)
- `NULL_VALUE` — explicit `null` for a non-nullable key (`'string|null'` opts in)
- `TYPE_CONVERSION` — e.g. `APP_PORT=abc` for a `number` key
- `UNKNOWN_KEY` — key missing from the schema (disable with `allowUnknown`)
- `INVALID_LIST_OP` — `+`/`-` used on a non-array key

Schema types: `string`, `number`, `boolean`, each with `[]` (array), `?`
(optional), `|null` (nullable) and `secret:` (masked) modifiers. Without a
schema, values are merged as-is (env/argv values stay strings).
