import { test } from "node:test";
import assert from "node:assert/strict";
import { rat, fromString } from "../src/rational.ts";
import { interval, point, contains, intersect, intervalToString } from "../src/interval.ts";

test("开闭端点判定", () => {
  const iv = interval(rat(0n), true, rat(1n), false); // [0, 1)
  assert.ok(contains(iv, rat(0n)));
  assert.ok(!contains(iv, rat(1n)));
  assert.ok(contains(iv, fromString("0.999")));
  assert.equal(intervalToString(iv), "[0, 1)");
});

test("交集保留端点开闭", () => {
  const a = interval(rat(0n), false, rat(2n), true); // (0, 2]
  const b = interval(rat(0n), true, rat(1n), false); // [0, 1)
  const c = intersect(a, b);
  assert.ok(c !== null && !c.lo!.closed && !c.hi!.closed); // (0, 1)
  assert.equal(intervalToString(c!), "(0, 1)");
  assert.equal(intersect(point(rat(1n)), interval(rat(1n), false, rat(2n), true)), null);
});

test("空区间构造抛错", () => {
  assert.throws(() => interval(rat(1n), true, rat(1n), false)); // [1, 1) 为空
  assert.throws(() => interval(rat(2n), true, rat(1n), true));
});
