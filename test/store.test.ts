import { test } from "node:test";
import assert from "node:assert/strict";
import { validateAll } from "../src/validator.ts";
import { makeStore, normalizeReverse } from "./helpers.ts";

test("batch insert with mutual references passes existence checks", () => {
  const { registry, store } = makeStore();
  const res = store.transact([
    { op: "insert", doc: { id: "a", type: "user", schemaVersion: 1, data: { name: "Ann", friend: "b" } } },
    { op: "insert", doc: { id: "b", type: "user", schemaVersion: 1, data: { name: "Bob", friend: "a" } } },
  ]);
  assert.equal(res.ok, true);
  assert.equal(store.size, 2);
  const full = validateAll(registry, store.snapshot());
  assert.equal(full.errors.length, 0, "cycle is legal: no residual errors");
  assert.deepEqual(normalizeReverse(store.reverseIndex()), normalizeReverse(full.reverse));
});

test("deleting reference entry points of a cycle leaves zero errors", () => {
  const { registry, store } = makeStore();
  store.transact([
    { op: "insert", doc: { id: "a", type: "user", schemaVersion: 1, data: { name: "Ann", friend: "b" } } },
    { op: "insert", doc: { id: "b", type: "user", schemaVersion: 1, data: { name: "Bob", friend: "a" } } },
  ]);
  const res = store.transact([
    { op: "delete", id: "a" },
    { op: "delete", id: "b" },
  ]);
  assert.equal(res.ok, true);
  assert.equal(store.size, 0);
  const full = validateAll(registry, store.snapshot());
  assert.equal(full.errors.length, 0, "no residual error count after deleting the cycle");
  assert.equal(store.reverseIndex().size, 0, "reverse index is empty too");
});

test("dangling reference fails with instance path, rule path and doc slice", () => {
  const { store } = makeStore();
  const res = store.transact([
    { op: "insert", doc: { id: "a", type: "user", schemaVersion: 1, data: { name: "Ann", friend: "ghost" } } },
  ]);
  assert.equal(res.ok, false);
  assert.equal(res.errors.length, 1);
  const err = res.errors[0];
  assert.equal(err.code, "ref-missing");
  assert.equal(err.instancePath, "/friend");
  assert.equal(err.rulePath, "/properties/friend");
  assert.deepEqual(err.docs, [{ id: "a", path: "/friend", value: "ghost" }]);
  assert.equal(store.size, 0, "failed batch commits nothing");
});

test("duplicate id in different documents conflicts, inside and across batches", () => {
  const { store } = makeStore();
  const res = store.transact([
    { op: "insert", doc: { id: "a", type: "user", schemaVersion: 1, data: { name: "Ann" } } },
    { op: "insert", doc: { id: "a", type: "user", schemaVersion: 1, data: { name: "Other Ann" } } },
  ]);
  assert.equal(res.ok, false);
  assert.equal(res.errors[0].code, "duplicate-id");
  assert.equal(store.size, 0);

  assert.equal(store.transact([{ op: "insert", doc: { id: "a", type: "user", schemaVersion: 1, data: { name: "Ann" } } }]).ok, true);
  const res2 = store.transact([{ op: "insert", doc: { id: "a", type: "team", data: { name: "Team A" } } }]);
  assert.equal(res2.ok, false);
  assert.equal(res2.errors[0].code, "duplicate-id");
  assert.equal(store.get("a")!.type, "user", "existing document untouched");
});

test("target type is checked through the referring document's schema version", () => {
  const { store } = makeStore();
  assert.equal(
    store.transact([
      { op: "insert", doc: { id: "t1", type: "team", data: { name: "Core" } } },
      { op: "insert", doc: { id: "u1", type: "user", schemaVersion: 1, data: { name: "Ann" } } },
    ]).ok,
    true,
  );
  // audit@1 only allows user subjects.
  const v1 = store.transact([
    { op: "insert", doc: { id: "au1", type: "audit", schemaVersion: 1, data: { subject: "t1" } } },
  ]);
  assert.equal(v1.ok, false);
  assert.equal(v1.errors[0].code, "ref-type");
  assert.deepEqual(v1.errors[0].docs[1], { id: "t1", path: "#", value: { type: "team", schemaVersion: 1 } });
  // audit@2 (latest) also allows teams.
  const v2 = store.transact([{ op: "insert", doc: { id: "au2", type: "audit", data: { subject: "t1" } } }]);
  assert.equal(v2.ok, true);
  assert.equal(store.get("au2")!.schemaVersion, 2, "unpinned insert resolves to latest version");
});

test("document pinned to an older schema version validates against that version", () => {
  const { store } = makeStore();
  // user@2 requires email; pinned to v1 it is not required.
  const pinned = store.transact([
    { op: "insert", doc: { id: "u1", type: "user", schemaVersion: 1, data: { name: "Ann" } } },
  ]);
  assert.equal(pinned.ok, true);
  // Latest (v2) requires email.
  const latest = store.transact([{ op: "insert", doc: { id: "u2", type: "user", data: { name: "Bob" } } }]);
  assert.equal(latest.ok, false);
  assert.equal(latest.errors[0].code, "missing-required");
  assert.equal(latest.errors[0].rulePath, "/required");
});

test("reverse cardinality: maxIncoming=1 on post.editor", () => {
  const { store } = makeStore();
  assert.equal(
    store.transact([
      { op: "insert", doc: { id: "u1", type: "user", schemaVersion: 1, data: { name: "Ann" } } },
      { op: "insert", doc: { id: "u2", type: "user", schemaVersion: 1, data: { name: "Bob" } } },
    ]).ok,
    true,
  );
  const res = store.transact([
    { op: "insert", doc: { id: "p1", type: "post", data: { title: "One", author: "u1", editor: "u2" } } },
    { op: "insert", doc: { id: "p2", type: "post", data: { title: "Two", author: "u1", editor: "u2" } } },
  ]);
  assert.equal(res.ok, false);
  const err = res.errors.find((e) => e.code === "ref-max-incoming")!;
  assert.equal(err.rulePath, "/properties/editor");
  assert.match(err.message, /"u2".*2 documents/);
  assert.equal(store.size, 2, "atomic: neither post was committed");
  // Same batch with distinct editors passes.
  const ok = store.transact([
    { op: "insert", doc: { id: "p1", type: "post", data: { title: "One", author: "u1", editor: "u2" } } },
    { op: "insert", doc: { id: "p2", type: "post", data: { title: "Two", author: "u2", editor: "u1" } } },
  ]);
  assert.equal(ok.ok, true);
});

test("rename rewrites the reference graph and the reverse index atomically", () => {
  const { registry, store } = makeStore();
  store.transact([
    { op: "insert", doc: { id: "u1", type: "user", schemaVersion: 1, data: { name: "Ann" } } },
    { op: "insert", doc: { id: "p1", type: "post", data: { title: "One", author: "u1", editor: "u1" } } },
  ]);
  const res = store.transact([{ op: "rename", id: "u1", newId: "user-1" }]);
  assert.equal(res.ok, true);
  assert.equal(store.has("u1"), false);
  const post = store.get("p1")!;
  assert.deepEqual(post.data, { title: "One", author: "user-1", editor: "user-1" });
  const incoming = store.incoming("user-1");
  assert.equal(incoming.length, 2);
  const full = validateAll(registry, store.snapshot());
  assert.equal(full.errors.length, 0);
  assert.deepEqual(normalizeReverse(store.reverseIndex()), normalizeReverse(full.reverse));
});

test("rename to an existing id conflicts and changes nothing", () => {
  const { store } = makeStore();
  store.transact([
    { op: "insert", doc: { id: "u1", type: "user", schemaVersion: 1, data: { name: "Ann" } } },
    { op: "insert", doc: { id: "u2", type: "user", schemaVersion: 1, data: { name: "Bob" } } },
  ]);
  const res = store.transact([{ op: "rename", id: "u1", newId: "u2" }]);
  assert.equal(res.ok, false);
  assert.equal(res.errors[0].code, "duplicate-id");
  assert.equal(store.get("u1")!.type, "user");
  const res2 = store.transact([{ op: "rename", id: "ghost", newId: "u3" }]);
  assert.equal(res2.ok, false);
  assert.equal(res2.errors[0].code, "rename-missing");
});

test("deleting a still-referenced document is rejected", () => {
  const { store } = makeStore();
  store.transact([
    { op: "insert", doc: { id: "u1", type: "user", schemaVersion: 1, data: { name: "Ann" } } },
    { op: "insert", doc: { id: "p1", type: "post", data: { title: "One", author: "u1" } } },
  ]);
  const res = store.transact([{ op: "delete", id: "u1" }]);
  assert.equal(res.ok, false);
  assert.equal(res.errors[0].code, "delete-referenced");
  assert.deepEqual(res.errors[0].docs[1], { id: "p1", path: "/author", value: "u1" });
  assert.equal(store.size, 2, "rejected delete leaves the store unchanged");
  // Deleting the referrer first (same batch) unblocks the target.
  const ok = store.transact([
    { op: "delete", id: "p1" },
    { op: "delete", id: "u1" },
  ]);
  assert.equal(ok.ok, true);
  assert.equal(store.size, 0);
});

test("a batch is atomic: one bad op discards the whole batch", () => {
  const { store } = makeStore();
  const res = store.transact([
    { op: "insert", doc: { id: "u1", type: "user", schemaVersion: 1, data: { name: "Ann" } } },
    { op: "insert", doc: { id: "p1", type: "post", data: { title: "One", author: "u1" } } },
    { op: "insert", doc: { id: "p2", type: "post", data: { title: 42, author: "u1" } } },
  ]);
  assert.equal(res.ok, false);
  assert.equal(res.errors[0].code, "type-mismatch");
  assert.equal(res.errors[0].instancePath, "/title");
  assert.equal(store.size, 0, "valid inserts in the same batch are rolled back");
});

test("unknown schema type is rejected", () => {
  const { store } = makeStore();
  const res = store.transact([{ op: "insert", doc: { id: "x", type: "nope", data: {} } }]);
  assert.equal(res.ok, false);
  assert.equal(res.errors[0].code, "unknown-schema");
});
