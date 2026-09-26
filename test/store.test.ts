import { test } from "node:test";
import assert from "node:assert/strict";
import {
  LayeredConfigStore,
  MergeTypeConflictError,
  byId,
} from "../src/index.ts";
import type { Snapshot } from "../src/index.ts";
import { capture, assertInternallyConsistent } from "./helpers.ts";

test("publishes are atomic: subscribers only see consistent snapshots", () => {
  const s = new LayeredConfigStore({
    strategies: [{ pattern: ["plugins"], strategy: byId("name") }],
  });
  const seen: Snapshot[] = [];
  s.subscribe((snap) => {
    // Inside the notification the store must already expose this snapshot.
    assert.equal(s.snapshot(), snap);
    assertInternallyConsistent(snap);
    seen.push(snap);
  });

  s.addLayer({ id: "base", data: { plugins: [{ name: "x", v: 1 }], a: 1 } });
  s.addLayer({ id: "app", data: { a: 2 } });
  assert.deepEqual(seen.map((x) => x.version), [1, 2]);

  // A failing commit must not notify anyone nor move the version.
  capture(
    () => s.addLayer({ id: "bad", data: { plugins: ["x"] } }),
    MergeTypeConflictError,
  );
  assert.equal(seen.length, 2);
  assert.equal(s.snapshot().version, 2);
});

test("snapshots are deeply frozen and history is kept", () => {
  const s = new LayeredConfigStore();
  s.addLayer({ id: "base", data: { a: 1, nested: { b: 2 } } });
  const v1 = s.snapshot();
  s.addLayer({ id: "app", data: { a: 2 } });
  const v2 = s.snapshot();

  assert.equal(s.snapshotAt(1), v1);
  assert.equal(s.snapshotAt(2), v2);
  assert.equal(s.snapshotAt(99), undefined);
  assert.deepEqual(s.versions(), [0, 1, 2]);

  assert.throws(() => {
    (v1.config as Record<string, unknown>).a = 99;
  }, TypeError);
  assert.throws(() => {
    ((v1.config as { nested: Record<string, unknown> }).nested).b = 99;
  }, TypeError);
});

test("concurrent readers keep their own consistent view", () => {
  const s = new LayeredConfigStore();
  s.addLayer({ id: "base", data: { a: 1 } });
  const reader1 = s.snapshot();

  s.addLayer({ id: "app", data: { a: 2, b: 3 } });
  const reader2 = s.snapshot();

  s.updateLayerData("base", { a: 10, c: 4 });

  // The older snapshots are unaffected by later publishes.
  assert.deepEqual(reader1.config, { a: 1 });
  assert.deepEqual(reader2.config, { a: 2, b: 3 });
  // base now says a=10, but the higher-priority app layer still wins with 2.
  assert.deepEqual(s.snapshot().config, { a: 2, b: 3, c: 4 });
  assert.equal(reader1.explain["/a"]?.value, 1);
  assert.equal(reader2.explain["/a"]?.value, 2);
  assert.equal(s.snapshot().explain["/a"]?.value, 2);
  assert.deepEqual(
    s.snapshot().explain["/a"]?.contributions.map((c) => [c.layerId, c.value]),
    [["base", 10], ["app", 2]],
  );
});

test("conditional layers follow the context", () => {
  const s = new LayeredConfigStore({ context: { env: "dev" } });
  s.addLayer({ id: "base", data: { level: "info" } });
  s.addLayer({
    id: "prod",
    data: { level: "warn" },
    enabledWhen: (ctx) => ctx.env === "prod",
  });

  assert.deepEqual(s.listLayers(), [
    { id: "base", enabled: true },
    { id: "prod", enabled: false },
  ]);
  assert.equal((s.snapshot().config as { level: string }).level, "info");

  s.setContext({ env: "prod" });
  assert.equal((s.snapshot().config as { level: string }).level, "warn");
  assert.deepEqual(s.listLayers().map((l) => l.enabled), [true, true]);
});

test("swapping layer priority changes the winner", () => {
  const s = new LayeredConfigStore();
  s.addLayer({ id: "a", data: { x: "from-a" } });
  s.addLayer({ id: "b", data: { x: "from-b" } });
  assert.equal((s.snapshot().config as { x: string }).x, "from-b");

  s.swapLayers("a", "b");
  assert.equal((s.snapshot().config as { x: string }).x, "from-a");
  assert.equal(s.explainAt("/x")?.winner, "a");
});

test("rollback restores an earlier layer set as a new version", () => {
  const s = new LayeredConfigStore();
  s.addLayer({ id: "base", data: { a: 1 } });
  const v1 = s.snapshot();
  s.addLayer({ id: "app", data: { b: 2 } });
  s.updateLayerData("base", { a: 10 });
  assert.deepEqual(s.snapshot().config, { a: 10, b: 2 });

  const rolled = s.rollback(v1.version);
  assert.ok(rolled.version > 3, "rollback publishes a new version");
  assert.deepEqual(rolled.config, v1.config);
  assert.deepEqual(s.listLayers(), [{ id: "base", enabled: true }]);

  // History is append-only: intermediate versions remain available.
  assert.equal(s.snapshotAt(3)?.config && (s.snapshotAt(3)!.config as { a: number }).a, 10);
});

test("unsubscribe stops notifications", () => {
  const s = new LayeredConfigStore();
  let count = 0;
  const off = s.subscribe(() => count++);
  s.addLayer({ id: "a", data: {} });
  off();
  s.addLayer({ id: "b", data: {} });
  assert.equal(count, 1);
});
