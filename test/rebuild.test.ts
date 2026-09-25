import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { Store, type Batch, type SourceDef } from "../src/store.ts";
import { compareWithRebuild } from "../src/rebuild.ts";
import { deepEqual } from "../src/json.ts";

const here = dirname(fileURLToPath(import.meta.url));
const sample = JSON.parse(
  readFileSync(join(here, "..", "sample", "events.json"), "utf8"),
) as { sources: SourceDef[]; batches: Batch[] };
const config = { sources: sample.sources, deletePolicy: "orphan" as const };

function replay() {
  const store = new Store(config);
  const results = sample.batches.map((b) => {
    store.graph.drainTouched();
    const r = store.applyBatch(b);
    return { result: r, touched: [...store.graph.drainTouched()].sort() };
  });
  return { store, results };
}

test("incremental application equals full rebuild", () => {
  const { store } = replay();
  const cmp = compareWithRebuild(store, config, sample.batches);
  assert.ok(cmp.equal, "incremental state must equal full rebuild");
});

test("sample scenario: end-to-end expectations", () => {
  const { store, results } = replay();
  const [b1, b2, b3, b4, b5, b6] = results.map((r) => r.result);

  // b1: child D arrives before parent M -> orphan queue
  assert.deepEqual(b1.applied, ["e1", "e2", "e3", "e4", "e5", "e6"]);

  // b2: duplicate e6 skipped (no double count); M arrives, D reattached
  assert.deepEqual(b2.skipped, ["e6"]);
  assert.deepEqual(b2.applied, ["e7"]);
  assert.equal(store.graph.get("D").parentId, "M");

  // b3: B reparented A->R; moving R under its descendant B rejected atomically
  assert.deepEqual(b3.applied, ["e8"]);
  assert.equal(b3.errors.length, 1);
  assert.equal(b3.errors[0]!.code, "CYCLE");
  assert.equal(b3.errors[0]!.path, "/events/1/entity/parentId");
  assert.equal(results[2]!.touched.join(","), "A,R");

  // b4: parent deleted -> child kept as orphan; restored from another source
  assert.deepEqual(b4.applied, ["e10", "e11"]);
  assert.equal(store.graph.orphans.size, 0);
  assert.equal(store.graph.get("D").parentId, "M");

  // b5: crm delete of A -> name falls back to erp's older value
  assert.deepEqual(b5.applied, ["e12"]);
  const aName = store.lineage("A").name as { effective: { value: string; source: string } };
  assert.equal(aName.effective.value, "A-erp");
  assert.equal(aName.effective.source, "erp");

  // b6: stale version rejected with path
  assert.equal(b6.errors[0]!.code, "STALE_VERSION");
  assert.equal(b6.errors[0]!.path, "/events/0/version");

  // final aggregate stats
  assert.deepEqual(store.graph.get("R").stats, { nodeCount: 5, weightSum: 15 });
  assert.deepEqual(store.graph.get("M").stats, { nodeCount: 2, weightSum: 9 });

  // B.name stays undecided between equal-priority erp and web
  const bName = store.lineage("B").name as { effective: unknown; conflict: unknown[] };
  assert.equal(bName.effective, null);
  assert.equal(bName.conflict.length, 2);
});

test("cycle rejection leaves stats untouched", () => {
  const store = new Store(config);
  for (const b of sample.batches.slice(0, 2)) store.applyBatch(b);
  const before = store.snapshot();
  store.graph.drainTouched();
  const r = store.applyBatch({
    batch: "cycle-only",
    events: [
      { kind: "upsert", eventId: "x1", source: "crm", version: 2,
        entity: { id: "R", parentId: "A", fields: { name: "evil", weight: 99 } } },
    ],
  });
  assert.equal(r.errors[0]!.code, "CYCLE");
  assert.ok(deepEqual(store.snapshot(), before));
  assert.equal(store.graph.drainTouched().size, 0, "no stats propagation happened");
});

test("orphan queue visible mid-replay and drained when parent arrives", () => {
  const store = new Store(config);
  store.applyBatch(sample.batches[0]!);
  assert.equal(store.graph.orphans.get("D"), "M");
  assert.equal(store.graph.get("D").parentId, null);
  store.applyBatch(sample.batches[1]!);
  assert.equal(store.graph.orphans.size, 0);
});
