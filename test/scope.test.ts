import { test } from "node:test";
import assert from "node:assert/strict";
import {
  AcquireCancelledError,
  DependencyCycleError,
  Ledger,
  ResourceReleaseError,
  Scheduler,
  Scope,
  ScopeCloseError,
  ScopeClosedError,
} from "../src/index.ts";
import type { Handle, ScopeEvent } from "../src/index.ts";

function make() {
  const scheduler = new Scheduler();
  const ledger = new Ledger();
  const scope = new Scope({ id: "s", scheduler, ledger });
  const released: string[] = [];
  const spec = (
    name: string,
    opts: { deps?: Handle<unknown>[]; failRelease?: boolean } = {},
  ) => ({
    name,
    deps: opts.deps,
    acquire: () => `value:${name}`,
    release: () => {
      released.push(name);
      if (opts.failRelease) throw new Error(`${name}-release-failed`);
    },
  });
  return { scheduler, ledger, scope, released, spec };
}

test("releases in reverse dependency order, LIFO for ties", async () => {
  const { scheduler, ledger, scope, released, spec } = make();
  const a = scope.acquire(spec("a"));
  const b = scope.acquire(spec("b", { deps: [a] }));
  const c = scope.acquire(spec("c", { deps: [b] }));
  scope.acquire(spec("d"));
  await scheduler.runUntilIdle();
  await scope.close();
  assert.deepEqual(released, ["d", "c", "b", "a"]);
  ledger.assertExactOnce();
});

test("release failures do not stop the sweep; all errors are aggregated", async () => {
  const { scheduler, ledger, scope, released, spec } = make();
  const a = scope.acquire(spec("a"));
  const b = scope.acquire(spec("b", { deps: [a], failRelease: true }));
  const c = scope.acquire(spec("c", { deps: [b], failRelease: true }));
  scope.acquire(spec("d"));
  await scheduler.runUntilIdle();

  const err = await scope.close().then(
    () => {
      throw new Error("close should have rejected");
    },
    (e: unknown) => e,
  );
  assert.ok(err instanceof ScopeCloseError);
  assert.equal(err.errors.length, 2);
  assert.ok(err.errors.every((e) => e instanceof ResourceReleaseError));
  // Every resource was still released (attempted) exactly once, in order.
  assert.deepEqual(released, ["d", "c", "b", "a"]);
  assert.equal(scope.state.resources[c.id].status, "release-failed");
  assert.equal(scope.state.resources[a.id].status, "released");
  ledger.assertExactOnce();
});

test("close is idempotent: same promise, single release per resource", async () => {
  const { scheduler, ledger, scope, released, spec } = make();
  scope.acquire(spec("a"));
  scope.acquire(spec("b"));
  await scheduler.runUntilIdle();
  const first = scope.close();
  const second = scope.close();
  assert.strictEqual(first, second);
  await first;
  await scope.close();
  assert.deepEqual(released.sort(), ["a", "b"]);
  ledger.assertExactOnce();
});

test("late acquisition after close is returned immediately", async () => {
  const { scheduler, ledger, scope, released, spec } = make();
  const slow = scope.acquire(spec("slow"));
  const closing = scope.close();
  assert.equal(slow.status, "acquiring");
  // The value only materialises now, after closing began.
  await scheduler.runUntilIdle();
  await closing;
  assert.deepEqual(released, ["slow"]);
  assert.equal(slow.status, "returned-late");
  await assert.rejects(slow.value, ScopeClosedError);
  const entry = ledger.entry(slow.id);
  assert.deepEqual(
    { acquires: entry?.acquires, releases: entry?.releases },
    { acquires: 1, releases: 1 },
  );
  ledger.assertExactOnce();
});

test("cancelled acquisition returns the late value immediately", async () => {
  const { scheduler, ledger, scope, released, spec } = make();
  const pending = scope.acquire(spec("pending"));
  pending.cancel();
  await assert.rejects(pending.value, AcquireCancelledError);
  await scheduler.runUntilIdle();
  assert.deepEqual(released, ["pending"]);
  assert.equal(pending.status, "returned-late");
  await scope.close();
  ledger.assertExactOnce();
});

test("acquire during closing or after close is rejected immediately", async () => {
  const { scheduler, ledger, scope, spec } = make();
  scope.acquire(spec("a"));
  await scheduler.runUntilIdle();
  const closing = scope.close();
  assert.throws(() => scope.acquire(spec("late")), ScopeClosedError);
  await closing;
  assert.throws(() => scope.acquire(spec("later")), ScopeClosedError);
  ledger.assertExactOnce();
});

test("child close does not release parent's borrowed resource", async () => {
  const { scheduler, ledger, scope, released, spec } = make();
  const db = scope.acquire(spec("db"));
  await scheduler.runUntilIdle();

  const child = scope.fork("child");
  const borrowed = await child.borrow(db);
  assert.equal(borrowed, "value:db");
  assert.deepEqual(scope.state.resources[db.id].borrowedBy, ["child"]);

  await child.close();
  assert.deepEqual(released, []);
  assert.equal(db.status, "owned");

  await scope.close();
  assert.deepEqual(released, ["db"]);
  ledger.assertExactOnce();
});

test("parent close closes children first, then own resources", async () => {
  const { scheduler, ledger, scope, released, spec } = make();
  scope.acquire(spec("parent-res"));
  const child = scope.fork("child");
  child.acquire(spec("child-res"));
  await scheduler.runUntilIdle();
  await scope.close();
  assert.deepEqual(released, ["child-res", "parent-res"]);
  assert.equal(child.phase, "closed");
  ledger.assertExactOnce();
});

test("dependency cycle: deterministic order, aggregated cycle error, exactly-once", async () => {
  const run = async () => {
    const scheduler = new Scheduler();
    const ledger = new Ledger();
    const scope = new Scope({ id: "cyc", scheduler, ledger });
    const released: string[] = [];
    const mk = (name: string, deps: Handle<unknown>[] = []) =>
      scope.acquire({
        name,
        deps,
        acquire: () => name,
        release: () => {
          released.push(name);
        },
      });
    const a = mk("a");
    const b = mk("b", [a]);
    const c = mk("c", [b]);
    scope.addDependency(a, c); // cycle: a -> c -> b -> a
    await scheduler.runUntilIdle();
    const err: unknown = await scope.close().then(
      () => null,
      (e: unknown) => e,
    );
    return { released, err, ledger };
  };

  const first = await run();
  assert.ok(first.err instanceof ScopeCloseError);
  assert.equal(first.err.errors.length, 1);
  assert.ok(first.err.errors[0] instanceof DependencyCycleError);
  assert.deepEqual(first.err.errors[0].cycle, ["cyc:r1", "cyc:r3", "cyc:r2", "cyc:r1"]);
  // Deterministic: dependents-first DFS, back edge ignored, all released once.
  assert.deepEqual(first.released, ["a", "c", "b"]);
  first.ledger.assertExactOnce();

  const second = await run();
  assert.deepEqual(second.released, first.released);
});

test("scope state is replayable and subscribers only see committed states", async () => {
  const { scheduler, ledger, scope, spec } = make();
  const events: ScopeEvent[] = [];
  const off = scope.subscribe((event) => events.push(event));
  const a = scope.acquire(spec("a"));
  scope.acquire(spec("b", { deps: [a] }));
  await scheduler.runUntilIdle();
  await scope.close();
  off();

  // Every event carried a fully committed snapshot; statuses form a valid
  // lifecycle with no torn intermediate states.
  for (const event of events) {
    assert.ok(Object.isFrozen(event.state));
    for (const resource of Object.values(event.state.resources)) {
      assert.ok(
        [
          "acquiring",
          "owned",
          "releasing",
          "released",
          "release-failed",
          "acquire-failed",
          "returned-late",
        ].includes(resource.status),
      );
    }
  }
  assert.deepEqual(scope.store.replay(), scope.store.snapshot());
  assert.equal(scope.state.phase, "closed");
  ledger.assertExactOnce();
});

test("enumeration: acquire/borrow/cancel/double-close interleavings keep exactly-once", async (t) => {
  // For each scenario: pump k acquisitions, optionally cancel one, start
  // close (twice), then drain. The ledger must balance in every interleaving.
  const scenarios: unknown[] = [];
  for (let pumps = 0; pumps <= 3; pumps++) {
    for (const cancel of [-1, 0, 1, 2]) {
      for (const doubleClose of [false, true]) {
        scenarios.push({ pumps, cancel, doubleClose });
      }
    }
  }

  for (const { pumps, cancel, doubleClose } of scenarios as Array<{
    pumps: number;
    cancel: number;
    doubleClose: boolean;
  }>) {
    await t.test(
      `pumps=${pumps} cancel=${cancel} doubleClose=${doubleClose}`,
      async () => {
        const scheduler = new Scheduler();
        const ledger = new Ledger();
        const scope = new Scope({ id: "enum", scheduler, ledger });
        const released: string[] = [];
        const handles = [0, 1, 2].map((n) =>
          scope.acquire({
            name: `r${n}`,
            acquire: () => `v${n}`,
            release: () => {
              released.push(`r${n}`);
            },
          }),
        );
        const child = scope.fork("c");

        await scheduler.pump(pumps);
        if (cancel >= 0) handles[cancel].cancel();
        // Borrow whatever is already owned (no-op for still-acquiring ones).
        const borrows = handles
          .filter((h) => h.status === "owned")
          .map((h) => child.borrow(h).catch(() => {}));

        const first = scope.close();
        const second = scope.close();
        assert.strictEqual(first, second);
        await scheduler.runUntilIdle();
        await Promise.all(borrows);
        await first; // no release failures in this scenario
        assert.equal(scope.phase, "closed");
        assert.equal(child.phase, "closed");

        // Every materialised resource was released exactly once.
        ledger.assertExactOnce();
        for (const h of handles) {
          const entry = ledger.entry(h.id);
          if (h.status === "released" || h.status === "returned-late") {
            assert.deepEqual(
              { a: entry?.acquires, r: entry?.releases },
              { a: 1, r: 1 },
              `resource ${h.name} status=${h.status}`,
            );
          }
        }
      },
    );
  }
});
