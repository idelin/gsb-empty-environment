# plugin-host

Dependency-ordered asynchronous plugin host for TypeScript with shared-dependency
reference counting, scoped rollback on failure, and error-collecting cleanup.

## Semantics

- **Dependency-ordered initialization** — `install(name)` recursively initializes
  dependencies before the plugin itself; each plugin factory runs at most once per
  live instance and receives its dependency instances via `context.deps`.
- **Scoped rollback** — if any plugin in the subtree fails to initialize, only the
  instances created by that install call are rolled back (disposed in reverse
  creation order). Pre-existing shared dependencies are kept untouched. The
  original error propagates wrapped in `PluginInstallError`, with any rollback
  disposal failures collected on `rollbackFailures`.
- **Reference counting** — an instance stays alive while it has root installs
  (`install`/`uninstall` pairs) or initialized dependents. Concurrent installs
  that race on the same dependency share a single in-flight initialization, and a
  failure is propagated to every waiter. Failed records are evicted, so a later
  install retries cleanly.
- **Reverse-order cleanup** — `uninstall(name)` releases one reference; once a
  plugin is unreferenced it and its unused dependencies are disposed in reverse
  dependency order. `shutdown()` disposes everything in reverse dependency order.
  Disposal failures are collected and returned, never aborting remaining cleanup.
- **Cycle detection** — circular dependencies are reported as `PluginCycleError`,
  both within a single install (dependency chain) and across concurrent installs
  (wait-for graph), so cyclic definitions can never deadlock the host.

## API

```ts
const host = new PluginHost();
host.register({
  name: 'database',
  factory: async () => connect(),
  dispose: async (db) => db.close(),
});
host.register({
  name: 'server',
  dependencies: ['database'],
  factory: async ({ deps }) => startServer(deps.database),
});

await host.install('server');   // connects db, then starts server
await host.uninstall('server'); // stops server, then closes db
await host.shutdown();          // disposes anything still alive
```

## Development

```sh
npm install     # requires network access to fetch the typescript devDependency
npm run build   # compiles src/ and test/ into dist/
npm test        # builds, then runs the test suite with plain node
```

The test suite (`test/plugin-host.test.ts`) uses a controllable factory
(`test/controllable-factory.ts`) with manual gates and failure injection to
verify initialization order, reference counting, scoped rollback, concurrent
sharing of in-flight dependencies, failure propagation, and cleanup error
collection. Tests run through a tiny dependency-free runner (`test/runner.ts`),
so the only dev dependency is `typescript` itself.
