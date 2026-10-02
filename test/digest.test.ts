import { test } from "node:test";
import assert from "node:assert/strict";
import { DigestRing, contributionOf, intervalOf } from "../src/digest.ts";
import { INTERVAL_COUNT } from "../src/membership.ts";

test("增量哈希环：与插入顺序无关", () => {
  const a = new DigestRing();
  const b = new DigestRing();
  const items = [101, 202, 303, 404];
  for (const c of items) a.add(c);
  for (const c of [...items].reverse()) b.add(c);
  assert.equal(a.digest, b.digest);
  assert.equal(a.count, 4);
});

test("增量哈希环：O(1) 增删改", () => {
  const ring = new DigestRing();
  const c1 = contributionOf("n1", "ALIVE", 1);
  const c2 = contributionOf("n2", "ALIVE", 1);
  ring.add(c1);
  ring.add(c2);
  const before = ring.digest;
  ring.remove(c2); // 删除 n2 后应回到仅含 c1 的状态
  assert.equal(ring.digest, c1);
  assert.equal(ring.count, 1);
  ring.add(c2);
  assert.equal(ring.digest, before);
  const c2new = contributionOf("n2", "DEAD", 2);
  ring.replace(c2, c2new); // 原地更新，无需全量重算
  const fresh = new DigestRing();
  fresh.add(c1);
  fresh.add(c2new);
  assert.equal(ring.digest, fresh.digest);
});

test("区间映射稳定且在界内", () => {
  for (const id of ["A1", "B4", "node-xyz"]) {
    const i = intervalOf(id, INTERVAL_COUNT);
    assert.ok(i >= 0 && i < INTERVAL_COUNT);
    assert.equal(i, intervalOf(id, INTERVAL_COUNT));
  }
});
