/**
 * Replayable end-to-end scenario for the layered configuration store.
 *
 * Every step is deterministic and self-verifying: the script throws if any
 * expectation is violated, and prints a transcript of what happened.
 *
 * Run with: npm run replay
 */
import assert from "node:assert/strict";
import {
  LayeredConfigStore,
  DELETE,
  OVERRIDE,
  byId,
  compileFromScratch,
  StrategyMap,
} from "../src/index.ts";
import type { Snapshot, StrategyRule } from "../src/index.ts";

const RULES: readonly StrategyRule[] = [
  { pattern: ["plugins"], strategy: byId("name") },
  { pattern: ["flags"], strategy: OVERRIDE },
];

let step = 0;
function log(title: string, detail: unknown): void {
  step += 1;
  const body = typeof detail === "string" ? detail : JSON.stringify(detail);
  console.log(`[step ${String(step).padStart(2)}] ${title}: ${body}`);
}

function checkAgainstScratch(store: LayeredConfigStore): void {
  const snap = store.snapshot();
  const active = snap.layers
    .filter((l) => l.enabledWhen === undefined || l.enabledWhen(snap.context))
    .map((l) => ({ id: l.id, data: l.data }));
  const scratch = compileFromScratch(active, new StrategyMap(RULES));
  assert.deepEqual(snap.config, scratch.config);
  assert.deepEqual(snap.explain, scratch.explain);
  assert.deepEqual(snap.dependencies, scratch.dependencies);
}

const store = new LayeredConfigStore({
  strategies: RULES,
  context: { env: "dev", beta: false },
});
const published: number[] = [];
store.subscribe((snap: Snapshot) => {
  assert.equal(store.snapshot(), snap, "subscriber must see the published snapshot");
  published.push(snap.version);
});

// --- 1. Compose layers -----------------------------------------------------
store.addLayer({
  id: "base",
  data: {
    db: { host: "db.dev", port: 5432 },
    ui: { theme: "light" },
    flags: ["f1"],
    plugins: [{ name: "auth", v: 1 }, { name: "log", v: 2 }],
    url: { $ref: "#/db/host" },
  },
});
store.addLayer({
  id: "app",
  data: { db: { port: 15432 }, plugins: [{ name: "log", level: "debug" }] },
});
store.addLayer({
  id: "prod",
  data: { db: { host: "db.prod" }, ui: { theme: "dark" } },
  enabledWhen: (ctx) => ctx.env === "prod",
});
store.addLayer({
  id: "beta",
  data: { flags: ["f1", "f2"], extra: { $ref: "#/ui/theme" } },
  enabledWhen: (ctx) => ctx.beta === true,
});
let snap = store.snapshot();
log("composed 4 layers (2 conditional), merged config", snap.config);
assert.equal((snap.config as { url: string }).url, "db.dev");

// --- 2. Explain a field ----------------------------------------------------
const rec = store.explainAt("/db/port");
log(
  "provenance chain of /db/port",
  rec?.contributions.map((c) => `${c.layerId}=${JSON.stringify(c.value)} (${c.kind})`),
);
assert.equal(rec?.winner, "app");

// --- 3. Hot update a single layer ------------------------------------------
const beforeHot = store.snapshot();
store.updateLayerData("base", {
  db: { host: "db.moved", port: 5432 },
  ui: { theme: "light" },
  flags: ["f1"],
  plugins: [{ name: "auth", v: 1 }, { name: "log", v: 2 }],
  url: { $ref: "#/db/host" },
});
snap = store.snapshot();
log("hot update recomputed only", snap.recomputed);
assert.deepEqual([...snap.recomputed].sort(), ["/db/host", "/url"]);
assert.equal((snap.config as { url: string }).url, "db.moved");
assert.equal(
  (snap.config as Record<string, unknown>).ui,
  (beforeHot.config as Record<string, unknown>).ui,
  "untouched subtree keeps identity",
);
checkAgainstScratch(store);

// --- 4. Flip conditional layers --------------------------------------------
store.setContext({ env: "prod", beta: true });
snap = store.snapshot();
log("context prod+beta", snap.config);
assert.equal((snap.config as { url: string }).url, "db.prod");
assert.equal((snap.config as { extra: string }).extra, "dark");
store.setContext({ env: "dev", beta: false });
assert.equal((store.snapshot().config as { url: string }).url, "db.moved");
log("context back to dev, url reverted (no stale refs)", (store.snapshot().config as { url: string }).url);
checkAgainstScratch(store);

// --- 5. A rejected commit keeps the old config ------------------------------
const beforeBad = store.snapshot();
try {
  store.addLayer({ id: "bad", data: { plugins: ["log"] } });
  throw new Error("should have rejected the conflicting batch");
} catch (e) {
  assert.ok(e instanceof Error && e.name === "MergeTypeConflictError");
  log("rejected conflicting batch", (e as Error).message);
}
assert.equal(store.snapshot(), beforeBad);
log("old config still usable after failed commit", store.snapshot().config);

// --- 6. Priority swap -------------------------------------------------------
store.swapLayers("base", "app");
snap = store.snapshot();
log("swapped base/app, /db/port winner", store.explainAt("/db/port")?.winner);
assert.equal(store.explainAt("/db/port")?.winner, "base");
checkAgainstScratch(store);
store.swapLayers("base", "app");
checkAgainstScratch(store);

// --- 7. Delete sentinel vs null ---------------------------------------------
store.updateLayerData("app", {
  db: { port: DELETE },
  plugins: [{ name: "log", level: "debug" }],
  note: null,
});
snap = store.snapshot();
log("sentinel removed /db/port, null kept /note", snap.config);
assert.equal(Object.hasOwn(snap.config as object, "note"), true);
assert.equal(Object.hasOwn((snap.config as { db: object }).db, "port"), false);
checkAgainstScratch(store);

// --- 8. Rollback -------------------------------------------------------------
const target = 2;
const historical = store.snapshotAt(target);
assert.ok(historical);
const rolled = store.rollback(target);
log(`rolled back to v${target} as new version`, rolled.version);
assert.deepEqual(rolled.config, historical.config);
checkAgainstScratch(store);

// --- 9. History and concurrent readers ---------------------------------------
const reader = store.snapshotAt(1);
assert.ok(reader);
assert.deepEqual(reader.config, store.snapshotAt(1)?.config);
log("history versions", store.versions());
log("subscriber saw versions", published);
assert.deepEqual(published, [...Array(published.length).keys()].map((i) => i + 1));

console.log(`\nREPLAY OK — ${step} steps, final version v${store.snapshot().version}`);
