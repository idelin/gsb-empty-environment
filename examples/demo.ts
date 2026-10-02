import { Store } from "../src/store.ts";
import type { TxnResult } from "../src/store.ts";
import { validateAll } from "../src/validator.ts";
import type { Schema } from "../src/schema.ts";

const schemaV1: Schema = {
  version: 1,
  types: {
    author: {
      properties: {
        name: { kind: "scalar", scalar: "string" },
        books: { kind: "refArray", target: "book" },
      },
      required: ["name"],
    },
    book: {
      properties: {
        title: { kind: "scalar", scalar: "string" },
        author: { kind: "ref", target: "author" },
        award: { kind: "ref", target: "award" },
      },
      required: ["title"],
    },
    award: {
      properties: { label: { kind: "scalar", scalar: "string" } },
      required: ["label"],
      maxIncoming: 1,
    },
  },
};

const store = new Store(structuredClone(schemaV1));

function show(label: string, result: TxnResult): void {
  console.log(`\n== ${label}`);
  if (result.ok) {
    console.log("OK");
  } else {
    for (const e of result.errors) {
      console.log(
        `REJECTED [${e.code}] doc=${e.docId} instance=${e.instancePath || "/"} rule=${e.schemaPath}`,
      );
      console.log(`  message: ${e.message}`);
      console.log(`  slice:   ${JSON.stringify(e.slice)}`);
    }
  }
}

// 1. Atomic batch with mutual references (cycle author <-> book) in one txn.
show(
  "batch insert: author <-> book mutual references",
  store.transact([
    { op: "insert", doc: { id: "a1", type: "author", data: { name: "Ann", books: ["b1"] } } },
    { op: "insert", doc: { id: "b1", type: "book", data: { title: "Novel", author: "a1" } } },
  ]),
);

// 2. Dangling reference: rejected, nothing committed.
show(
  "insert book referencing a missing author",
  store.transact([{ op: "insert", doc: { id: "b2", type: "book", data: { title: "Ghost", author: "nobody" } } }]),
);

// 3. Reverse cardinality: award accepts at most one inbound reference.
store.transact([
  { op: "insert", doc: { id: "w1", type: "award", data: { label: "Gold" } } },
  { op: "insert", doc: { id: "b3", type: "book", data: { title: "One", award: "w1" } } },
]);
show(
  "second book referencing the same award (maxIncoming = 1)",
  store.transact([{ op: "insert", doc: { id: "b4", type: "book", data: { title: "Two", award: "w1" } } }]),
);

// 4. Rename inside a transaction rewires referrers and the reverse index.
show("rename a1 -> a2 (b1.author rewired)", store.transact([{ op: "rename", from: "a1", to: "a2" }]));
console.log(`  b1.author is now: ${JSON.stringify(store.get("b1")!.data.author)}`);

// 5. Deleting a still-referenced document is rejected.
show("delete a2 while b1 still references it", store.transact([{ op: "delete", id: "a2" }]));

// 6. Deleting the reference entry point and the target together is allowed.
show(
  "delete b1 and a2 in one batch (cycle removed atomically)",
  store.transact([
    { op: "delete", id: "b1" },
    { op: "delete", id: "a2" },
  ]),
);

// 7. Duplicate id with different content conflicts.
show(
  "insert duplicate id b3 with different content",
  store.transact([{ op: "insert", doc: { id: "b3", type: "book", data: { title: "Changed" } } }]),
);

// 8. Versioned schema upgrade: incompatible target type is rejected.
const schemaV2: Schema = structuredClone(schemaV1);
schemaV2.version = 2;
(schemaV2.types.book.properties as Record<string, { target: string }>).award.target = "medal";
show("setSchema v2 (book.award now targets missing type 'medal')", store.setSchema(schemaV2));

// 9. Independent full validation of the final state: zero residual errors.
const full = validateAll(store.docs.values(), store.schema);
console.log(`\n== final state: ${store.docs.size} docs, schema v${store.schema.version}`);
console.log(`independent full validation errors: ${full.errors.length}`);
console.log(`reverse index targets: ${[...store.incoming.keys()].sort().join(", ") || "(none)"}`);
