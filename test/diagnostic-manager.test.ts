import { test } from "node:test";
import assert from "node:assert/strict";
import {
  DiagnosticManager,
  type DiagnosticsProvider,
  type DependencyProvider,
  type Diagnostic,
  type VersionVector,
} from "../src/diagnostic-manager.ts";

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function diag(message: string): Diagnostic {
  return {
    range: { start: { line: 0, character: 0 }, end: { line: 0, character: 1 } },
    message,
  };
}

/** A provider whose responses are resolved manually, in any order. */
class ManualProvider implements DiagnosticsProvider {
  calls: { uri: string; vector: VersionVector; signal: AbortSignal }[] = [];
  private queue: Array<ReturnType<typeof deferred<Diagnostic[]>>> = [];

  provideDiagnostics(uri: string, vector: VersionVector, signal: AbortSignal): Promise<Diagnostic[]> {
    const d = deferred<Diagnostic[]>();
    this.calls.push({ uri, vector, signal });
    this.queue.push(d);
    return d.promise;
  }

  resolveCall(index: number, diagnostics: Diagnostic[]): void {
    const d = this.queue[index];
    assert.ok(d, `no call at index ${index}`);
    d.resolve(diagnostics);
  }
}

class StaticDeps implements DependencyProvider {
  private readonly graph: Record<string, string[]>;
  constructor(graph: Record<string, string[]>) {
    this.graph = graph;
  }
  getDependencies(uri: string): Iterable<string> {
    return this.graph[uri] ?? [];
  }
}

function setup(graph: Record<string, string[]> = {}) {
  const provider = new ManualProvider();
  const manager = new DiagnosticManager({ provider, dependencies: new StaticDeps(graph) });
  const published: { uri: string; diagnostics: readonly Diagnostic[] }[] = [];
  manager.onDiagnosticsPublished((e) => published.push({ uri: e.uri, diagnostics: e.diagnostics }));
  return { provider, manager, published };
}

test("publishes diagnostics when the dependency vector is unchanged", async () => {
  const { provider, manager, published } = setup();
  manager.openFile("a.ts", 1);

  const result = manager.requestDiagnostics("a.ts");
  provider.resolveCall(0, [diag("unused variable")]);

  assert.deepEqual(await result, [diag("unused variable")]);
  assert.equal(published.length, 1);
  assert.equal(published[0]?.uri, "a.ts");
});

test("out-of-order responses: stale request resolving last is discarded", async () => {
  const { provider, manager, published } = setup();
  manager.openFile("a.ts", 1);

  const first = manager.requestDiagnostics("a.ts");
  manager.updateFile("a.ts", 2);
  const second = manager.requestDiagnostics("a.ts");

  // The newer request resolves first.
  provider.resolveCall(1, [diag("fresh")]);
  assert.deepEqual(await second, [diag("fresh")]);

  // The older request resolves afterwards — must be discarded.
  provider.resolveCall(0, [diag("stale")]);
  assert.equal(await first, undefined);

  assert.equal(published.length, 1);
  assert.deepEqual(published[0]?.diagnostics, [diag("fresh")]);
  assert.deepEqual(manager.getPublishedDiagnostics("a.ts"), [diag("fresh")]);
});

test("dependency edit invalidates an in-flight request even when the target file is untouched", async () => {
  const { provider, manager, published } = setup({ "a.ts": ["b.ts"] });
  manager.openFile("a.ts", 1);
  manager.openFile("b.ts", 1);

  const result = manager.requestDiagnostics("a.ts");
  manager.updateFile("b.ts", 2); // a.ts itself is NOT modified
  provider.resolveCall(0, [diag("computed against old b.ts")]);

  assert.equal(await result, undefined);
  assert.equal(published.length, 0);
  assert.equal(manager.getPublishedDiagnostics("a.ts"), undefined);
});

test("single-file version match is not sufficient for cache reuse", async () => {
  const { provider, manager } = setup({ "a.ts": ["b.ts"] });
  manager.openFile("a.ts", 1);
  manager.openFile("b.ts", 1);

  const first = manager.requestDiagnostics("a.ts");
  provider.resolveCall(0, [diag("v1")]);
  await first;
  assert.equal(provider.calls.length, 1);

  // Only the dependency changes; a.ts keeps the exact same version.
  manager.updateFile("b.ts", 2);

  assert.equal(manager.getReusableDiagnostics("a.ts"), undefined);
  const second = manager.requestDiagnostics("a.ts");
  assert.equal(provider.calls.length, 2, "must recompute instead of reusing");
  provider.resolveCall(1, [diag("v2")]);
  assert.deepEqual(await second, [diag("v2")]);
});

test("transitive dependency edits invalidate diagnostics", async () => {
  const { provider, manager, published } = setup({ "a.ts": ["b.ts"], "b.ts": ["c.ts"] });
  manager.openFile("a.ts", 1);
  manager.openFile("b.ts", 1);
  manager.openFile("c.ts", 1);

  const result = manager.requestDiagnostics("a.ts");
  manager.updateFile("c.ts", 2);
  provider.resolveCall(0, [diag("stale")]);

  assert.equal(await result, undefined);
  assert.equal(published.length, 0);
});

test("close and reopen resets versions via epochs: old results never match", async () => {
  const { provider, manager, published } = setup();
  manager.openFile("a.ts", 1);
  manager.updateFile("a.ts", 2);

  const inFlight = manager.requestDiagnostics("a.ts");
  manager.closeFile("a.ts");
  // Reopen: the editor restarts version numbering at 1.
  manager.openFile("a.ts", 1);
  manager.updateFile("a.ts", 2); // numerically identical to the pre-close state

  provider.resolveCall(0, [diag("from previous life")]);
  assert.equal(await inFlight, undefined);
  assert.equal(published.length, 0);

  // Cache from before the close is not reusable even though (uri, version)
  // pairs coincide — the epoch differs.
  const fresh = manager.requestDiagnostics("a.ts");
  assert.equal(provider.calls.length, 2);
  provider.resolveCall(1, [diag("new life")]);
  assert.deepEqual(await fresh, [diag("new life")]);
});

test("deleting a dependency invalidates in-flight and cached diagnostics", async () => {
  const { provider, manager, published } = setup({ "a.ts": ["b.ts"] });
  manager.openFile("a.ts", 1);
  manager.openFile("b.ts", 1);

  // Cache a valid result first.
  const cached = manager.requestDiagnostics("a.ts");
  provider.resolveCall(0, [diag("v1")]);
  await cached;

  // Edit the target so a recomputation is triggered, then, while the new
  // request is in flight, the dependency disappears.
  manager.updateFile("a.ts", 2);
  const inFlight = manager.requestDiagnostics("a.ts");
  manager.deleteFile("b.ts");
  provider.resolveCall(1, [diag("computed while b.ts existed")]);

  assert.equal(await inFlight, undefined);
  assert.equal(published.length, 1, "only the first result was ever published");
  assert.equal(manager.getReusableDiagnostics("a.ts"), undefined);
});

test("a dependency that appears after the snapshot invalidates the result", async () => {
  const { provider, manager, published } = setup({ "a.ts": ["b.ts"] });
  manager.openFile("a.ts", 1);
  // b.ts is referenced but does not exist yet.

  const result = manager.requestDiagnostics("a.ts");
  manager.openFile("b.ts", 1);
  provider.resolveCall(0, [diag("stale")]);

  assert.equal(await result, undefined);
  assert.equal(published.length, 0);
});

test("failed cancellation of a stale request cannot publish", async () => {
  const { provider, manager, published } = setup();
  manager.openFile("a.ts", 1);

  const stale = manager.requestDiagnostics("a.ts");
  manager.updateFile("a.ts", 2);
  const fresh = manager.requestDiagnostics("a.ts");

  // The first request was aborted...
  assert.equal(provider.calls[0]?.signal.aborted, true);

  // ...but the provider ignores cancellation and resolves anyway.
  provider.resolveCall(0, [diag("zombie")]);
  assert.equal(await stale, undefined);

  provider.resolveCall(1, [diag("fresh")]);
  assert.deepEqual(await fresh, [diag("fresh")]);

  assert.equal(published.length, 1);
  assert.deepEqual(published[0]?.diagnostics, [diag("fresh")]);
});

test("precise reuse condition: identical vector reuses cache without a provider call", async () => {
  const { provider, manager } = setup({ "a.ts": ["b.ts"] });
  manager.openFile("a.ts", 1);
  manager.openFile("b.ts", 1);

  const first = manager.requestDiagnostics("a.ts");
  provider.resolveCall(0, [diag("v1")]);
  await first;

  // Unrelated file changes do not affect the closure of a.ts.
  manager.openFile("unrelated.ts", 1);
  manager.updateFile("unrelated.ts", 2);

  const reused = await manager.requestDiagnostics("a.ts");
  assert.deepEqual(reused, [diag("v1")]);
  assert.equal(provider.calls.length, 1, "cache reused, provider not called again");
});

test("closing the target file cancels its pending request", async () => {
  const { provider, manager, published } = setup();
  manager.openFile("a.ts", 1);

  const result = manager.requestDiagnostics("a.ts");
  manager.closeFile("a.ts");
  provider.resolveCall(0, [diag("too late")]);

  assert.equal(await result, undefined);
  assert.equal(published.length, 0);
});

test("rejects non-continuous (stale or duplicate) versions", async () => {
  const { manager } = setup();
  manager.openFile("a.ts", 1);
  manager.updateFile("a.ts", 2);

  assert.throws(() => manager.updateFile("a.ts", 2), /non-continuous/);
  assert.throws(() => manager.updateFile("a.ts", 1), /non-continuous/);
  assert.throws(() => manager.updateFile("ghost.ts", 5), /not open/);
  assert.throws(() => manager.openFile("a.ts", 3), /already open/);
});

test("recreated file gets a fresh epoch and does not reuse deleted-file cache", async () => {
  const { provider, manager } = setup({ "a.ts": ["b.ts"] });
  manager.openFile("a.ts", 1);
  manager.openFile("b.ts", 1);

  const first = manager.requestDiagnostics("a.ts");
  provider.resolveCall(0, [diag("v1")]);
  await first;

  // Delete and recreate the dependency with the same version number.
  manager.deleteFile("b.ts");
  manager.openFile("b.ts", 1);

  assert.equal(manager.getReusableDiagnostics("a.ts"), undefined);
  const second = manager.requestDiagnostics("a.ts");
  assert.equal(provider.calls.length, 2);
  provider.resolveCall(1, [diag("v2")]);
  assert.deepEqual(await second, [diag("v2")]);
});
