import { test } from "node:test";
import assert from "node:assert/strict";
import { Store } from "../src/store.ts";
import type { Doc } from "../src/store.ts";
import { validateAll } from "../src/validator.ts";
import type { Schema } from "../src/schema.ts";
import { getAtPath } from "../src/pointer.ts";

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

const author = (id: string, name: string, books: string[] = []): Doc => ({
  id,
  type: "author",
  data: { name, books },
});

const book = (id: string, title: string, authorId?: string): Doc => ({
  id,
  type: "book",
  data: authorId === undefined ? { title } : { title, author: authorId },
});

function makeStore(): Store {
  return new Store(structuredClone(schemaV1));
}

test("same-batch mutual references pass the existence check (cycle is legal)", () => {
  const store = makeStore();
  const a = author("a1", "Ann", ["b1"]);
  const b = book("b1", "Novel", "a1");
  const result = store.transact([
    { op: "insert", doc: a },
    { op: "insert", doc: b },
  ]);
  assert.deepEqual(result, { ok: true });
  assert.equal(validateAll(store.docs.values(), store.schema).errors.length, 0);
  assert.deepEqual([...store.incoming.get("b1")!.keys()], ["a1"]);
  assert.deepEqual([...store.incoming.get("a1")!.keys()], ["b1"]);
});

test("dangling reference is rejected with instance path, rule path and minimal slice", () => {
  const store = makeStore();
  const result = store.transact([{ op: "insert", doc: book("b1", "Novel", "ghost") }]);
  assert.equal(result.ok, false);
  if (result.ok) return;
  assert.equal(result.errors.length, 1);
  const err = result.errors[0];
  assert.equal(err.code, "REF_NOT_FOUND");
  assert.equal(err.docId, "b1");
  assert.equal(err.instancePath, "/author");
  assert.equal(err.schemaPath, "#/types/book/properties/author");
  assert.equal(err.slice, "ghost");
  assert.equal(store.docs.size, 0, "failed batch must not commit anything");
});

test("reference to the wrong target type is rejected", () => {
  const store = makeStore();
  assert.deepEqual(store.transact([{ op: "insert", doc: author("a1", "Ann") }]), { ok: true });
  const result = store.transact([
    { op: "insert", doc: { id: "b1", type: "book", data: { title: "T", award: "a1" } } },
  ]);
  assert.equal(result.ok, false);
  if (result.ok) return;
  assert.equal(result.errors[0].code, "REF_WRONG_TYPE");
  assert.equal(result.errors[0].instancePath, "/award");
  assert.equal(result.errors[0].schemaPath, "#/types/book/properties/award");
});

test("deleting a still-referenced document is rejected; deleting the whole cycle leaves zero errors", () => {
  const store = makeStore();
  assert.deepEqual(
    store.transact([
      { op: "insert", doc: author("a1", "Ann", ["b1"]) },
      { op: "insert", doc: book("b1", "Novel", "a1") },
    ]),
    { ok: true },
  );

  const rejected = store.transact([{ op: "delete", id: "a1" }]);
  assert.equal(rejected.ok, false);
  if (!rejected.ok) {
    assert.equal(rejected.errors[0].code, "DELETE_REFERENCED");
    assert.equal(rejected.errors[0].docId, "a1");
    assert.deepEqual(rejected.errors[0].slice, ["b1"]);
  }
  assert.ok(store.get("a1"), "rejected delete keeps the document");

  // Remove the reference entry point and the cycle member in one batch.
  assert.deepEqual(
    store.transact([
      { op: "delete", id: "b1" },
      { op: "delete", id: "a1" },
    ]),
    { ok: true },
  );
  const full = validateAll(store.docs.values(), store.schema);
  assert.equal(full.errors.length, 0, "no residual errors after the cycle is gone");
  assert.equal(store.incoming.size, 0, "no residual reverse-index entries");
});

test("rename rewires referrers and the reverse index atomically", () => {
  const store = makeStore();
  assert.deepEqual(
    store.transact([
      { op: "insert", doc: author("a1", "Ann", ["b1"]) },
      { op: "insert", doc: book("b1", "Novel", "a1") },
    ]),
    { ok: true },
  );
  assert.deepEqual(store.transact([{ op: "rename", from: "a1", to: "a2" }]), { ok: true });
  assert.equal(store.get("a1"), undefined);
  assert.equal(store.get("a2")!.id, "a2");
  assert.equal(store.get("b1")!.data.author, "a2");
  assert.deepEqual(store.get("a2")!.data.books, ["b1"]);
  assert.ok(store.incoming.has("a2"));
  assert.ok(!store.incoming.has("a1"));
  assert.equal(validateAll(store.docs.values(), store.schema).errors.length, 0);

  // Rename into an existing id conflicts; renaming a missing id is NOT_FOUND.
  const conflict = store.transact([{ op: "rename", from: "a2", to: "b1" }]);
  assert.equal(conflict.ok, false);
  if (!conflict.ok) assert.equal(conflict.errors[0].code, "RENAME_CONFLICT");
  const missing = store.transact([{ op: "rename", from: "nope", to: "x" }]);
  assert.equal(missing.ok, false);
  if (!missing.ok) assert.equal(missing.errors[0].code, "NOT_FOUND");
});

test("rename and insert in the same batch: new doc can reference the new id", () => {
  const store = makeStore();
  assert.deepEqual(store.transact([{ op: "insert", doc: author("a1", "Ann") }]), { ok: true });
  const result = store.transact([
    { op: "rename", from: "a1", to: "a2" },
    { op: "insert", doc: book("b1", "Novel", "a2") },
  ]);
  assert.deepEqual(result, { ok: true });
  assert.equal(store.get("b1")!.data.author, "a2");
});

test("duplicate id with different documents conflicts; identical re-insert is a no-op", () => {
  const store = makeStore();
  const a = author("a1", "Ann");
  assert.deepEqual(store.transact([{ op: "insert", doc: a }]), { ok: true });
  assert.deepEqual(store.transact([{ op: "insert", doc: author("a1", "Ann") }]), { ok: true });
  const conflict = store.transact([{ op: "insert", doc: author("a1", "Bob") }]);
  assert.equal(conflict.ok, false);
  if (!conflict.ok) {
    assert.equal(conflict.errors[0].code, "DUPLICATE_ID");
    assert.equal(conflict.errors[0].docId, "a1");
  }
  assert.equal(store.get("a1")!.data.name, "Ann");
});

test("reverse cardinality: maxIncoming is enforced across referrers", () => {
  const store = makeStore();
  assert.deepEqual(
    store.transact([
      { op: "insert", doc: { id: "w1", type: "award", data: { label: "Gold" } } },
      { op: "insert", doc: { id: "b1", type: "book", data: { title: "One", award: "w1" } } },
    ]),
    { ok: true },
  );
  const result = store.transact([
    { op: "insert", doc: { id: "b2", type: "book", data: { title: "Two", award: "w1" } } },
  ]);
  assert.equal(result.ok, false);
  if (!result.ok) {
    const err = result.errors.find((e) => e.code === "MAX_INCOMING_EXCEEDED")!;
    assert.equal(err.docId, "w1");
    assert.equal(err.schemaPath, "#/types/award/maxIncoming");
    assert.deepEqual(err.slice, ["b1", "b2"]);
  }
  assert.equal(store.get("b2"), undefined, "rejected batch commits nothing");
});

test("batch is atomic: one bad op rejects the whole batch", () => {
  const store = makeStore();
  const result = store.transact([
    { op: "insert", doc: author("a1", "Ann") },
    { op: "insert", doc: book("b1", "Novel", "ghost") },
  ]);
  assert.equal(result.ok, false);
  assert.equal(store.docs.size, 0);
  assert.equal(store.incoming.size, 0);
});

test("versioned schema: incompatible upgrade is rejected, compatible one applies", () => {
  const store = makeStore();
  assert.deepEqual(
    store.transact([
      { op: "insert", doc: author("a1", "Ann") },
      { op: "insert", doc: book("b1", "Novel", "a1") },
    ]),
    { ok: true },
  );

  const v2Bad: Schema = structuredClone(schemaV1);
  v2Bad.version = 2;
  (v2Bad.types.book.properties as Record<string, { target: string }>).author.target = "publisher";
  const rejected = store.setSchema(v2Bad);
  assert.equal(rejected.ok, false);
  if (!rejected.ok) {
    assert.equal(rejected.errors[0].code, "REF_WRONG_TYPE");
    assert.equal(rejected.errors[0].schemaPath, "#/types/book/properties/author");
  }
  assert.equal(store.schema.version, 1, "rejected schema swap keeps the old version");

  const v2Good: Schema = structuredClone(schemaV1);
  v2Good.version = 2;
  v2Good.types.book.properties.pages = { kind: "scalar", scalar: "number" };
  assert.deepEqual(store.setSchema(v2Good), { ok: true });
  assert.equal(store.schema.version, 2);
});

test("error slice is the minimal document slice at the instance path", () => {
  const store = makeStore();
  const nested: Doc = {
    id: "b1",
    type: "book",
    data: { title: 42 as unknown as string },
  };
  const result = store.transact([{ op: "insert", doc: nested }]);
  assert.equal(result.ok, false);
  if (result.ok) return;
  const err = result.errors[0];
  assert.equal(err.code, "TYPE_MISMATCH");
  assert.equal(err.instancePath, "/title");
  assert.equal(err.schemaPath, "#/types/book/properties/title");
  const segments = err.instancePath.split("/").slice(1);
  assert.equal(getAtPath(nested.data, segments), err.slice);
});
