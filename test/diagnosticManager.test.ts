import { test } from "node:test";
import assert from "node:assert/strict";
import {
  DiagnosticManager,
  vectorMatches,
  type ComputeContext,
  type Diagnostic,
  type DiagnosticsChangedEvent,
  type FileStamp,
} from "../src/index.ts";

const MAIN = "file:///main.ts";
const DEP = "file:///dep.ts";
const OTHER = "file:///other.ts";

function diag(message: string): Diagnostic {
  return {
    range: { start: { line: 0, character: 0 }, end: { line: 0, character: 1 } },
    message,
    severity: "error",
  };
}

interface Deferred<T> {
  promise: Promise<T>;
  resolve(value: T): void;
  reject(reason?: unknown): void;
}

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

/** A computer whose every call returns a manually resolvable promise. */
class ScriptedComputer {
  calls: Array<{ context: ComputeContext; deferred: Deferred<Diagnostic[]> }> = [];

  readonly compute = (context: ComputeContext): Promise<Diagnostic[]> => {
    const d = deferred<Diagnostic[]>();
    this.calls.push({ context, deferred: d });
    return d.promise;
  };

  get count(): number {
    return this.calls.length;
  }

  last() {
    return this.calls[this.calls.length - 1];
  }
}

function flush(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

// ---------------------------------------------------------------------------
// Basic acceptance
// ---------------------------------------------------------------------------

test("accepts a result whose version vector matches the current state", async () => {
  const computer = new ScriptedComputer();
  const manager = new DiagnosticManager(computer.compute);
  manager.open(MAIN, "const a = 1;");

  manager.requestDiagnostics(MAIN);
  computer.last().deferred.resolve([diag("ok")]);
  await flush();

  assert.deepEqual(manager.getDiagnostics(MAIN), [diag("ok")]);
  assert.equal(manager.pendingRequestCount(), 0);
});

// ---------------------------------------------------------------------------
// Out-of-order background results
// ---------------------------------------------------------------------------

test("out-of-order: an older result arriving after a newer one is discarded", async () => {
  const computer = new ScriptedComputer();
  const manager = new DiagnosticManager(computer.compute);
  manager.open(MAIN, "v1");

  manager.requestDiagnostics(MAIN); // request 1 @ v1
  manager.change(MAIN, "v2"); // cancels request 1
  manager.requestDiagnostics(MAIN); // request 2 @ v2

  // Newer result lands first.
  computer.calls[1].deferred.resolve([diag("new")]);
  await flush();
  assert.deepEqual(manager.getDiagnostics(MAIN), [diag("new")]);

  // Stale result lands late and must not clobber the newer one.
  computer.calls[0].deferred.resolve([diag("stale")]);
  await flush();
  assert.deepEqual(manager.getDiagnostics(MAIN), [diag("new")]);
});

test("out-of-order: an older result arriving after an edit is discarded", async () => {
  const computer = new ScriptedComputer();
  const manager = new DiagnosticManager(computer.compute);
  manager.open(MAIN, "v1");

  manager.requestDiagnostics(MAIN); // request 1 @ v1
  manager.change(MAIN, "v2");
  manager.requestDiagnostics(MAIN); // request 2 @ v2

  // Older result lands first this time; it is already stale.
  computer.calls[0].deferred.resolve([diag("stale")]);
  await flush();
  assert.deepEqual(manager.getDiagnostics(MAIN), []);

  computer.calls[1].deferred.resolve([diag("new")]);
  await flush();
  assert.deepEqual(manager.getDiagnostics(MAIN), [diag("new")]);
});

// ---------------------------------------------------------------------------
// Multi-file version vector: single-file version equality is not enough
// ---------------------------------------------------------------------------

test("a result is rejected when a dependency changed, even if the primary file is untouched", async () => {
  const computer = new ScriptedComputer();
  const manager = new DiagnosticManager(computer.compute);
  manager.open(MAIN, "import './dep';");
  manager.open(DEP, "export const x = 1;");

  manager.requestDiagnostics(MAIN);
  computer.last().context.read(DEP); // records DEP @ v1 into the vector

  manager.change(DEP, "export const x = 2;"); // MAIN's version never changes

  computer.last().deferred.resolve([diag("stale")]);
  await flush();

  assert.deepEqual(manager.getDiagnostics(MAIN), []);
});

test("published diagnostics are invalidated when a dependency changes", async () => {
  const computer = new ScriptedComputer();
  const events: DiagnosticsChangedEvent[] = [];
  const manager = new DiagnosticManager(computer.compute);
  manager.onDidChangeDiagnostics((e) => events.push(e));
  manager.open(MAIN, "import './dep';");
  manager.open(DEP, "export const x = 1;");

  manager.requestDiagnostics(MAIN);
  computer.last().context.read(DEP);
  computer.last().deferred.resolve([diag("from-dep-v1")]);
  await flush();
  assert.deepEqual(manager.getDiagnostics(MAIN), [diag("from-dep-v1")]);

  manager.change(DEP, "export const x = 2;");
  assert.deepEqual(manager.getDiagnostics(MAIN), []);
  assert.ok(
    events.some((e) => e.uri === MAIN && e.diagnostics.length === 0),
    "expected an invalidation event for MAIN",
  );
});

test("published diagnostics survive changes to unrelated files", async () => {
  const computer = new ScriptedComputer();
  const manager = new DiagnosticManager(computer.compute);
  manager.open(MAIN, "import './dep';");
  manager.open(DEP, "export const x = 1;");
  manager.open(OTHER, "unrelated");

  manager.requestDiagnostics(MAIN);
  computer.last().context.read(DEP);
  computer.last().deferred.resolve([diag("kept")]);
  await flush();

  manager.change(OTHER, "edited");
  manager.close(OTHER);
  assert.deepEqual(manager.getDiagnostics(MAIN), [diag("kept")]);
});

// ---------------------------------------------------------------------------
// Close / reopen
// ---------------------------------------------------------------------------

test("close/reopen with identical content and dependencies reuses cached diagnostics", async () => {
  const computer = new ScriptedComputer();
  const manager = new DiagnosticManager(computer.compute);
  manager.open(MAIN, "import './dep';");
  manager.open(DEP, "export const x = 1;");

  manager.requestDiagnostics(MAIN);
  computer.last().context.read(DEP);
  computer.last().deferred.resolve([diag("cached")]);
  await flush();
  assert.equal(computer.count, 1);

  manager.close(MAIN);
  assert.deepEqual(manager.getDiagnostics(MAIN), []); // not displayed while closed

  manager.open(MAIN, "import './dep';"); // same content -> same stamp
  manager.requestDiagnostics(MAIN);
  await flush();

  assert.equal(computer.count, 1, "no recomputation expected");
  assert.deepEqual(manager.getDiagnostics(MAIN), [diag("cached")]);
});

test("close/reopen with different content forces recomputation", async () => {
  const computer = new ScriptedComputer();
  const manager = new DiagnosticManager(computer.compute);
  manager.open(MAIN, "v1");

  manager.requestDiagnostics(MAIN);
  computer.last().deferred.resolve([diag("old")]);
  await flush();

  manager.close(MAIN);
  manager.open(MAIN, "v2"); // same version number (1), different content
  manager.requestDiagnostics(MAIN);

  assert.equal(computer.count, 2, "cache must not be reused");
  computer.last().deferred.resolve([diag("fresh")]);
  await flush();
  assert.deepEqual(manager.getDiagnostics(MAIN), [diag("fresh")]);
});

test("close/reopen is not reusable when a dependency drifted in between", async () => {
  const computer = new ScriptedComputer();
  const manager = new DiagnosticManager(computer.compute);
  manager.open(MAIN, "import './dep';");
  manager.open(DEP, "export const x = 1;");

  manager.requestDiagnostics(MAIN);
  computer.last().context.read(DEP);
  computer.last().deferred.resolve([diag("old")]);
  await flush();

  manager.close(MAIN);
  manager.change(DEP, "export const x = 2;");
  manager.open(MAIN, "import './dep';"); // identical content as before
  manager.requestDiagnostics(MAIN);

  assert.equal(computer.count, 2, "dependency drift must defeat reuse");
});

test("a stale result computed before close is rejected after reopen, even with identical version numbers", async () => {
  const computer = new ScriptedComputer();
  const manager = new DiagnosticManager(computer.compute);
  manager.open(MAIN, "content-A"); // MAIN @ version 1

  manager.requestDiagnostics(MAIN); // vector: MAIN @ {version: 1, hash(A)}
  manager.close(MAIN);
  manager.open(MAIN, "content-B"); // MAIN @ version 1 again, different hash

  computer.last().deferred.resolve([diag("stale")]);
  await flush();

  assert.deepEqual(manager.getDiagnostics(MAIN), []);
});

// ---------------------------------------------------------------------------
// Dependency deletion
// ---------------------------------------------------------------------------

test("deleting a dependency invalidates published diagnostics and cancels pending requests", async () => {
  const computer = new ScriptedComputer();
  const events: DiagnosticsChangedEvent[] = [];
  const manager = new DiagnosticManager(computer.compute);
  manager.onDidChangeDiagnostics((e) => events.push(e));
  manager.open(MAIN, "import './dep';");
  manager.open(DEP, "export const x = 1;");

  manager.requestDiagnostics(MAIN);
  computer.last().context.read(DEP);
  computer.last().deferred.resolve([diag("needs-dep")]);
  await flush();
  assert.deepEqual(manager.getDiagnostics(MAIN), [diag("needs-dep")]);

  manager.deleteFile(DEP);
  assert.deepEqual(manager.getDiagnostics(MAIN), []);
  assert.ok(events.some((e) => e.uri === MAIN && e.diagnostics.length === 0));
});

test("a pending result that read a now-deleted dependency is discarded", async () => {
  const computer = new ScriptedComputer();
  const manager = new DiagnosticManager(computer.compute);
  manager.open(MAIN, "import './dep';");
  manager.open(DEP, "export const x = 1;");

  manager.requestDiagnostics(MAIN);
  computer.last().context.read(DEP);
  manager.deleteFile(DEP);

  computer.last().deferred.resolve([diag("stale")]);
  await flush();
  assert.deepEqual(manager.getDiagnostics(MAIN), []);
});

test("a dependency deleted and restored with identical content keeps the result reusable", async () => {
  const computer = new ScriptedComputer();
  const manager = new DiagnosticManager(computer.compute);
  manager.open(MAIN, "import './dep';");
  manager.open(DEP, "export const x = 1;");

  manager.requestDiagnostics(MAIN);
  computer.last().context.read(DEP);
  computer.last().deferred.resolve([diag("cached")]);
  await flush();

  manager.close(MAIN);
  manager.deleteFile(DEP);
  manager.open(DEP, "export const x = 1;"); // identical content restored
  manager.open(MAIN, "import './dep';");
  manager.requestDiagnostics(MAIN);
  await flush();

  assert.equal(computer.count, 1, "vector matches again: reuse is exact");
  assert.deepEqual(manager.getDiagnostics(MAIN), [diag("cached")]);
});

// ---------------------------------------------------------------------------
// Cancellation
// ---------------------------------------------------------------------------

test("editing a file cancels its pending request token", async () => {
  const computer = new ScriptedComputer();
  const manager = new DiagnosticManager(computer.compute);
  manager.open(MAIN, "v1");

  manager.requestDiagnostics(MAIN);
  const token = computer.last().context.token;
  assert.equal(token.cancelled, false);

  manager.change(MAIN, "v2");
  assert.equal(token.cancelled, true);
});

test("a failed cancellation does not save a stale result", async () => {
  // Computer ignores the cancellation token entirely and resolves anyway.
  const computer = new ScriptedComputer();
  const manager = new DiagnosticManager(computer.compute);
  manager.open(MAIN, "v1");

  manager.requestDiagnostics(MAIN); // request 1 @ v1
  manager.change(MAIN, "v2"); // tries to cancel request 1
  manager.requestDiagnostics(MAIN); // request 2 @ v2

  computer.calls[1].deferred.resolve([diag("current")]);
  await flush();

  // Cancellation "failed": the stale computation still produces a result.
  assert.equal(computer.calls[0].context.token.cancelled, true);
  computer.calls[0].deferred.resolve([diag("zombie")]);
  await flush();

  assert.deepEqual(manager.getDiagnostics(MAIN), [diag("current")]);
});

test("a computer failure settles the request without publishing", async () => {
  const computer = new ScriptedComputer();
  const manager = new DiagnosticManager(computer.compute);
  manager.open(MAIN, "v1");

  manager.requestDiagnostics(MAIN);
  computer.last().deferred.reject(new Error("language server crashed"));
  await flush();

  assert.equal(manager.pendingRequestCount(), 0);
  assert.deepEqual(manager.getDiagnostics(MAIN), []);
});

// ---------------------------------------------------------------------------
// vectorMatches unit tests
// ---------------------------------------------------------------------------

test("vectorMatches: exact conditions", () => {
  const stamp: FileStamp = { version: 3, contentHash: "abc" };
  const env = {
    files: new Map<string, FileStamp>([[MAIN, stamp]]),
    currentStamp(uri: string) {
      return this.files.get(uri);
    },
  };

  assert.equal(vectorMatches(new Map([[MAIN, stamp]]), env), true);
  assert.equal(
    vectorMatches(new Map([[MAIN, { version: 3, contentHash: "xyz" }]]), env),
    false,
    "same version, different content must not match",
  );
  assert.equal(
    vectorMatches(new Map([[MAIN, { version: 4, contentHash: "abc" }]]), env),
    false,
    "same content, different version must not match",
  );
  assert.equal(
    vectorMatches(new Map([[MAIN, stamp], [DEP, stamp]]), env),
    false,
    "recorded dependency must be present",
  );
  assert.equal(
    vectorMatches(new Map([[MAIN, stamp], [DEP, null]]), env),
    true,
    "absent dependency recorded as null matches while still absent",
  );

  env.files.set(DEP, stamp);
  assert.equal(
    vectorMatches(new Map([[MAIN, stamp], [DEP, null]]), env),
    false,
    "a dependency that appeared since compute time invalidates the vector",
  );
});
