import { Frac, ZERO, ONE } from "../src/fraction";
import { test, assert, assertEq, assertThrows } from "./harness";

test("fraction: parse and reduce", () => {
  assertEq(Frac.parse("3").toString(), "3");
  assertEq(Frac.parse("-7").toString(), "-7");
  assertEq(Frac.parse("3/4").toString(), "3/4");
  assertEq(Frac.parse("-2/6").toString(), "-1/3");
  assertEq(Frac.parse("6/-8").toString(), "-3/4");
  assertEq(Frac.parse("-6/-8").toString(), "3/4");
  assertEq(Frac.parse("0/5").toString(), "0");
  assertEq(Frac.parse("  +12/8 ").toString(), "3/2");
});

test("fraction: rejects garbage", () => {
  assertThrows(() => Frac.parse("1/0"));
  assertThrows(() => Frac.parse("abc"));
  assertThrows(() => Frac.parse("1/2/3"));
  assertThrows(() => Frac.parse("1.5"));
  assertThrows(() => Frac.parse(""));
});

test("fraction: arithmetic is exact", () => {
  const a = Frac.parse("1/3");
  const b = Frac.parse("1/6");
  assertEq(a.add(b).toString(), "1/2");
  assertEq(a.sub(b).toString(), "1/6");
  assertEq(a.mul(b).toString(), "1/18");
  assertEq(a.div(b).toString(), "2");
  assertEq(a.neg().toString(), "-1/3");
  assert(ONE.div(Frac.parse("4")).eq(Frac.parse("1/4")), "reciprocal");
  assert(ZERO.isZero(), "zero");
  assertEq(Frac.parse("2/3").cmp(Frac.parse("3/4")), -1);
  assertEq(Frac.parse("3/4").cmp(Frac.parse("3/4")), 0);
});

test("fraction: big integers stay exact", () => {
  const big = Frac.parse("123456789123456789/987654321987654321");
  const one = big.div(big);
  assertEq(one.toString(), "1");
  const sum = big.add(big);
  assertEq(sum.sub(big).toString(), big.toString());
});
