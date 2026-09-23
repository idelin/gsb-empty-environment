import { test } from "node:test";
import assert from "node:assert/strict";
import { Store } from "../src/index.ts";
import type { Op } from "../src/index.ts";

interface Counter {
  total: number;
  tags: string[];
}

const reduce = (state: Counter, op: Op): Counter => {
  switch (op.type) {
    case "add":
      return { ...state, total: state.total + (op.n as number) };
    case "tag":
      return { ...state, tags: [...state.tags, op.tag as string] };
    default:
      return state;
  }
};

const initial: Counter = { total: 0, tags: [] };

test("replay from the op log reproduces the committed snapshot", () => {
  const store = new Store(reduce, initial);
  store.transact([{ type: "add", n: 2 }]);
  store.transact([{ type: "tag", tag: "x" }, { type: "add", n: 3 }]);
  assert.deepEqual(store.replay(), store.snapshot());
  assert.deepEqual(store.snapshot(), { total: 5, tags: ["x"] });
});

test("readers never observe a transaction's intermediate state", () => {
  const store = new Store(reduce, initial);
  const seen: Counter[] = [];
  store.subscribe((snapshot) => seen.push(snapshot));
  store.transact([{ type: "add", n: 1 }, { type: "add", n: 10 }]);
  // Both notifications carry the final committed snapshot; the intermediate
  // { total: 1 } state is never observable.
  assert.equal(seen.length, 2);
  assert.equal(seen[0].total, 11);
  assert.equal(seen[1].total, 11);
  assert.equal(store.snapshot().total, 11);
});

test("committed snapshots are deeply frozen", () => {
  const store = new Store(reduce, initial);
  store.transact([{ type: "tag", tag: "frozen" }]);
  const snapshot = store.snapshot();
  assert.throws(() => {
    (snapshot as { total: number }).total = 99;
  }, TypeError);
  assert.throws(() => {
    (snapshot.tags as string[]).push("nope");
  }, TypeError);
  assert.equal(store.snapshot().total, 0);
});

test("older snapshots are not mutated by later transactions", () => {
  const store = new Store(reduce, initial);
  store.transact([{ type: "add", n: 1 }]);
  const before = store.snapshot();
  store.transact([{ type: "add", n: 5 }, { type: "tag", tag: "later" }]);
  assert.deepEqual(before, { total: 1, tags: [] });
  assert.equal(store.snapshot().total, 6);
});

test("unsubscribe is idempotent", () => {
  const store = new Store(reduce, initial);
  let calls = 0;
  const off = store.subscribe(() => calls++);
  store.transact([{ type: "add", n: 1 }]);
  off();
  off();
  store.transact([{ type: "add", n: 1 }]);
  assert.equal(calls, 1);
});
