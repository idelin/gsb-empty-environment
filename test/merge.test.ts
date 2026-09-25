import { test } from "node:test";
import assert from "node:assert/strict";
import { ConfigStore, ConfigError, getPath, jsonEquals } from "../src/index.ts";
import { assertMatchesScratch } from "./helpers.ts";

const RULES = [
  { pattern: "/services", strategy: "byId" as const },
  { pattern: "/features/*", strategy: "override" as const },
];

function newStore() {
  return new ConfigStore({ strategies: RULES });
}

test("deep merge is the default; object key order is insignificant", () => {
  const store = newStore();
  store.commit([
    { kind: "upsertLayer", layer: { id: "a", priority: 0, data: { s: { x: 1, y: { p: 1, q: 2 } } } } },
    { kind: "upsertLayer", layer: { id: "b", priority: 1, data: { s: { y: { q: 20, r: 3 }, z: 9 } } } },
  ]);
  const cfg = store.getSnapshot().config as any;
  assert.deepStrictEqual(cfg.s, { x: 1, y: { p: 1, q: 20, r: 3 }, z: 9 });
  // key order must not matter
  assert.ok(jsonEquals({ a: 1, b: [1, 2] }, { b: [1, 2], a: 1 }));
  assert.ok(!jsonEquals({ a: [1, 2] }, { a: [2, 1] }), "array order matters");
  assertMatchesScratch(store, RULES);
});

test("override strategy replaces the whole field", () => {
  const store = newStore();
  store.commit([
    { kind: "upsertLayer", layer: { id: "a", priority: 0, data: { features: { ui: { a: 1, b: 2 } } } } },
    { kind: "upsertLayer", layer: { id: "b", priority: 1, data: { features: { ui: { c: 3 } } } } },
  ]);
  const cfg = store.getSnapshot().config as any;
  assert.deepStrictEqual(cfg.features.ui, { c: 3 }, "override discards lower-layer keys");
  assertMatchesScratch(store, RULES);
});

test("delete sentinel removes a field; null is a real value", () => {
  const store = newStore();
  store.commit([
    { kind: "upsertLayer", layer: { id: "a", priority: 0, data: { keep: 1, gone: 2, nul: 3 } } },
    { kind: "upsertLayer", layer: { id: "b", priority: 1, data: { gone: { $delete: true }, nul: null } } },
  ]);
  const cfg = store.getSnapshot().config as any;
  assert.equal(cfg.keep, 1);
  assert.ok(!("gone" in cfg), "sentinel removes the key");
  assert.ok("nul" in cfg && cfg.nul === null, "null survives as a value");
  assertMatchesScratch(store, RULES);
});

test("byId: set-union by id, per-item deep merge, sentinel removes an item", () => {
  const store = newStore();
  store.commit([
    { kind: "upsertLayer", layer: { id: "a", priority: 0, data: { services: [
      { id: "web", type: "http", weight: 1 },
      { id: "db", type: "tcp" },
    ] } } },
    { kind: "upsertLayer", layer: { id: "b", priority: 1, data: { services: [
      { id: "web", weight: 5 },
      { id: "cache", type: "tcp" },
      { id: "db", $delete: true },
    ] } } },
  ]);
  const cfg = store.getSnapshot().config as any;
  assert.deepStrictEqual(cfg.services, [
    { id: "web", type: "http", weight: 5 },
    { id: "cache", type: "tcp" },
  ]);
  // provenance at the array field records both layers and the item ids
  const entries = store.explainAt(["services"]);
  assert.deepStrictEqual(entries.map((e) => (e.kind === "layer" ? `${e.layer}:${e.op}:${e.id ?? ""}` : "ref")), [
    "a:byId:web", "a:byId:db", "b:byId:web", "b:byId:cache", "b:delete:db",
  ]);
  assertMatchesScratch(store, RULES);
});

test("byId: item without id is an error locating the exact path", () => {
  const store = newStore();
  store.commit({ kind: "upsertLayer", layer: { id: "a", priority: 0, data: { services: [] } } });
  assert.throws(
    () => store.commit({ kind: "upsertLayer", layer: { id: "b", priority: 1, data: { services: [{ weight: 1 }] } } }),
    (err: unknown) => {
      assert.ok(err instanceof ConfigError);
      assert.equal(err.code, "MISSING_ID");
      assert.deepStrictEqual(err.path, ["services", 0]);
      assert.equal(err.layer, "b");
      return true;
    },
  );
});

test("byId: same-id type conflict rejects the whole batch; old config keeps serving", () => {
  const store = newStore();
  const before = store.commit([
    { kind: "upsertLayer", layer: { id: "a", priority: 0, data: { services: [{ id: "web", type: "http" }], v: 1 } } },
  ]);
  assert.throws(
    () =>
      store.commit([
        { kind: "upsertLayer", layer: { id: "bad", priority: 1, data: { services: [{ id: "web", type: "grpc" }] } } },
        { kind: "upsertLayer", layer: { id: "other", priority: 2, data: { v: 2 } } },
      ]),
    (err: unknown) => {
      assert.ok(err instanceof ConfigError);
      assert.equal(err.code, "TYPE_CONFLICT");
      assert.deepStrictEqual(err.path, ["services"]);
      assert.deepStrictEqual(err.details, { id: "web", existingType: "http", incomingType: "grpc" });
      return true;
    },
  );
  const after = store.getSnapshot();
  assert.equal(after.version, before.version, "rejected batch must not publish");
  assert.equal((after.config as any).v, 1, "other ops in the rejected batch must not apply");
  assert.deepStrictEqual((after.config as any).services, [{ id: "web", type: "http" }]);
  // store still works afterwards
  store.commit({ kind: "upsertLayer", layer: { id: "ok", priority: 3, data: { v: 2 } } });
  assert.equal((store.getSnapshot().config as any).v, 2);
  assertMatchesScratch(store, RULES);
});

test("errors and provenance locate concrete paths", () => {
  const store = newStore();
  store.commit([
    { kind: "upsertLayer", layer: { id: "a", priority: 0, data: { s: { p: 1 } } } },
    { kind: "upsertLayer", layer: { id: "b", priority: 1, data: { s: { p: 2 } } } },
  ]);
  const chain = store.explainAt(["s", "p"]);
  assert.equal(chain.length, 2);
  assert.deepStrictEqual(chain[0], { kind: "layer", layer: "a", priority: 0, op: "set", path: ["s", "p"], value: 1 });
  assert.deepStrictEqual(chain[1], { kind: "layer", layer: "b", priority: 1, op: "set", path: ["s", "p"], value: 2 });
});
