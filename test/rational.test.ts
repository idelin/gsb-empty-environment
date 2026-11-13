import { test } from "node:test";
import assert from "node:assert/strict";
import { Rat } from "../src/rational.ts";

test("约分与符号规范化", () => {
  const r = Rat.of(2n, 4n);
  assert.equal(r.n, 1n);
  assert.equal(r.d, 2n);
  const neg = Rat.of(3n, -6n);
  assert.equal(neg.n, -1n);
  assert.equal(neg.d, 2n);
  assert.equal(Rat.of(0n, 7n).d, 1n);
});

test("分母为零拒绝", () => {
  assert.throws(() => Rat.of(1n, 0n), RangeError);
});

test("四则运算保持精确", () => {
  const a = Rat.of(1n, 3n);
  const b = Rat.of(1n, 6n);
  assert.equal(a.add(b).toString(), "1/2");
  assert.equal(a.sub(b).toString(), "1/6");
  assert.equal(a.mul(b).toString(), "1/18");
  assert.equal(a.div(b).toString(), "2/1");
  assert.equal(a.scale(9n).toString(), "3/1");
  assert.throws(() => a.div(Rat.zero()), RangeError);
});

test("floor 对负数向下取整", () => {
  assert.equal(Rat.of(7n, 2n).floor(), 3n);
  assert.equal(Rat.of(-7n, 2n).floor(), -4n);
  assert.equal(Rat.of(-4n, 2n).floor(), -2n);
});

test("比较", () => {
  assert.equal(Rat.of(1n, 3n).cmp(Rat.of(2n, 6n)), 0);
  assert.equal(Rat.of(1n, 3n).cmp(Rat.of(1n, 2n)), -1);
  assert.ok(Rat.of(1n, 10n).isPositive());
  assert.ok(!Rat.of(-1n, 10n).isPositive());
});
