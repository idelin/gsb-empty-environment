import { test } from "node:test";
import assert from "node:assert/strict";
import { Store, type Batch, type StoreConfig } from "../src/store.ts";
import { deepEqual } from "../src/json.ts";

const config: StoreConfig = {
  sources: [
    { id: "crm", priority: 10 },
    { id: "erp", priority: 5 },
    { id: "web", priority: 5 },
  ],
  deletePolicy: "orphan",
};

function upsert(
  eventId: string,
  source: string,
  version: number,
  id: string,
  parentId: string | null,
  fields: Record<string, unknown> = {},
) {
  return { kind: "upsert", eventId, source, version, entity: { id, parentId, fields } };
}

function apply(store: Store, events: unknown[], batch = "b") {
  return store.applyBatch({ batch, events });
}

test("field priority: higher-priority source wins, lineage keeps both", () => {
  const s = new Store(config);
  apply(s, [
    upsert("e1", "erp", 1, "A", null, { name: "A-erp" }),
    upsert("e2", "crm", 1, "A", null, { name: "A-crm" }),
  ]);
  const lin = s.lineage("A").name as { effective: { value: string; source: string }; contributions: unknown[] };
  assert.equal(lin.effective.value, "A-crm");
  assert.equal(lin.effective.source, "crm");
  assert.equal(lin.contributions.length, 2);
});

test("same id, same priority, different name: undecided, never arbitrary", () => {
  const s = new Store(config);
  apply(s, [
    upsert("e1", "erp", 1, "B", null, { name: "B-erp" }),
    upsert("e2", "web", 1, "B", null, { name: "B-web" }),
  ]);
  const lin = s.lineage("B").name as { effective: unknown; conflict: { source: string }[] };
  assert.equal(lin.effective, null);
  assert.deepEqual(lin.conflict.map((c) => c.source).sort(), ["erp", "web"]);
});

test("tied sources with equal values are not a conflict", () => {
  const s = new Store(config);
  apply(s, [
    upsert("e1", "erp", 1, "B", null, { name: "same" }),
    upsert("e2", "web", 1, "B", null, { name: "same" }),
  ]);
  const lin = s.lineage("B").name as { effective: { value: string }; conflict: unknown };
  assert.equal(lin.effective.value, "same");
  assert.equal(lin.conflict, null);
});

test("duplicate events across batches are skipped, never double-counted", () => {
  const s = new Store(config);
  const ev = upsert("e1", "crm", 1, "A", null, { weight: 5 });
  const r1 = apply(s, [ev], "b1");
  const r2 = apply(s, [ev], "b2");
  assert.deepEqual(r1.applied, ["e1"]);
  assert.deepEqual(r2.skipped, ["e1"]);
  assert.deepEqual(r2.applied, []);
  assert.deepEqual(s.graph.get("A").stats, { nodeCount: 1, weightSum: 5 });
});

test("stale version is rejected with a locating path", () => {
  const s = new Store(config);
  apply(s, [upsert("e1", "crm", 2, "A", null, { name: "new" })]);
  const r = apply(s, [upsert("e2", "crm", 1, "A", null, { name: "old" })]);
  assert.equal(r.errors.length, 1);
  assert.equal(r.errors[0]!.code, "STALE_VERSION");
  assert.equal(r.errors[0]!.path, "/events/0/version");
  const lin = s.lineage("A").name as { effective: { value: string; version: number } };
  assert.equal(lin.effective.value, "new");
  assert.equal(lin.effective.version, 2);
});

test("delete falls back to older contribution from another source", () => {
  const s = new Store(config);
  apply(s, [
    upsert("e1", "erp", 1, "A", null, { name: "A-erp", weight: 2 }),
    upsert("e2", "crm", 1, "A", null, { name: "A-crm" }),
  ]);
  const r = apply(s, [{ kind: "delete", eventId: "e3", source: "crm", version: 2, entityId: "A" }]);
  assert.deepEqual(r.errors, []);
  const lin = s.lineage("A").name as { effective: { value: string; source: string } };
  assert.equal(lin.effective.value, "A-erp");
  assert.equal(lin.effective.source, "erp");
  assert.ok(s.graph.has("A"), "entity survives via remaining source");
});

test("delete parent keeps children; restore from another source keeps child identity", () => {
  const s = new Store(config);
  apply(s, [
    upsert("e1", "crm", 1, "R", null, {}),
    upsert("e2", "crm", 1, "M", "R", { weight: 5 }),
    upsert("e3", "erp", 1, "D", "M", { name: "D", weight: 4 }),
  ]);
  apply(s, [{ kind: "delete", eventId: "e4", source: "crm", version: 2, entityId: "M" }]);
  assert.equal(s.graph.has("M"), false);
  assert.equal(s.graph.orphans.get("D"), "M");
  assert.deepEqual(s.graph.get("D").stats, { nodeCount: 1, weightSum: 4 });

  apply(s, [upsert("e5", "erp", 1, "M", "R", { name: "M-erp", weight: 5 })]);
  assert.equal(s.graph.orphans.size, 0);
  assert.equal(s.graph.get("D").parentId, "M");
  const lin = s.lineage("D").name as { effective: { value: string } };
  assert.equal(lin.effective.value, "D", "child identity and fields preserved");
  assert.deepEqual(s.graph.get("M").stats, { nodeCount: 2, weightSum: 9 });
});

test("reparent moves subtree and updates only affected ancestor chains", () => {
  const s = new Store(config);
  apply(s, [
    upsert("e1", "crm", 1, "R", null, { weight: 1 }),
    upsert("e2", "crm", 1, "A", "R", { weight: 2 }),
    upsert("e3", "crm", 1, "B", "A", { weight: 3 }),
    upsert("e4", "crm", 1, "U", null, { weight: 100 }),
  ]);
  assert.deepEqual(s.graph.get("R").stats, { nodeCount: 3, weightSum: 6 });
  s.graph.drainTouched();
  apply(s, [upsert("e5", "crm", 2, "B", "R", { weight: 3 })]);
  assert.deepEqual(s.graph.get("A").stats, { nodeCount: 1, weightSum: 2 });
  assert.deepEqual(s.graph.get("R").stats, { nodeCount: 3, weightSum: 6 });
  assert.deepEqual(s.graph.get("B").stats, { nodeCount: 1, weightSum: 3 });
  const touched = s.graph.drainTouched();
  assert.deepEqual([...touched].sort(), ["A", "R"], "unrelated node U untouched");
});

test("cycle via parent correction is rejected atomically, stats unchanged", () => {
  const s = new Store(config);
  apply(s, [
    upsert("e1", "crm", 1, "R", null, { weight: 1 }),
    upsert("e2", "crm", 1, "B", "R", { weight: 3 }),
  ]);
  const before = s.snapshot();
  const r = apply(s, [upsert("e3", "crm", 2, "R", "B", { name: "x", weight: 9 })]);
  assert.equal(r.errors.length, 1);
  assert.equal(r.errors[0]!.code, "CYCLE");
  assert.equal(r.errors[0]!.path, "/events/0/entity/parentId");
  assert.ok(deepEqual(s.snapshot(), before), "entire event rolled back: no field, weight or link change");
});

test("validation errors locate the exact input path", () => {
  const s = new Store(config);
  const r = s.applyBatch({
    batch: "bad",
    events: [
      { kind: "upsert", eventId: "e1", source: "crm", version: 1, entity: { parentId: null } },
      { kind: "upsert", eventId: "e2", source: "crm", version: 1, entity: { id: "A", parentId: null, fields: { bad: undefined } } },
      { kind: "upsert", eventId: "e3", source: "ghost", version: 1, entity: { id: "B", parentId: null } },
    ],
  } as unknown as Batch);
  assert.equal(r.errors[0]!.code, "VALIDATION");
  assert.equal(r.errors[0]!.path, "/events/0/entity/id");
  assert.equal(r.errors[1]!.code, "VALIDATION");
  assert.equal(r.errors[1]!.path, "/events/1/entity/fields/bad");
  assert.equal(r.errors[2]!.code, "UNKNOWN_SOURCE");
});

test("object key order does not affect the outcome", () => {
  const a = new Store(config);
  const b = new Store(config);
  a.applyBatch(JSON.parse(`{"batch":"b","events":[{"kind":"upsert","eventId":"e1","source":"crm","version":1,"entity":{"id":"A","parentId":null,"fields":{"name":"x","weight":1}}}]}`));
  b.applyBatch(JSON.parse(`{"events":[{"entity":{"fields":{"weight":1,"name":"x"},"parentId":null,"id":"A"},"version":1,"source":"crm","eventId":"e1","kind":"upsert"}],"batch":"b"}`));
  assert.ok(deepEqual(a.snapshot(), b.snapshot()));
});

test("lineage paths point into the source batch", () => {
  const s = new Store(config);
  apply(s, [
    upsert("e0", "crm", 1, "pad", null, {}),
    upsert("e1", "erp", 3, "A", null, { name: "A-erp" }),
  ]);
  const lin = s.lineage("A").name as { effective: { path: string; source: string; version: number } };
  assert.equal(lin.effective.path, "/events/1/entity/fields/name");
  assert.equal(lin.effective.source, "erp");
  assert.equal(lin.effective.version, 3);
});
