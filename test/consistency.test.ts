import { test } from "node:test";
import assert from "node:assert/strict";
import {
  LayeredConfigStore,
  DELETE,
  OVERRIDE,
  byId,
  MergeTypeConflictError,
  RefResolutionError,
} from "../src/index.ts";
import type { StrategyRule } from "../src/index.ts";
import { capture, assertMatchesScratch } from "./helpers.ts";

const RULES: readonly StrategyRule[] = [
  { pattern: ["plugins"], strategy: byId("name") },
  { pattern: ["flags"], strategy: OVERRIDE },
];

function buildStore(): LayeredConfigStore {
  const s = new LayeredConfigStore({
    strategies: RULES,
    context: { env: "dev", beta: false },
  });
  s.addLayer({
    id: "base",
    data: {
      db: { host: "db.dev", port: 5432 },
      ui: { theme: "light", lang: "en" },
      flags: ["f1"],
      plugins: [{ name: "auth", v: 1 }, { name: "log", v: 2 }],
      url: { $ref: "#/db/host" },
    },
  });
  s.addLayer({
    id: "app",
    data: {
      db: { port: 15432 },
      plugins: [{ name: "log", level: "debug" }, { name: "cache" }],
    },
  });
  s.addLayer({
    id: "prod",
    data: { db: { host: "db.prod" }, ui: { theme: "dark" } },
    enabledWhen: (ctx) => ctx.env === "prod",
  });
  s.addLayer({
    id: "beta",
    data: { flags: ["f1", "f2"], extra: { $ref: "#/ui/theme" } },
    enabledWhen: (ctx) => ctx.beta === true,
  });
  return s;
}

test("every operation matches a from-scratch merge", () => {
  const s = buildStore();
  assertMatchesScratch(s, RULES);

  // Hot updates of single layers.
  s.updateLayerData("app", {
    db: { port: 25432 },
    plugins: [{ name: "log", level: "info" }, { name: "cache" }, { name: "new" }],
  });
  assertMatchesScratch(s, RULES);

  s.updateLayerData("base", {
    db: { host: "db.dev2", port: 5432 },
    ui: { theme: "light", lang: "en" },
    flags: ["f1"],
    plugins: [{ name: "auth", v: 1 }, { name: "log", v: 2 }],
    url: { $ref: "#/db/host" },
  });
  assertMatchesScratch(s, RULES);

  // Conditional layer flips (change reference dependencies).
  s.setContext({ env: "prod", beta: false });
  assertMatchesScratch(s, RULES);
  s.setContext({ env: "prod", beta: true });
  assertMatchesScratch(s, RULES);
  s.setContext({ env: "dev", beta: true });
  assertMatchesScratch(s, RULES);

  // Priority swaps.
  s.swapLayers("base", "app");
  assertMatchesScratch(s, RULES);
  s.swapLayers("base", "app");
  assertMatchesScratch(s, RULES);

  // Layer removal and re-add.
  s.removeLayer("app");
  assertMatchesScratch(s, RULES);

  // Roll back through every historical version.
  for (const v of s.versions()) {
    const historical = s.snapshotAt(v);
    if (historical === undefined) continue;
    const rolled = s.rollback(v);
    assert.deepEqual(rolled.config, historical.config, `rollback to v${v} diverges`);
    assertMatchesScratch(s, RULES);
  }
});

test("hot update recomputes only affected fields", () => {
  const s = buildStore();
  const before = s.snapshot();

  s.updateLayerData("base", {
    db: { host: "db.moved", port: 5432 },
    ui: { theme: "light", lang: "en" },
    flags: ["f1"],
    plugins: [{ name: "auth", v: 1 }, { name: "log", v: 2 }],
    url: { $ref: "#/db/host" },
  });
  const after = s.snapshot();

  // Only /db/host and its dependent ref /url were recomputed.
  assert.deepEqual([...after.recomputed].sort(), ["/db/host", "/url"]);
  assert.equal((after.config as { url: string }).url, "db.moved");

  // Untouched subtrees keep reference identity (structural sharing proof).
  const b = before.config as Record<string, unknown>;
  const a = after.config as Record<string, unknown>;
  assert.equal(a.ui, b.ui);
  assert.equal(a.plugins, b.plugins);
  assert.equal(a.flags, b.flags);
  assert.notEqual(a.db, b.db);
  assert.equal((a.db as Record<string, unknown>).port, (b.db as Record<string, unknown>).port);

  // Unrelated explain records are reused, not rebuilt.
  assert.equal(after.explain["/ui/theme"], before.explain["/ui/theme"]);
  assert.equal(after.explain["/plugins"], before.explain["/plugins"]);
  assert.notEqual(after.explain["/db/host"], before.explain["/db/host"]);

  assertMatchesScratch(s, RULES);
});

test("hot update through a byId field recomputes the whole array field", () => {
  const s = buildStore();
  s.updateLayerData("app", {
    db: { port: 15432 },
    plugins: [{ name: "log", level: "warn" }, { name: "cache" }],
  });
  const snap = s.snapshot();
  assert.deepEqual(snap.recomputed, ["/plugins"]);
  assert.deepEqual(
    (snap.config as { plugins: unknown[] }).plugins,
    [
      { name: "auth", v: 1 },
      { name: "log", v: 2, level: "warn" },
      { name: "cache" },
    ],
  );
  assertMatchesScratch(s, RULES);
});

test("hot update that breaks a reference is rejected, old config intact", () => {
  const s = buildStore();
  const before = s.snapshot();

  // Removing /db/host would orphan the /url reference.
  const err = capture(
    () =>
      s.updateLayerData("base", {
        db: { port: 5432 },
        ui: { theme: "light", lang: "en" },
        flags: ["f1"],
        plugins: [{ name: "auth", v: 1 }, { name: "log", v: 2 }],
        url: { $ref: "#/db/host" },
      }),
    RefResolutionError,
  );
  assert.equal(err.pointer, "/url");

  assert.equal(s.snapshot().version, before.version);
  assert.equal(s.snapshot(), before);
  assertMatchesScratch(s, RULES);

  // The store still accepts valid updates afterwards.
  s.updateLayerData("app", { db: { port: 30000 }, plugins: [{ name: "log" }] });
  assert.equal((s.snapshot().config as { db: { port: number } }).db.port, 30000);
  assertMatchesScratch(s, RULES);
});

test("hot update introducing a reference cycle is rejected atomically", () => {
  const s = buildStore();
  const before = s.snapshot();
  capture(
    () =>
      s.updateLayerData("base", {
        db: { host: { $ref: "#/url" }, port: 5432 },
        ui: { theme: "light", lang: "en" },
        flags: ["f1"],
        plugins: [{ name: "auth", v: 1 }, { name: "log", v: 2 }],
        url: { $ref: "#/db/host" },
      }),
    Error,
  );
  assert.equal(s.snapshot(), before);
  assertMatchesScratch(s, RULES);
});

test("hot update with a byId type conflict is rejected atomically", () => {
  const s = buildStore();
  const before = s.snapshot();
  const err = capture(
    () =>
      s.updateLayerData("app", {
        db: { port: 15432 },
        plugins: ["log"],
      }),
    MergeTypeConflictError,
  );
  assert.equal(err.layerId, "app");
  assert.equal(s.snapshot(), before);
  assertMatchesScratch(s, RULES);
});

test("delete sentinel in a hot update removes keys and stale references", () => {
  const s = new LayeredConfigStore();
  s.addLayer({
    id: "base",
    data: {
      keep: 1,
      drop: { nested: true },
      target: "t",
      alias: { $ref: "#/target" },
    },
  });
  const before = s.snapshot();

  s.updateLayerData("base", {
    keep: 1,
    drop: DELETE,
    target: DELETE,
    alias: "no-longer-a-ref",
  });
  const snap = s.snapshot();
  const cfg = snap.config as Record<string, unknown>;
  assert.equal(Object.hasOwn(cfg, "drop"), false);
  assert.equal(Object.hasOwn(cfg, "target"), false);
  assert.equal(cfg.alias, "no-longer-a-ref");
  assert.deepEqual(snap.dependencies, {});
  assertMatchesScratch(s, []);
  assert.ok(before !== snap);
});

test("no-op hot update does not publish a new version", () => {
  const s = buildStore();
  const before = s.snapshot();
  const same = structuredClone(before.layers.find((l) => l.id === "app")!.data);
  const snap = s.updateLayerData("app", same);
  assert.equal(snap, before);
  assert.equal(s.snapshot().version, before.version);
});
