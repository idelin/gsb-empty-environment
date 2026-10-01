import { DocumentStore, validateAll, type TxResult } from "../src/index.ts";
import { makeRegistry } from "../test/helpers.ts";

const registry = makeRegistry();
const store = new DocumentStore(registry);

function show(title: string, res: TxResult): void {
  console.log(`\n=== ${title} ===`);
  console.log(res.ok ? "OK" : "REJECTED");
  for (const e of res.errors) {
    console.log(`  [${e.code}] ${e.message}`);
    console.log(`    instancePath: ${e.instancePath || "(document)"}   rulePath: ${e.rulePath || "(n/a)"}`);
    for (const d of e.docs) console.log(`    slice: doc=${d.id} path=${d.path} value=${JSON.stringify(d.value)}`);
  }
}

// 1. Atomic batch with mutual references (cycle) — passes existence checks.
show(
  "batch insert: mutually referencing users (cycle is legal)",
  store.transact([
    { op: "insert", doc: { id: "ann", type: "user", schemaVersion: 1, data: { name: "Ann", friend: "bob" } } },
    { op: "insert", doc: { id: "bob", type: "user", schemaVersion: 1, data: { name: "Bob", friend: "ann" } } },
  ]),
);

// 2. Dangling reference: instance path + rule path + minimal slice.
show(
  "insert with dangling reference",
  store.transact([
    { op: "insert", doc: { id: "eve", type: "user", schemaVersion: 1, data: { name: "Eve", friend: "ghost" } } },
  ]),
);

// 3. Duplicate id in a different document conflicts.
show(
  "duplicate id in a different document",
  store.transact([{ op: "insert", doc: { id: "ann", type: "team", data: { name: "Team Ann" } } }]),
);

// 4. Reverse cardinality: post.editor allows at most one incoming reference.
store.transact([{ op: "insert", doc: { id: "p1", type: "post", data: { title: "One", author: "ann", editor: "bob" } } }]);
show(
  "second post pointing editor at the same user",
  store.transact([{ op: "insert", doc: { id: "p2", type: "post", data: { title: "Two", author: "ann", editor: "bob" } } }]),
);

// 5. In-transaction rename rewrites the reference graph.
show("rename ann -> user-ann (post p1 follows)", store.transact([{ op: "rename", id: "ann", newId: "user-ann" }]));
console.log("  p1 after rename:", JSON.stringify(store.get("p1")!.data));

// 6. Deleting a still-referenced document is rejected.
show("delete user-ann while p1.author references her", store.transact([{ op: "delete", id: "user-ann" }]));

// 7. Deleting the cycle's entry points leaves zero residual errors.
show(
  "delete the whole cycle (entry points first, same batch)",
  store.transact([
    { op: "delete", id: "p1" },
    { op: "delete", id: "user-ann" },
    { op: "delete", id: "bob" },
  ]),
);

// 8. Independent full validator cross-checks the incremental state.
const full = validateAll(registry, store.snapshot());
console.log(`\n=== full validation after all batches ===`);
console.log(`  documents: ${store.size}, residual errors: ${full.errors.length}`);
