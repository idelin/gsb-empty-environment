import { test } from "node:test";
import assert from "node:assert/strict";
import { validateAll } from "../src/validator.ts";
import type { Op } from "../src/store.ts";
import { makeStore, normalizeReverse } from "./helpers.ts";

/** Deterministic PRNG so failures reproduce. */
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function pick<T>(rand: () => number, xs: T[]): T {
  return xs[Math.floor(rand() * xs.length)];
}

test("random op batches cross-checked against the independent full validator", () => {
  const rand = mulberry32(20261001);
  const { registry, store } = makeStore();
  let counter = 0;
  const freshId = (prefix: string) => `${prefix}-${counter++}`;

  const existingIds = () => store.snapshot().map((d) => d.id);
  const usersWithoutEditor = () =>
    store
      .snapshot()
      .filter((d) => d.type === "user")
      .map((d) => d.id)
      .filter((id) => !store.incoming(id).some((o) => o.rulePath === "/properties/editor"));

  const randomUserData = (ids: string[], dangling: boolean) => {
    const data: Record<string, unknown> = { name: `user-${counter}` };
    if (rand() < 0.5) {
      data.friend = dangling ? freshId("ghost") : ids.length > 0 ? pick(rand, ids) : undefined;
      if (data.friend === undefined) delete data.friend;
    }
    return data;
  };

  const randomValidOp = (): Op | null => {
    const ids = existingIds();
    const users = store.snapshot().filter((d) => d.type === "user").map((d) => d.id);
    const posts = store.snapshot().filter((d) => d.type === "post").map((d) => d.id);
    const choice = rand();
    if (choice < 0.35) {
      return { op: "insert", doc: { id: freshId("u"), type: "user", schemaVersion: 1, data: randomUserData(ids, false) } };
    }
    if (choice < 0.6 && users.length > 0) {
      const data: Record<string, unknown> = { title: `post-${counter}`, author: pick(rand, users) };
      const freeEditors = usersWithoutEditor();
      if (freeEditors.length > 0 && rand() < 0.5) data.editor = pick(rand, freeEditors);
      return { op: "insert", doc: { id: freshId("p"), type: "post", data } };
    }
    if (choice < 0.75 && posts.length > 0) {
      const data: Record<string, unknown> = { text: "hi", post: pick(rand, posts) };
      if (users.length > 0 && rand() < 0.5) data.author = pick(rand, users);
      return { op: "insert", doc: { id: freshId("c"), type: "comment", data } };
    }
    if (choice < 0.9 && ids.length > 0) {
      return { op: "rename", id: pick(rand, ids), newId: freshId("r") };
    }
    if (ids.length > 0) {
      return { op: "delete", id: pick(rand, ids) };
    }
    return null;
  };

  const randomInvalidOp = (): Op => {
    const ids = existingIds();
    const choice = rand();
    if (choice < 0.25 && ids.length > 0) {
      return { op: "insert", doc: { id: pick(rand, ids), type: "user", schemaVersion: 1, data: { name: "dup" } } };
    }
    if (choice < 0.5) {
      return { op: "insert", doc: { id: freshId("u"), type: "user", schemaVersion: 1, data: randomUserData(ids, true) } };
    }
    if (choice < 0.7) {
      return { op: "insert", doc: { id: freshId("u"), type: "user", schemaVersion: 1, data: { name: 42 } } };
    }
    if (choice < 0.85) {
    return { op: "insert", doc: { id: freshId("x"), type: "user", schemaVersion: 1, data: { name: "x", unknown: 1 } } };
    }
    return { op: "delete", id: freshId("ghost") };
  };

  let committed = 0;
  let rejected = 0;
  for (let round = 0; round < 300; round++) {
    const beforeDocs = JSON.stringify(store.snapshot().map((d) => [d.id, d.type, d.schemaVersion, d.data]).sort());
    const beforeReverse = normalizeReverse(store.reverseIndex());

    const ops: Op[] = [];
    const n = 1 + Math.floor(rand() * 4);
    for (let i = 0; i < n; i++) {
      const op = rand() < 0.75 ? randomValidOp() : randomInvalidOp();
      if (op !== null) ops.push(op);
    }
    if (ops.length === 0) continue;

    const res = store.transact(ops);
    if (res.ok) committed++;
    else rejected++;

    // Invariant 1: the store is always fully valid after any transaction.
    const full = validateAll(registry, store.snapshot());
    assert.equal(
      full.errors.length,
      0,
      `round ${round}: full validator disagrees after ${res.ok ? "commit" : "reject"}: ${JSON.stringify(full.errors.slice(0, 2))}`,
    );
    // Invariant 2: the incremental reverse index matches a from-scratch rebuild.
    assert.deepEqual(
      normalizeReverse(store.reverseIndex()),
      normalizeReverse(full.reverse),
      `round ${round}: reverse index drifted`,
    );
    // Invariant 3: a rejected batch changes nothing (atomicity).
    if (!res.ok) {
      const afterDocs = JSON.stringify(store.snapshot().map((d) => [d.id, d.type, d.schemaVersion, d.data]).sort());
      assert.equal(afterDocs, beforeDocs, `round ${round}: rejected batch mutated documents`);
      assert.deepEqual(normalizeReverse(store.reverseIndex()), beforeReverse, `round ${round}: rejected batch mutated the reverse index`);
    }
  }

  assert.ok(committed > 50, `expected plenty of committed batches, got ${committed}`);
  assert.ok(rejected > 20, `expected plenty of rejected batches, got ${rejected}`);
  console.log(`fuzz: ${committed} committed, ${rejected} rejected, final size ${store.size}`);
});
