/**
 * Replayable sample: reads sample/events.json, applies every batch
 * incrementally, prints per-batch outcomes (with error paths), the final
 * tree with subtree stats, the orphan queue, field conflicts and lineage,
 * then verifies the result against a full rebuild.
 */
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { Store, type Batch, type SourceDef } from "./store.ts";
import { compareWithRebuild } from "./rebuild.ts";

const here = dirname(fileURLToPath(import.meta.url));
const sample = JSON.parse(
  readFileSync(join(here, "..", "sample", "events.json"), "utf8"),
) as { sources: SourceDef[]; batches: Batch[] };

const config = { sources: sample.sources, deletePolicy: "orphan" as const };
const store = new Store(config);

console.log("=== replaying", sample.batches.length, "batches ===");
for (const batch of sample.batches) {
  store.graph.drainTouched();
  const r = store.applyBatch(batch);
  const touched = [...store.graph.drainTouched()].sort();
  console.log(`\n--- ${r.batch} ---`);
  console.log("applied:", r.applied.join(", ") || "(none)");
  if (r.skipped.length) console.log("skipped (duplicate, not double-counted):", r.skipped.join(", "));
  for (const e of r.errors) {
    console.log(`error: [${e.code}] ${e.message}  @ ${e.path}`);
  }
  console.log("stats touched (affected ancestor chain only):", touched.join(", ") || "(none)");
}

function printTree(id: string, indent: string): void {
  const n = store.graph.get(id);
  const name = n.fields.get("name")?.effective;
  const label = name ? `${String(name.value)} (src=${name.source} v${name.version})` : "(undecided)";
  console.log(
    `${indent}${id} ${label} stats={nodeCount:${n.stats.nodeCount} weightSum:${n.stats.weightSum}}`,
  );
  for (const c of [...n.children].sort()) printTree(c, indent + "  ");
}

console.log("\n=== final tree ===");
const roots = [...store.graph.nodes.values()]
  .filter((n) => n.parentId === null && n.pendingParentId === null)
  .map((n) => n.id)
  .sort();
for (const r of roots) printTree(r, "");

console.log("\n=== orphan queue ===");
if (store.graph.orphans.size === 0) console.log("(empty)");
for (const [orphan, waitingFor] of store.graph.orphans) {
  console.log(`${orphan} waiting for parent ${waitingFor}`);
}

console.log("\n=== field conflicts (undecided, never picked arbitrarily) ===");
for (const id of [...store.graph.nodes.keys()].sort()) {
  const lin = store.lineage(id);
  for (const [field, info] of Object.entries(lin)) {
    const c = (info as { conflict: unknown }).conflict;
    if (c) console.log(`${id}.${field}:`, JSON.stringify(c));
  }
}

console.log("\n=== lineage sample: B.name ===");
console.log(JSON.stringify(store.lineage("B").name, null, 2));

const cmp = compareWithRebuild(store, config, sample.batches);
console.log("\n=== full-rebuild comparison ===");
console.log(cmp.equal ? "MATCH: incremental result equals full rebuild" : "MISMATCH!");
if (!cmp.equal) process.exitCode = 1;
