# plugin-host

A TypeScript plugin host with dependency-ordered initialization and
reference-counted shared dependencies.

## Semantics

- **Dependency order** — `install()` resolves the transitive dependency
  closure and initializes plugins dependencies-first (cycles and unknown
  dependencies are rejected up front).
- **Scoped rollback** — references are acquired eagerly per install. If a
  plugin fails, the failing install releases exactly what it acquired:
  instances it created are disposed in reverse order, while pre-existing
  shared dependencies referenced by other installs are kept.
- **Reverse-order cleanup** — `uninstall()` / `dispose()` dispose instances
  whose reference count reaches zero, dependents before dependencies.
  Cleanup failures are collected and reported together as a `CleanupError`
  after every remaining cleanup has run.
- **Concurrent installs** — concurrent `install()` calls share an in-flight
  initialization of the same plugin (the factory runs once); a failure
  propagates to every waiter as a `PluginInstallError` naming the failed
  plugin, and a later install retries.

## Layout

- `src/plugin-host.ts` — `PluginHost`, error types, plugin definitions.
- `test/plugin-host.test.ts` — behavior tests driven by a
  `ControllableFactory` (`test/helpers.ts`) that parks each factory call
  until the test resolves or rejects it, enabling deterministic
  verification of reference counting and failure propagation.

## Commands

```sh
npm run build   # type-check + emit to dist/
npm test        # build, then run the test suite
```
