import { test } from "node:test";
import assert from "node:assert/strict";
import {
  compileFull,
  ConfigStore,
  jsonEquals,
  StrategyMap,
  type CommitOp,
  type JsonValue,
  type StrategyRule,
} from "../src/index.ts";

const RULES: StrategyRule[] = [
  { pattern: "/services", strategy: "byId" },
  { pattern: "/flags/*", strategy: "override" },
];

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomLeaf(rnd: () => number): JsonValue {
  const r = rnd();
  if (r < 0.1) return { $delete: true };
  if (r < 0.2) return null;
  if (r < 0.3) return { $ref: "#/core/value" };
  if (r < 0.5) return Math.floor(rnd() * 100);
  if (r < 0.7) return rnd() < 0.5;
  return `s${Math.floor(rnd() * 5)}`;
}

function randomData(rnd: () => number): JsonValue {
  const out: Record<string, JsonValue> = {};
  const keys = ["alpha", "beta", "gamma"];
  for (const k of keys) {
    if (rnd() < 0.7) out[k] = { x: randomLeaf(rnd), y: randomLeaf(rnd) };
  }
  if (rnd() < 0.6) out["flags"] = { a: randomLeaf(rnd), b: randomLeaf(rnd) };
  if (rnd() < 0.6) {
    const items: JsonValue[] = [];
    const n = Math.floor(rnd() * 3);
    for (let i = 0; i < n; i++) {
      const id = ["web", "db", "cache"][Math.floor(rnd() * 3)];
      if (rnd() < 0.2) items.push({ id, $delete: true });
      else items.push({ id, weight: Math.floor(rnd() * 9), type: rnd() < 0.8 ? "http" : "tcp" });
    }
    out["services"] = items;
  }
  return out;
}

test("fuzz: 400 random commits/rollbacks always equal a from-scratch compile", () => {
  const rnd = mulberry32(20260926);
  const store = new ConfigStore({ strategies: RULES });
  const scratch = () => compileFull(store.getLayerStates(), store.getContext(), new StrategyMap(RULES));

  store.commit({ kind: "upsertLayer", layer: { id: "core", priority: -100, data: { core: { value: "V" } } } });
  const ids = ["L1", "L2", "L3", "L4"];
  for (const id of ids) {
    store.commit({ kind: "upsertLayer", layer: { id, priority: ids.indexOf(id) * 10, data: randomData(rnd) } });
  }

  let okCommits = 0;
  let rejected = 0;
  let rollbacks = 0;
  for (let step = 0; step < 400; step++) {
    const id = ids[Math.floor(rnd() * ids.length)];
    const pick = rnd();
    let op: CommitOp;
    if (pick < 0.45) op = { kind: "setLayerData", id, data: randomData(rnd) };
    else if (pick < 0.6) op = { kind: "setEnabled", id, enabled: rnd() < 0.5 };
    else if (pick < 0.75) op = { kind: "swapPriority", a: id, b: ids[Math.floor(rnd() * ids.length)] };
    else if (pick < 0.85) op = { kind: "setContext", context: { f: Math.floor(rnd() * 3) } };
    else op = { kind: "upsertLayer", layer: { id, priority: Math.floor(rnd() * 40), data: randomData(rnd) } };

    const before = store.getSnapshot();
    try {
      if (pick >= 0.85 && rnd() < 0.3) {
        store.rollback();
        rollbacks++;
      } else {
        store.commit(op);
        okCommits++;
      }
    } catch (err) {
      // rejected batches (type conflicts, dangling refs) must leave the store untouched
      rejected++;
      assert.equal(store.getSnapshot().version, before.version, "rejected commit must not publish");
      assert.equal(store.getSnapshot(), before, "rejected commit keeps the exact same snapshot");
      continue;
    }
    const full = scratch();
    const snap = store.getSnapshot();
    assert.ok(jsonEquals(snap.config, full.config), `step ${step}: config diverges from scratch`);
    assert.deepStrictEqual(snap.explain, full.explain.map, `step ${step}: explain diverges from scratch`);
  }
  assert.ok(okCommits > 100, `expected enough successful commits, got ${okCommits}`);
  assert.ok(rejected > 0, "expected some rejected batches to exercise transactional abort");
  assert.ok(rollbacks > 0, "expected some rollbacks");
});
