import { Frac } from "../src/fraction";
import { LP, solve, SolveResult } from "../src/simplex";
import { checkResult } from "../src/checker";
import { enumerateVertices } from "../src/enumerate";
import { test, assert, assertEq } from "./harness";

function lp(c: string[], A: string[][], b: string[]): LP {
  return {
    c: c.map(Frac.parse),
    A: A.map((row) => row.map(Frac.parse)),
    b: b.map(Frac.parse),
  };
}

function solved(res: SolveResult, problem: LP): SolveResult {
  const check = checkResult(problem, res);
  assert(
    check.ok,
    `certificate rejected: ${check.errors.join("; ")} (status ${res.status})`
  );
  return res;
}

test("simplex: trivial bounded problem", () => {
  const p = lp(["1"], [["1"]], ["5"]);
  const r = solved(solve(p), p);
  assertEq(r.status, "optimal");
  assertEq(r.value!.toString(), "5");
  assertEq(r.x![0].toString(), "5");
  assertEq(r.y![0].toString(), "1");
});

test("simplex: fractional coefficients", () => {
  // max x+y s.t. 2x+y <= 4, x+2y <= 3  ->  x=5/3, y=2/3, value 7/3
  const p = lp(["1", "1"], [["2", "1"], ["1", "2"]], ["4", "3"]);
  const r = solved(solve(p), p);
  assertEq(r.status, "optimal");
  assertEq(r.value!.toString(), "7/3");
  assertEq(r.x![0].toString(), "5/3");
  assertEq(r.x![1].toString(), "2/3");
});

test("simplex: negative b, feasible (needs phase 1)", () => {
  // max x s.t. -x <= -1 (i.e. x >= 1), x <= 3  ->  3
  const p = lp(["1"], [["-1"], ["1"]], ["-1", "3"]);
  const r = solved(solve(p), p);
  assertEq(r.status, "optimal");
  assertEq(r.value!.toString(), "3");
});

test("simplex: infeasible with Farkas certificate", () => {
  // x <= 1 and x >= 2
  const p = lp(["1"], [["1"], ["-1"]], ["1", "-2"]);
  const r = solved(solve(p), p);
  assertEq(r.status, "infeasible");
  assert(r.y!.every((f) => f.sign() >= 0), "y >= 0");
  assert(
    r.y![0].mul(Frac.parse("1")).add(r.y![1].mul(Frac.parse("-2"))).sign() < 0,
    "b·y < 0"
  );
});

test("simplex: unbounded with ray", () => {
  // max x+y s.t. x - y <= 1  (ray d=(1,1))
  const p = lp(["1", "1"], [["1", "-1"]], ["1"]);
  const r = solved(solve(p), p);
  assertEq(r.status, "unbounded");
  assert(r.d!.some((f) => !f.isZero()), "nonzero ray");
});

test("simplex: no constraints", () => {
  const unb = solved(solve(lp(["2", "-1"], [], [])), lp(["2", "-1"], [], []));
  assertEq(unb.status, "unbounded");
  const opt = solved(solve(lp(["-2", "-1"], [], [])), lp(["-2", "-1"], [], []));
  assertEq(opt.status, "optimal");
  assertEq(opt.value!.toString(), "0");
  assertEq(opt.y!.length, 0);
});

test("simplex: zero variables", () => {
  const p1 = lp([], [[], []], ["1", "2"]);
  const feasible = solved(solve(p1), p1);
  assertEq(feasible.status, "optimal");
  assertEq(feasible.value!.toString(), "0");
  assertEq(feasible.x!.length, 0);
  const p2 = lp([], [[]], ["-1"]);
  const infeasible = solved(solve(p2), p2);
  assertEq(infeasible.status, "infeasible");
  assertEq(infeasible.y!.length, 1);
});

test("simplex: redundant rows (linearly dependent, negative b)", () => {
  // -x <= -1, -2x <= -2 (redundant), x <= 3 ; max x -> 3
  const p = lp(["1"], [["-1"], ["-2"], ["1"]], ["-1", "-2", "3"]);
  const r = solved(solve(p), p);
  assertEq(r.status, "optimal");
  assertEq(r.value!.toString(), "3");
  assertEq(r.y!.length, 3);
});

test("simplex: degenerate vertex", () => {
  // max x+y s.t. x<=1, y<=1, x+y<=2  ->  vertex (1,1) is degenerate
  const p = lp(["1", "1"], [["1", "0"], ["0", "1"], ["1", "1"]], ["1", "1", "2"]);
  const r = solved(solve(p), p);
  assertEq(r.status, "optimal");
  assertEq(r.value!.toString(), "2");
});

test("simplex: multiple optimal solutions", () => {
  // max x+y s.t. x+y <= 1  ->  every point of the edge is optimal
  const p = lp(["1", "1"], [["1", "1"]], ["1"]);
  const r = solved(solve(p), p);
  assertEq(r.status, "optimal");
  assertEq(r.value!.toString(), "1");
  const en = enumerateVertices(p);
  const optima = en.vertices.filter((v) =>
    v[0].add(v[1]).eq(Frac.parse("1"))
  );
  assert(optima.length >= 2, "edge of optimal vertices");
});

test("simplex: Beale cycling example terminates under Bland", () => {
  const p = lp(
    ["3/4", "-150", "1/50", "-6"],
    [
      ["1/4", "-60", "-1/25", "9"],
      ["1/2", "-90", "-1/50", "3"],
      ["0", "0", "1", "0"],
    ],
    ["0", "0", "1"]
  );
  const r = solved(solve(p), p);
  assertEq(r.status, "optimal");
  assertEq(r.value!.toString(), "1/20");
  const en = enumerateVertices(p);
  assertEq(en.maxValue!.toString(), "1/20");
});

test("simplex: zero objective is immediately optimal", () => {
  const p = lp(["-1"], [["1"]], ["4"]);
  const r = solved(solve(p), p);
  assertEq(r.status, "optimal");
  assertEq(r.value!.toString(), "0");
  assertEq(r.y![0].toString(), "0");
});

test("simplex: budget exhaustion is reported, not misclassified", () => {
  const p = lp(["1"], [["1"]], ["5"]); // needs exactly 1 pivot
  const r0 = solve(p, 0);
  assertEq(r0.status, "budget_exhausted");
  const r1 = solved(solve(p, 1), p);
  assertEq(r1.status, "optimal");
  assertEq(r1.value!.toString(), "5");
});

test("simplex: budget covers phase-1 pivots too", () => {
  const p = lp(["1"], [["-1"], ["1"]], ["-1", "3"]);
  const r = solve(p, 0);
  assertEq(r.status, "budget_exhausted");
});

test("checker: rejects tampered optimal certificate", () => {
  const p = lp(["1", "1"], [["2", "1"], ["1", "2"]], ["4", "3"]);
  const r = solve(p);
  assertEq(r.status, "optimal");
  const bad1: SolveResult = { ...r, x: [r.x![0], r.x![1].add(Frac.parse("1"))] };
  assert(!checkResult(p, bad1).ok, "infeasible x rejected");
  const bad2: SolveResult = { ...r, y: r.y!.map((f) => f.add(Frac.parse("1"))) };
  assert(!checkResult(p, bad2).ok, "broken strong duality rejected");
  const bad3: SolveResult = { ...r, value: r.value!.add(Frac.parse("1")) };
  assert(!checkResult(p, bad3).ok, "wrong value rejected");
});

test("checker: rejects tampered infeasible certificate", () => {
  const p = lp(["1"], [["1"], ["-1"]], ["1", "-2"]);
  const r = solve(p);
  assertEq(r.status, "infeasible");
  const bad: SolveResult = { ...r, y: r.y!.map((f) => f.neg()) };
  assert(!checkResult(p, bad).ok, "negated Farkas y rejected");
});

test("checker: rejects tampered unbounded ray", () => {
  const p = lp(["1", "1"], [["1", "-1"]], ["1"]);
  const r = solve(p);
  assertEq(r.status, "unbounded");
  const bad: SolveResult = { ...r, d: r.d!.map((f) => f.neg()) };
  assert(!checkResult(p, bad).ok, "negated ray rejected");
});
