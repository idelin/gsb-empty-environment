import { test } from "node:test";
import assert from "node:assert/strict";
import { OrderStatisticSkipList } from "../src/skipList.ts";

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

test("insert and select by rank in sorted order", () => {
  const list = new OrderStatisticSkipList();
  const values = [5, 1, 9, 3, 7, 1, 5];
  values.forEach((v, i) => list.insert([v, `e${i}`]));
  assert.equal(list.size, values.length);
  const sorted = [...values].sort((a, b) => a - b);
  for (let rank = 1; rank <= sorted.length; rank++) {
    assert.equal(list.select(rank)![0], sorted[rank - 1]);
  }
});

test("duplicate values stay distinct via id tie-break", () => {
  const list = new OrderStatisticSkipList();
  list.insert([1, "a"]);
  list.insert([1, "b"]);
  list.insert([1, "c"]);
  assert.equal(list.size, 3);
  assert.equal(list.remove([1, "b"]), true);
  assert.equal(list.size, 2);
  assert.deepEqual(
    list.toArray().map((k) => k[1]),
    ["a", "c"],
  );
  assert.equal(list.remove([1, "b"]), false);
});

test("remove returns false for missing keys", () => {
  const list = new OrderStatisticSkipList();
  list.insert([2, "x"]);
  assert.equal(list.remove([3, "x"]), false);
  assert.equal(list.remove([2, "y"]), false);
  assert.equal(list.size, 1);
});

test("select out of range returns null", () => {
  const list = new OrderStatisticSkipList();
  list.insert([1, "a"]);
  assert.equal(list.select(0), null);
  assert.equal(list.select(2), null);
});

test("randomized insert/remove/select matches sorted-array model", () => {
  const rand = mulberry32(42);
  const list = new OrderStatisticSkipList();
  const model = new Map<string, number>();
  for (let op = 0; op < 5000; op++) {
    const id = `e${Math.floor(rand() * 500)}`;
    if (rand() < 0.6) {
      if (!model.has(id)) {
        const value = Math.floor(rand() * 1000);
        model.set(id, value);
        list.insert([value, id]);
      }
    } else if (model.has(id)) {
      list.remove([model.get(id)!, id]);
      model.delete(id);
    }
    if (op % 50 === 0) {
      const sorted = [...model.entries()].sort(
        (a, b) => a[1] - b[1] || (a[0] < b[0] ? -1 : 1),
      );
      assert.equal(list.size, sorted.length);
      if (sorted.length > 0) {
        const rank = 1 + Math.floor(rand() * sorted.length);
        assert.deepEqual(list.select(rank), [sorted[rank - 1][1], sorted[rank - 1][0]]);
        assert.deepEqual(list.toArray(), sorted.map(([id, value]) => [value, id]));
      }
    }
  }
});
