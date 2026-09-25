import { test } from "node:test";
import assert from "node:assert/strict";
import {
  type Assignment,
  type Problem,
  checkAssignment,
  solveAssignment,
  verifyCheck,
  verifySolve,
} from "../src/index.ts";

const always = { kind: "always" } as const;

test("every satisfied solve plan and check result is accepted by the independent verifier", () => {
  const problems: Problem[] = [
    {
      subjects: [{ id: "a" }, { id: "b" }, { id: "c" }],
      positions: [{ id: "p1", required: always }, { id: "p2", required: always }, { id: "p3", required: always }],
      sodGroups: [{ id: "g", positions: ["p1", "p2", "p3"] }],
    },
    {
      subjects: [{ id: "a", attrs: { lvl: 5 } }, { id: "b", attrs: { lvl: 1 } }],
      aliases: { alpha: "a" },
      positions: [
        { id: "p1", required: { kind: "gte", attr: "lvl", value: 3 }, candidates: ["alpha", "b"] },
        { id: "p2", required: always },
      ],
      sodGroups: [{ id: "g", positions: ["p1", "p2"] }],
      exceptionBudget: 10,
      exceptions: [{ id: "e1", group: "g", subject: "alpha", cost: 4 }],
    },
  ];
  for (const p of problems) {
    const solved = solveAssignment(p);
    assert.equal(solved.status, "satisfied");
    assert.equal(verifySolve(p, undefined, solved).ok, true);
    const checked = checkAssignment(p, solved.assignment!);
    assert.equal(checked.verdict, "approved");
    assert.equal(verifyCheck(p, solved.assignment!, checked).ok, true);
  }
});

test("minimum conflict core is itself infeasible but removing any member makes it feasible", () => {
  // Two groups create a tight puzzle: p1/p2 exclusive and p2/p3 exclusive,
  // only two subjects. Feasible (a,b,a) — so instead force the Hall gap with
  // three exclusive positions and two subjects.
  const p: Problem = {
    subjects: [{ id: "a" }, { id: "b" }],
    positions: [
      { id: "p1", required: always },
      { id: "p2", required: always },
      { id: "p3", required: always },
    ],
    sodGroups: [{ id: "g", positions: ["p1", "p2", "p3"] }],
  };
  const r = solveAssignment(p);
  assert.equal(r.status, "infeasible");
  assert.ok(r.conflictSet);
  assert.equal(r.conflictSet!.length, 3);
  assert.equal(verifySolve(p, undefined, r).ok, true);
});

test("funding one group's exception does not relax a different unfunded group", () => {
  // Three mutually exclusive positions, only two subjects. The exception that
  // covers gx cannot be spent on the separate group gy; the Hall gap on gy
  // remains a hard conflict.
  const p: Problem = {
    subjects: [{ id: "a" }, { id: "b" }],
    positions: [
      { id: "x1", required: always },
      { id: "x2", required: always },
      { id: "y1", required: always },
      { id: "y2", required: always },
      { id: "y3", required: always },
    ],
    sodGroups: [
      { id: "gx", positions: ["x1", "x2"] },
      { id: "gy", positions: ["y1", "y2", "y3"] },
    ],
    exceptions: [{ id: "e", group: "gx", subject: "a", cost: 1 }],
    exceptionBudget: 5,
  };
  const r = solveAssignment(p);
  assert.equal(r.status, "infeasible");
  assert.ok(r.conflictSet);
  assert.deepEqual([...r.conflictSet!].sort(), ["y1", "y2", "y3"]);
  assert.equal(verifySolve(p, undefined, r).ok, true);
});

test("budget exhaustion on a forced exception returns undecided, not satisfied or infeasible", () => {
  const p: Problem = {
    subjects: [{ id: "a", attrs: { ok: true } }, { id: "b", attrs: { ok: false } }],
    positions: [
      { id: "p1", required: { kind: "eq", attr: "ok", value: true } },
      { id: "p2", required: { kind: "eq", attr: "ok", value: true } },
    ],
    sodGroups: [{ id: "g", positions: ["p1", "p2"] }],
    exceptions: [{ id: "e", group: "g", subject: "a", cost: 5 }],
    exceptionBudget: 1,
  };
  const r = solveAssignment(p);
  assert.equal(r.status, "undecided");
  assert.equal(r.assignment, null);
  assert.equal(verifySolve(p, undefined, r).ok, true);
});

test("partial assignment witness completion is independently valid and respects fixed seats", () => {
  const p: Problem = {
    subjects: [{ id: "a" }, { id: "b" }, { id: "c" }],
    positions: [
      { id: "p1", required: always },
      { id: "p2", required: always },
      { id: "p3", required: always },
    ],
    sodGroups: [{ id: "g", positions: ["p1", "p2", "p3"] }],
  };
  const partial: Assignment = { p1: "a", p2: null, p3: null };
  const r = checkAssignment(p, partial);
  assert.equal(r.verdict, "undecided");
  assert.equal(r.extendable, true);
  assert.equal(r.witness!.p1, "a");
  assert.equal(verifyCheck(p, partial, r).ok, true);
});
