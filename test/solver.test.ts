import { test } from "node:test";
import assert from "node:assert/strict";
import { evaluateModel, solve, type SolverConstraint } from "../src/solver.ts";

test("sat: difference and sum constraints solved together", () => {
  const vars = [
    { name: "s1", lo: 0, hi: 10 },
    { name: "s2", lo: 0, hi: 10 },
    { name: "s3", lo: 0, hi: 10 },
  ];
  const constraints: SolverConstraint[] = [
    { kind: "diff", id: "gap1", a: "s1", b: "s2", c: -2 },
    { kind: "diff", id: "gap2", a: "s2", b: "s3", c: -1 },
    { kind: "sum", id: "total", vars: ["s1", "s2", "s3"], op: "eq", value: 12 },
  ];
  const r = solve(vars, constraints);
  assert.equal(r.status, "sat");
  if (r.status === "sat") {
    assert.ok(evaluateModel(constraints, r.model));
    assert.equal(r.model.s1! + r.model.s2! + r.model.s3!, 12);
    assert.ok(r.model.s2! - r.model.s1! >= 2);
    assert.ok(r.model.s3! - r.model.s2! >= 1);
  }
});

test("when: conditional constraint enforced only when the condition holds", () => {
  const vars = [
    { name: "x", lo: 0, hi: 10 },
    { name: "y", lo: 0, hi: 10 },
  ];
  const when: SolverConstraint = {
    kind: "when",
    id: "w",
    cond: { op: "le", a: "x", b: 3 },
    then: { kind: "eqConst", id: "w.then", a: "y", value: 7 },
  };
  const taken = solve(vars, [when, { kind: "eqConst", id: "fx", a: "x", value: 1 }]);
  assert.equal(taken.status, "sat");
  if (taken.status === "sat") assert.equal(taken.model.y, 7);

  const notTaken = solve(vars, [when, { kind: "eqConst", id: "fx", a: "x", value: 9 }]);
  assert.equal(notTaken.status, "sat");
  if (notTaken.status === "sat") assert.notEqual(notTaken.model.y, 7, "y stays at its lower bound 0..6 region");
});

test("unsat: conflict is subset-minimal and excludes irrelevant constraints", () => {
  const vars = [
    { name: "x", lo: 0, hi: 5 },
    { name: "y", lo: 0, hi: 5 },
  ];
  const constraints: SolverConstraint[] = [
    { kind: "eqConst", id: "fix-x", a: "x", value: 5 },
    { kind: "diff", id: "x-before-y", a: "x", b: "y", c: -1 },
    { kind: "sum", id: "noise", vars: ["x", "y"], op: "ge", value: 0 },
  ];
  const r = solve(vars, constraints);
  assert.equal(r.status, "unsat");
  if (r.status !== "unsat") return;
  assert.deepEqual([...r.conflict].sort(), ["fix-x", "x-before-y"]);
  // 1-minimality: removing any single constraint from the core restores sat.
  for (const id of r.conflict) {
    const rest = constraints.filter((c) => r.conflict.includes(c.id) && c.id !== id);
    const again = solve(vars, rest);
    assert.equal(again.status, "sat", `removing ${id} should restore satisfiability`);
  }
});

test("budget-exceeded is reported distinctly from unsat and sat", () => {
  const vars = [{ name: "x", lo: 0, hi: 1_000_000 }];
  const limited = solve(vars, [], { maxNodes: 1 });
  assert.equal(limited.status, "budget-exceeded");

  const enough = solve(vars, []);
  assert.equal(enough.status, "sat");

  const contradiction = solve(
    [{ name: "x", lo: 0, hi: 10 }],
    [
      { kind: "eqConst", id: "a", a: "x", value: 1 },
      { kind: "eqConst", id: "b", a: "x", value: 2 },
    ],
    { maxNodes: 1 },
  );
  assert.equal(contradiction.status, "unsat", "propagation proves unsat before the budget matters");
});

test("empty domain is unsat with an empty conflict", () => {
  const r = solve([{ name: "x", lo: 5, hi: 3 }], []);
  assert.equal(r.status, "unsat");
  if (r.status === "unsat") assert.deepEqual(r.conflict, []);
});

test("sum with an empty variable list behaves like the empty list: total 0", () => {
  const vars = [{ name: "x", lo: 0, hi: 1 }];
  const ok = solve(vars, [{ kind: "sum", id: "s", vars: [], op: "le", value: 0 }]);
  assert.equal(ok.status, "sat");
  const bad = solve(vars, [{ kind: "sum", id: "s", vars: [], op: "ge", value: 1 }]);
  assert.equal(bad.status, "unsat");
});

test("eqVar links two variables", () => {
  const r = solve(
    [
      { name: "a", lo: 0, hi: 9 },
      { name: "b", lo: 5, hi: 12 },
    ],
    [{ kind: "eqVar", id: "link", a: "a", b: "b" }],
  );
  assert.equal(r.status, "sat");
  if (r.status === "sat") {
    assert.equal(r.model.a, r.model.b);
    assert.ok(r.model.a! >= 5 && r.model.a! <= 9);
  }
});

test("unknown variable references are rejected", () => {
  assert.throws(() =>
    solve([{ name: "x", lo: 0, hi: 1 }], [{ kind: "eqConst", id: "bad", a: "ghost", value: 0 }]),
  );
});
