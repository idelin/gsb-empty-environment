import { test } from "node:test";
import assert from "node:assert/strict";
import { checkOptimal, checkInfeasible, checkUnbounded } from "../src/checker.ts";
import { parseRat as R } from "../src/rational.ts";
import { lp } from "./helpers.ts";

test("检查器拒绝不可行的 x", () => {
  const problem = lp(["1", "1"], [["1", "1"]], ["1"]);
  const r = checkOptimal(problem, [R("2"), R("0")], [R("1")]);
  assert.equal(r.ok, false);
  assert.ok(r.violations.some((v) => v.includes("constraint 0")));
});

test("检查器拒绝负的对偶分量", () => {
  const problem = lp(["1"], [["1"]], ["1"]);
  const r = checkOptimal(problem, [R("1")], [R("-1")]);
  assert.equal(r.ok, false);
  assert.ok(r.violations.some((v) => v.includes("y[0]")));
});

test("检查器拒绝不满足强对偶的证书", () => {
  const problem = lp(["1"], [["1"]], ["1"]);
  // x=1 可行，y=1/2 对偶可行但 b·y=1/2 != 1=c·x
  const r = checkOptimal(problem, [R("1")], [R("1/2")]);
  assert.equal(r.ok, false);
  assert.ok(r.violations.some((v) => v.includes("strong duality")));
});

test("检查器拒绝无效的 Farkas 证书（b·y >= 0）", () => {
  const problem = lp(["1"], [["1"], ["-1"]], ["1", "-2"]);
  const r = checkInfeasible(problem, [R("1"), R("0")]);
  assert.equal(r.ok, false);
});

test("检查器拒绝 Aᵀy 有负分量的 Farkas 证书", () => {
  const problem = lp(["1"], [["1"], ["-1"]], ["1", "-2"]);
  const r = checkInfeasible(problem, [R("0"), R("1")]);
  assert.equal(r.ok, false);
  assert.ok(r.violations.some((v) => v.includes("Aᵀy")));
});

test("检查器拒绝 c·d <= 0 的射线", () => {
  const problem = lp(["-1"], [["-1"]], ["1"]);
  const r = checkUnbounded(problem, [R("0")], [R("1")]);
  assert.equal(r.ok, false);
  assert.ok(r.violations.some((v) => v.includes("c·d")));
});

test("检查器拒绝 Ad 有正分量的射线", () => {
  const problem = lp(["1"], [["1"]], ["1"]);
  const r = checkUnbounded(problem, [R("0")], [R("1")]);
  assert.equal(r.ok, false);
  assert.ok(r.violations.some((v) => v.includes("(Ad)")));
});

test("检查器拒绝不可行的无界基点", () => {
  const problem = lp(["1", "1"], [["1", "-1"]], ["1"]);
  const r = checkUnbounded(problem, [R("5"), R("0")], [R("1"), R("1")]);
  assert.equal(r.ok, false);
});
