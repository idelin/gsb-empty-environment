import { test } from "node:test";
import assert from "node:assert/strict";
import { Store } from "../src/store.ts";

test("transaction commits atomically; readers never see intermediate state", () => {
  const store = new Store();
  const before = store.snapshot();
  const seenDuringTx: unknown[] = [];
  store.transaction((tx) => {
    tx.set("a", 1);
    seenDuringTx.push(store.get("a"), before.get("a"));
    tx.set("a", 2);
    seenDuringTx.push(store.get("a"));
  });
  assert.deepEqual(seenDuringTx, [undefined, undefined, undefined]);
  assert.equal(store.get("a"), 2);
  assert.equal(before.get("a"), undefined);
  assert.equal(before.version, 0);
  assert.equal(store.currentVersion, 1);
});

test("transaction reads its own writes", () => {
  const store = new Store();
  store.transaction((tx) => {
    tx.set("x", 10);
    assert.equal(tx.get("x"), 10);
    tx.set("x", tx.get("x") as number + 5);
    assert.equal(tx.get("x"), 15);
  });
  assert.equal(store.get("x"), 15);
});

test("op log replays to the same state", () => {
  const store = new Store();
  store.transaction((tx) => {
    tx.set("a", 1);
    tx.set("b", 2);
  });
  store.transaction((tx) => {
    tx.set("a", 3);
  });
  const replayed = Store.replay(store.log);
  assert.equal(replayed.get("a"), 3);
  assert.equal(replayed.get("b"), 2);
  assert.deepEqual(
    store.log.map((op) => [op.key, op.value]),
    [
      ["a", 1],
      ["b", 2],
      ["a", 3],
    ],
  );
});
