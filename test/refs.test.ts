import { test } from "node:test";
import assert from "node:assert/strict";
import { ConfigStore, ConfigError, RefCycleError } from "../src/index.ts";
import { assertMatchesScratch } from "./helpers.ts";

test("pure cross-field reference resolves to the target value", () => {
  const store = new ConfigStore();
  store.commit({
    kind: "upsertLayer",
    layer: { id: "a", priority: 0, data: {
      server: { host: "h1", port: 80 },
      client: { endpoint: { $ref: "#/server/host" }, opts: { again: { $ref: "#/server" } } },
    } },
  });
  const cfg = store.getSnapshot().config as any;
  assert.equal(cfg.client.endpoint, "h1");
  assert.deepStrictEqual(cfg.client.opts.again, { host: "h1", port: 80 });
  const chain = store.explainAt(["client", "endpoint"]);
  const ref = chain.find((e) => e.kind === "ref");
  assert.ok(ref && ref.kind === "ref");
  assert.deepStrictEqual(ref.target, ["server", "host"]);
  assert.deepStrictEqual(ref.resolvedFrom, ["client", "endpoint"]);
});

test("chained refs resolve transitively", () => {
  const store = new ConfigStore();
  store.commit({
    kind: "upsertLayer",
    layer: { id: "a", priority: 0, data: {
      v: 42,
      b: { $ref: "#/v" },
      c: { $ref: "#/b" },
      d: { wrap: { $ref: "#/c" } },
    } },
  });
  const cfg = store.getSnapshot().config as any;
  assert.equal(cfg.c, 42);
  assert.equal(cfg.d.wrap, 42);
});

test("ref cycles are detected and reported with the full cycle chain", () => {
  const store = new ConfigStore();
  assert.throws(
    () =>
      store.commit({
        kind: "upsertLayer",
        layer: { id: "a", priority: 0, data: {
          x: { $ref: "#/y" },
          y: { next: { $ref: "#/z" } },
          z: { $ref: "#/x" },
        } },
      }),
    (err: unknown) => {
      assert.ok(err instanceof RefCycleError);
      assert.equal(err.code, "REF_CYCLE");
      const flat = err.cycle.map((p) => "/" + p.join("/"));
      assert.deepStrictEqual(flat, ["/x", "/y/next", "/z", "/x"]);
      return true;
    },
  );
  assert.equal(store.getSnapshot().version, 0, "failed commit publishes nothing");
});

test("dangling ref target is an error locating the ref", () => {
  const store = new ConfigStore();
  assert.throws(
    () => store.commit({ kind: "upsertLayer", layer: { id: "a", priority: 0, data: { r: { $ref: "#/nope" } } } }),
    (err: unknown) => {
      assert.ok(err instanceof ConfigError);
      assert.equal(err.code, "REF_NOT_FOUND");
      assert.deepStrictEqual(err.path, ["r"]);
      assert.deepStrictEqual(err.details, { target: ["nope"] });
      return true;
    },
  );
});

test("disabling a layer re-resolves references: no stale values survive", () => {
  const store = new ConfigStore();
  store.commit([
    { kind: "upsertLayer", layer: { id: "base", priority: 0, data: { link: { $ref: "#/extra/value" } } } },
    { kind: "upsertLayer", layer: { id: "opt", priority: 1, data: { extra: { value: "from-opt" } } } },
  ]);
  assert.equal((store.getSnapshot().config as any).link, "from-opt");

  // disabling opt would leave the ref dangling -> the commit is rejected
  assert.throws(
    () => store.commit({ kind: "setEnabled", id: "opt", enabled: false }),
    (err: unknown) => err instanceof ConfigError && err.code === "REF_NOT_FOUND",
  );
  assert.equal((store.getSnapshot().config as any).link, "from-opt", "old config keeps serving");

  // point the ref elsewhere, then disabling works and nothing stale remains
  store.commit({ kind: "setLayerData", id: "base", data: { link: { $ref: "#/base/value" }, base: { value: "local" } } });
  store.commit({ kind: "setEnabled", id: "opt", enabled: false });
  const cfg = store.getSnapshot().config as any;
  assert.equal(cfg.link, "local");
  assert.ok(!("extra" in cfg), "disabled layer contributes nothing");
  const refEntry = store.explainAt(["link"]).find((e) => e.kind === "ref");
  assert.ok(refEntry && refEntry.kind === "ref");
  assert.deepStrictEqual(refEntry.target, ["base", "value"], "ref dependency updated, not stale");
  assertMatchesScratch(store, []);
});

test("conditional layer enablement via context flips reference dependencies", () => {
  const store = new ConfigStore();
  store.commit([
    { kind: "upsertLayer", layer: { id: "base", priority: 0, data: { mode: "std", picked: { $ref: "#/modes/std" }, modes: { std: "S", beta: "B" } } } },
    { kind: "upsertLayer", layer: { id: "beta", priority: 1, when: { path: "flags.beta", equals: true }, data: {
      modes: { beta: "B+" },
      picked: { $ref: "#/modes/beta" },
    } } },
  ]);
  assert.equal((store.getSnapshot().config as any).picked, "S", "beta layer off: base ref wins");
  store.commit({ kind: "setContext", context: { flags: { beta: true } } });
  assert.equal((store.getSnapshot().config as any).picked, "B+", "beta layer on: its ref overrides");
  store.commit({ kind: "setContext", context: { flags: { beta: false } } });
  assert.equal((store.getSnapshot().config as any).picked, "S");
  assertMatchesScratch(store, []);
});
