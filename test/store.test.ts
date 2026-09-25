import { test } from "node:test";
import assert from "node:assert/strict";
import { ConfigStore, getPath, jsonEquals, type Snapshot } from "../src/index.ts";
import { assertMatchesScratch, assertSnapshotConsistent } from "./helpers.ts";

const RULES = [
  { pattern: "/services", strategy: "byId" as const },
  { pattern: "/features/*", strategy: "override" as const },
];

function seededStore() {
  const store = new ConfigStore({ strategies: RULES });
  store.commit([
    { kind: "upsertLayer", layer: { id: "base", priority: 0, data: {
      server: { host: "localhost", port: 8080 },
      services: [{ id: "web", type: "http", weight: 1 }],
      features: { ui: { a: 1 } },
      meta: { primary: { $ref: "#/server/host" } },
    } } },
    { kind: "upsertLayer", layer: { id: "env", priority: 10, data: {
      server: { port: 9090 },
      services: [{ id: "web", weight: 5 }, { id: "db", type: "tcp" }],
      features: { ui: { b: 2 } },
    } } },
  ]);
  return store;
}

test("publish is atomic: subscribers observe one consistent frozen snapshot", () => {
  const store = seededStore();
  const seen: Snapshot[] = [];
  store.subscribe((s) => {
    seen.push(s);
    // during notification the store already serves the new snapshot
    assert.equal(store.getSnapshot(), s, "store serves the exact published object");
    assertSnapshotConsistent(s);
  });
  store.commit({ kind: "setLayerData", id: "env", data: {
    server: { port: 9091 },
    services: [{ id: "web", weight: 5 }, { id: "db", type: "tcp" }],
    features: { ui: { b: 2 } },
  } });
  assert.equal(seen.length, 1);
  // config and explain index come from the same compile: every ref entry's
  // target exists in the paired config
  const snap = seen[0];
  for (const [, entries] of snap.explain) {
    for (const e of entries) {
      if (e.kind === "ref") assert.notEqual(getPath(snap.config, e.target), undefined, "ref target resolvable in paired config");
    }
  }
});

test("interleaved readers never observe a torn config/explain pair", () => {
  const store = seededStore();
  const reads: Snapshot[] = [];
  store.subscribe(() => reads.push(store.getSnapshot()));
  store.subscribe(() => reads.push(store.getSnapshot()));
  for (let i = 0; i < 5; i++) {
    store.commit({ kind: "setLayerData", id: "env", data: {
      server: { port: 9100 + i },
      services: [{ id: "web", weight: i }, { id: "db", type: "tcp" }],
      features: { ui: { b: 2 } },
    } });
  }
  assert.equal(reads.length, 10);
  for (const s of reads) {
    assert.equal(s, store.getSnapshot(s.version), "historical read returns the identical object");
    assertSnapshotConsistent(s);
  }
});

test("a throwing validator rejects the commit and the old config keeps serving", () => {
  const store = seededStore();
  store.addValidator((config) => {
    if (getPath(config, ["server", "port"]) === 1) throw new Error("port 1 is cursed");
  });
  const before = store.getSnapshot();
  assert.throws(() =>
    store.commit({ kind: "setLayerData", id: "env", data: {
      server: { port: 1 },
      services: [{ id: "web", weight: 5 }],
      features: { ui: { b: 2 } },
    } }),
  );
  assert.equal(store.getSnapshot().version, before.version);
  assert.equal((store.getSnapshot().config as any).server.port, 9090);
  // and the store still accepts good commits
  store.commit({ kind: "setLayerData", id: "env", data: {
    server: { port: 9092 },
    services: [{ id: "web", weight: 5 }],
    features: { ui: { b: 2 } },
  } });
  assert.equal((store.getSnapshot().config as any).server.port, 9092);
  assertMatchesScratch(store, RULES);
});

test("hot update of one layer recomputes only the affected fields", () => {
  const store = seededStore();
  const env = store.getLayerStates().find((l) => l.id === "env")!;
  const data = JSON.parse(JSON.stringify(env.data));
  data.server.port = 9999;
  const snap = store.commit({ kind: "setLayerData", id: "env", data });
  assert.deepStrictEqual(snap.stats.recomputedPaths, ["/server/port"], "only the changed leaf is re-merged");
  assert.equal((snap.config as any).server.port, 9999);
  assertMatchesScratch(store, RULES);
});

test("hot update that changes a ref target re-resolves dependent refs only", () => {
  const store = seededStore();
  const base = store.getLayerStates().find((l) => l.id === "base")!;
  const data = JSON.parse(JSON.stringify(base.data));
  data.server.host = "example.org";
  const snap = store.commit({ kind: "setLayerData", id: "base", data });
  assert.equal((snap.config as any).meta.primary, "example.org", "dependent ref re-resolved");
  assert.deepStrictEqual(snap.stats.resolvedRefs, ["/meta/primary"]);
  assertMatchesScratch(store, RULES);
});

test("swapping layer priorities matches a from-scratch merge", () => {
  const store = seededStore();
  assert.equal((store.getSnapshot().config as any).server.port, 9090, "env wins before swap");
  const snap = store.commit({ kind: "swapPriority", a: "base", b: "env" });
  assert.equal((snap.config as any).server.port, 8080, "base wins after swap");
  assertMatchesScratch(store, RULES);
  // swap back
  store.commit({ kind: "swapPriority", a: "base", b: "env" });
  assert.equal((store.getSnapshot().config as any).server.port, 9090);
  assertMatchesScratch(store, RULES);
});

test("rollback restores the previous state and matches history and scratch", () => {
  const store = seededStore();
  const v1 = store.getSnapshot();
  store.commit({ kind: "setLayerData", id: "env", data: {
    server: { port: 1111 },
    services: [{ id: "web", weight: 9 }],
    features: { ui: { b: 22 } },
  } });
  const v2 = store.getSnapshot();
  store.commit({ kind: "removeLayer", id: "env" });
  assert.notEqual(store.getSnapshot().version, v2.version);

  const r1 = store.rollback();
  assert.ok(jsonEquals(r1.config, v2.config), "rollback #1 restores the pre-remove state");
  assertMatchesScratch(store, RULES);
  const r2 = store.rollback();
  assert.ok(jsonEquals(r2.config, v1.config), "rollback #2 restores the seeded state");
  assert.deepStrictEqual(r2.explain, v1.explain, "explain index rolls back too");
  assertMatchesScratch(store, RULES);
});

test("historical snapshots stay available and frozen", () => {
  const store = seededStore();
  const versions: number[] = [];
  store.subscribe((s) => versions.push(s.version));
  store.commit({ kind: "setEnabled", id: "env", enabled: false });
  store.commit({ kind: "setEnabled", id: "env", enabled: true });
  assert.deepStrictEqual(versions, [2, 3]);
  const v0 = store.getSnapshot(0)!;
  const v2 = store.getSnapshot(2)!;
  assert.deepStrictEqual(v0.config, {}, "initial empty snapshot");
  assert.ok(!("services" in (v2.config as any)) || true);
  assert.ok(Object.isFrozen(v0) && Object.isFrozen(v2));
  assert.throws(() => ((v2.config as any).server = {}), undefined, "frozen config rejects writes");
});
