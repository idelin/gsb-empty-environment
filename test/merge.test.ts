import { test } from "node:test";
import assert from "node:assert/strict";
import {
  LayeredConfigStore,
  DELETE,
  OVERRIDE,
  byId,
  MergeTypeConflictError,
  FieldValidationError,
} from "../src/index.ts";
import { capture } from "./helpers.ts";


test("deep merge is the default and keeps a full provenance chain", () => {
  const s = new LayeredConfigStore();
  s.addLayer({ id: "base", data: { db: { host: "a", port: 1 }, ui: { theme: "light" } } });
  s.addLayer({ id: "app", data: { db: { port: 2, user: "x" } } });
  s.addLayer({ id: "env", data: { db: { port: 3 } } });

  assert.deepEqual(s.snapshot().config, {
    db: { host: "a", port: 3, user: "x" },
    ui: { theme: "light" },
  });

  const rec = s.explainAt("/db/port");
  assert.ok(rec);
  assert.equal(rec.strategy, "deep");
  assert.deepEqual(
    rec.contributions.map((c) => [c.layerId, c.kind, c.value]),
    [
      ["base", "value", 1],
      ["app", "value", 2],
      ["env", "value", 3],
    ],
  );
  assert.equal(rec.winner, "env");
  assert.equal(rec.value, 3);
  // Provenance entries carry structured paths, not formatted strings.
  assert.deepEqual(rec.contributions[0].path, ["db", "port"]);
});

test("override strategy replaces the whole field", () => {
  const s = new LayeredConfigStore({
    strategies: [{ pattern: ["list"], strategy: OVERRIDE }],
  });
  s.addLayer({ id: "base", data: { list: [1, 2, 3], x: 1 } });
  s.addLayer({ id: "app", data: { list: [9], x: 2 } });

  assert.deepEqual(s.snapshot().config, { list: [9], x: 2 });
  const rec = s.explainAt("/list");
  assert.equal(rec?.strategy, "override");
  assert.equal(rec?.winner, "app");
  assert.equal(rec?.contributions.length, 2);
});

test("delete sentinel removes a key; null is an ordinary value", () => {
  const s = new LayeredConfigStore();
  s.addLayer({ id: "base", data: { a: 1, b: 2, c: { d: 3 } } });
  s.addLayer({ id: "over", data: { a: DELETE, b: null, c: DELETE } });

  const cfg = s.snapshot().config as Record<string, unknown>;
  assert.equal(Object.hasOwn(cfg, "a"), false, "sentinel deletes the key");
  assert.equal(Object.hasOwn(cfg, "b"), true, "null keeps the key");
  assert.equal(cfg.b, null);
  assert.equal(Object.hasOwn(cfg, "c"), false, "sentinel deletes a subtree");

  const rec = s.explainAt("/a");
  assert.equal(rec?.present, false);
  assert.deepEqual(
    rec?.contributions.map((c) => c.kind),
    ["value", "delete"],
  );
});

test("a lower delete is masked again by a higher concrete value", () => {
  const s = new LayeredConfigStore();
  s.addLayer({ id: "l1", data: { a: 1 } });
  s.addLayer({ id: "l2", data: { a: DELETE } });
  s.addLayer({ id: "l3", data: { a: 5 } });
  assert.equal((s.snapshot().config as { a: number }).a, 5);
});

test("byId unions array items by id, deep-merges duplicates, keeps order", () => {
  const s = new LayeredConfigStore({
    strategies: [{ pattern: ["plugins"], strategy: byId("name") }],
  });
  s.addLayer({
    id: "base",
    data: { plugins: [{ name: "auth", v: 1 }, { name: "log", v: 2 }, "extra"] },
  });
  s.addLayer({
    id: "app",
    data: { plugins: [{ name: "log", v: 20, opts: { level: "i" } }, { name: "cache" }] },
  });
  s.addLayer({ id: "env", data: { plugins: [{ name: "log", opts: { json: true } }] } });

  const cfg = s.snapshot().config as { plugins: unknown[] };
  // Order = first appearance across layers (low -> high priority).
  assert.deepEqual(cfg.plugins, [
    { name: "auth", v: 1 },
    { name: "log", v: 20, opts: { level: "i", json: true } },
    "extra",
    { name: "cache" },
  ]);

  const rec = s.explainAt("/plugins");
  assert.equal(rec?.strategy, "byId");
  assert.deepEqual(
    rec?.contributions
      .filter((c) => c.kind === "merge")
      .map((c) => c.layerId),
    ["app", "env"],
  );
});

test("byId supports item-level delete and whole-field delete", () => {
  const s = new LayeredConfigStore({
    strategies: [{ pattern: ["plugins"], strategy: byId("name") }],
  });
  s.addLayer({ id: "base", data: { plugins: [{ name: "a" }, { name: "b" }] } });
  s.addLayer({ id: "app", data: { plugins: [{ name: "a", $delete: true }] } });
  assert.deepEqual((s.snapshot().config as { plugins: unknown[] }).plugins, [{ name: "b" }]);

  s.addLayer({ id: "wipe", data: { plugins: DELETE } });
  assert.equal(Object.hasOwn(s.snapshot().config as object, "plugins"), false);
});

test("same id with conflicting item types rejects the whole batch", () => {
  const s = new LayeredConfigStore({
    strategies: [{ pattern: ["plugins"], strategy: byId("name") }],
  });
  s.addLayer({ id: "base", data: { plugins: [{ name: "x", v: 1 }], keep: true } });
  const before = s.snapshot();

  const err = capture(
    () => s.addLayer({ id: "bad", data: { plugins: ["x"] } }),
    MergeTypeConflictError,
  );
  assert.equal(err.pointer, "/plugins/0");
  assert.equal(err.layerId, "bad");

  // The whole commit was rejected: old config is still the published one.
  assert.equal(s.snapshot().version, before.version);
  assert.equal(s.snapshot(), before);
  assert.deepEqual((s.snapshot().config as { plugins: unknown[] }).plugins, [
    { name: "x", v: 1 },
  ]);
});

test("byId validates item shape and reports the exact path", () => {
  const s = new LayeredConfigStore({
    strategies: [{ pattern: ["plugins"], strategy: byId("name") }],
  });
  s.addLayer({ id: "base", data: { plugins: [{ name: "x" }] } });
  const err = capture(
    () => s.addLayer({ id: "bad", data: { plugins: [{ noname: 1 }] } }),
    FieldValidationError,
  );
  assert.equal(err.pointer, "/plugins/0");
  assert.equal(err.layerId, "bad");
});

test("object key order is irrelevant, array order is significant", () => {
  const a = new LayeredConfigStore();
  a.addLayer({ id: "l", data: { x: 1, nested: { p: 1, q: 2 } } });

  const b = new LayeredConfigStore();
  b.addLayer({ id: "l", data: { nested: { q: 2, p: 1 }, x: 1 } });

  assert.deepEqual(a.snapshot().config, b.snapshot().config);

  const c = new LayeredConfigStore();
  c.addLayer({ id: "l", data: { arr: [1, 2] } });
  const d = new LayeredConfigStore();
  d.addLayer({ id: "l", data: { arr: [2, 1] } });
  assert.notDeepEqual(c.snapshot().config, d.snapshot().config);
});
