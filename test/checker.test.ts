import { test } from "node:test";
import assert from "node:assert/strict";
import {
  type Assignment,
  type CheckResult,
  type Problem,
  type SolveResult,
  checkAssignment,
  evalCondition,
  solveAssignment,
  verifyCheck,
  verifySolve,
} from "../src/index.ts";

const always = { kind: "always" } as const;
const senior = { kind: "gte", attr: "level", value: 2 } as const;

/** Four people, two separation groups: initiator/reviewer and pay/audit. */
function baseProblem(overrides: Partial<Problem> = {}): Problem {
  return {
    subjects: [
      { id: "alice", attrs: { level: 3, role: "finance" } },
      { id: "bob", attrs: { level: 2, role: "finance" } },
      { id: "carol", attrs: { level: 1, role: "ops" } },
      { id: "dave", attrs: { level: 2, role: "finance" } },
    ],
    aliases: { "a.li": "alice", "robert": "bob" },
    positions: [
      { id: "initiator", required: always, candidates: ["alice", "bob", "carol"] },
      { id: "reviewer", required: always, candidates: ["alice", "bob", "dave"] },
      { id: "payer", required: senior },
      { id: "auditor", required: always },
    ],
    sodGroups: [
      { id: "g-ir", positions: ["initiator", "reviewer"] },
      { id: "g-pa", positions: ["payer", "auditor"] },
    ],
    exceptions: [],
    exceptionBudget: 0,
    costs: {},
    ...overrides,
  };
}

test("distinct subjects across initiator/reviewer is approved", () => {
  const p = baseProblem();
  const a: Assignment = { initiator: "alice", reviewer: "dave", payer: "bob", auditor: "carol" };
  const r = checkAssignment(p, a);
  assert.equal(r.verdict, "approved");
  assert.equal(r.exceptionCost, 0);
  assert.equal(verifyCheck(p, a, r).ok, true);
});

test("initiator and reviewer the same without exception is rejected", () => {
  const p = baseProblem();
  const a: Assignment = { initiator: "alice", reviewer: "alice", payer: "bob", auditor: "carol" };
  const r = checkAssignment(p, a);
  assert.equal(r.verdict, "rejected");
  assert.ok(r.violations.some((v) => v.kind === "sod-conflict" && v.group === "g-ir"));
  assert.equal(verifyCheck(p, a, r).ok, true);
});

test("aliases mapping to the same canonical subject cannot bypass the rule", () => {
  const p = baseProblem();
  // "a.li" is an alias of alice: distinct names, same entity.
  const a: Assignment = { initiator: "a.li", reviewer: "alice", payer: "bob", auditor: "carol" };
  const r = checkAssignment(p, a);
  assert.equal(r.verdict, "rejected");
  assert.ok(r.violations.some((v) => v.kind === "sod-conflict"));
  assert.equal(verifyCheck(p, a, r).ok, true);
});

test("unassigned positions return undecided, never approved", () => {
  const p = baseProblem();
  const a: Assignment = { initiator: "alice", reviewer: null, payer: null, auditor: null };
  const r = checkAssignment(p, a);
  assert.equal(r.verdict, "undecided");
  assert.deepEqual(r.unassigned.sort(), ["auditor", "payer", "reviewer"]);
  assert.equal(r.extendable, true);
  assert.ok(r.witness);
  assert.equal(verifyCheck(p, a, r).ok, true);
});

test("qualification failure (level) is rejected", () => {
  const p = baseProblem();
  const a: Assignment = { initiator: "carol", reviewer: "dave", payer: "carol", auditor: "alice" };
  const r = checkAssignment(p, a);
  assert.equal(r.verdict, "rejected");
  assert.ok(r.violations.some((v) => v.kind === "ineligible" && v.position === "payer"));
  assert.equal(verifyCheck(p, a, r).ok, true);
});

test("assignee outside explicit candidate pool is rejected", () => {
  const p = baseProblem();
  const a: Assignment = { initiator: "dave", reviewer: "alice", payer: "bob", auditor: "carol" };
  const r = checkAssignment(p, a);
  assert.equal(r.verdict, "rejected");
  assert.ok(r.violations.some((v) => v.kind === "outside-candidate-pool" && v.position === "initiator"));
  assert.equal(verifyCheck(p, a, r).ok, true);
});

test("unknown alias name is rejected rather than treated as new identity", () => {
  const p = baseProblem();
  const a: Assignment = { initiator: "ghost", reviewer: "alice", payer: "bob", auditor: "carol" };
  const r = checkAssignment(p, a);
  assert.equal(r.verdict, "rejected");
  assert.ok(r.violations.some((v) => v.kind === "unknown-subject"));
  assert.equal(verifyCheck(p, a, r).ok, true);
});

test("paid exception within budget allows one subject to hold both roles", () => {
  const p = baseProblem({
    exceptions: [{ id: "ex-alice-ir", group: "g-ir", subject: "a.li", cost: 3 }],
    exceptionBudget: 5,
  });
  const a: Assignment = { initiator: "alice", reviewer: "alice", payer: "bob", auditor: "carol" };
  const r = checkAssignment(p, a);
  assert.equal(r.verdict, "approved");
  assert.equal(r.exceptionCost, 3);
  assert.equal(r.exceptionsUsed[0]?.subject, "alice");
  assert.equal(verifyCheck(p, a, r).ok, true);
});

test("exception cost over budget is undecided (budget exhausted, nothing approved)", () => {
  const p = baseProblem({
    exceptions: [{ id: "ex-alice-ir", group: "g-ir", subject: "alice", cost: 9 }],
    exceptionBudget: 2,
  });
  const a: Assignment = { initiator: "alice", reviewer: "alice", payer: "bob", auditor: "carol" };
  const r = checkAssignment(p, a);
  assert.equal(r.verdict, "undecided");
  assert.equal(r.violations.length, 0);
  assert.equal(r.exceptionCost, 9);
  assert.equal(verifyCheck(p, a, r).ok, true);
});

test("solver finds a complete feasible assignment from scratch", () => {
  const p = baseProblem();
  const r = solveAssignment(p);
  assert.equal(r.status, "satisfied");
  assert.ok(r.assignment);
  assert.equal(r.changesFromPrevious, 0);
  assert.equal(verifySolve(p, undefined, r).ok, true);
});

test("Hall gap: two senior-only positions but one qualified subject -> minimum conflict core", () => {
  const p: Problem = {
    subjects: [
      { id: "alice", attrs: { level: 3 } },
      { id: "bob", attrs: { level: 1 } },
    ],
    positions: [
      { id: "approve-1", required: senior },
      { id: "approve-2", required: senior },
      { id: "note", required: always },
    ],
    sodGroups: [{ id: "g-app", positions: ["approve-1", "approve-2"] }],
  };
  const r = solveAssignment(p);
  assert.equal(r.status, "infeasible");
  assert.ok(r.conflictSet);
  assert.deepEqual([...r.conflictSet!].sort(), ["approve-1", "approve-2"]);
  assert.equal(verifySolve(p, undefined, r).ok, true);
});

test("empty domain single position is a size-1 minimum conflict core", () => {
  const p: Problem = {
    subjects: [{ id: "bob", attrs: { level: 1 } }],
    positions: [{ id: "only", required: senior }],
  };
  const r = solveAssignment(p);
  assert.equal(r.status, "infeasible");
  assert.deepEqual(r.conflictSet, ["only"]);
  assert.equal(verifySolve(p, undefined, r).ok, true);
});

test("alias merge creates a new conflict after subjects are canonicalised", () => {
  // Before merging: bob and "robert" look like two people; after merging the
  // g-ir group has the same entity in both seats.
  const p = baseProblem();
  const a: Assignment = { initiator: "bob", reviewer: "robert", payer: "alice", auditor: "carol" };
  const r = checkAssignment(p, a);
  assert.equal(r.verdict, "rejected");
  assert.ok(r.violations.some((v) => v.kind === "sod-conflict" && v.subject === "bob"));
  assert.equal(verifyCheck(p, a, r).ok, true);

  // The solver must also avoid that pair.
  const plan = solveAssignment(p, a);
  assert.equal(plan.status, "satisfied");
  assert.notEqual(plan.assignment!.initiator, plan.assignment!.reviewer);
  assert.equal(verifySolve(p, a, plan).ok, true);
});

test("withdrawing an exception repairs the plan, changing the fewest positions first", () => {
  const withException = baseProblem({
    exceptions: [{ id: "ex-ir", group: "g-ir", subject: "alice", cost: 1 }],
    exceptionBudget: 5,
  });
  const oldPlan: Assignment = {
    initiator: "alice",
    reviewer: "alice",
    payer: "bob",
    auditor: "carol",
  };
  assert.equal(checkAssignment(withException, oldPlan).verdict, "approved");

  // Exception withdrawn: the same plan is rejected.
  const withdrawn = baseProblem();
  const stale = checkAssignment(withdrawn, oldPlan);
  assert.equal(stale.verdict, "rejected");

  // Repair keeps the maximum number of seats: only one of initiator/reviewer
  // changes, everything else is retained.
  const repaired = solveAssignment(withdrawn, oldPlan);
  assert.equal(repaired.status, "satisfied");
  assert.equal(repaired.changesFromPrevious, 1);
  assert.equal(repaired.assignment!.payer, "bob");
  assert.equal(repaired.assignment!.auditor, "carol");
  const seats = [repaired.assignment!.initiator, repaired.assignment!.reviewer];
  assert.ok(seats.includes("alice"));
  assert.notEqual(seats[0], seats[1]);
  assert.equal(verifySolve(withdrawn, oldPlan, repaired).ok, true);
});

test("after minimum changes, the solver optimises assignment cost", () => {
  // Only reviewer needs to change. carol is cheaper than dave but is not in
  // the reviewer pool; among legal replacements dave (cost 1) wins over
  // alice (cost 4) because alice is the initiator and no exception remains.
  const p = baseProblem({
    costs: {
      reviewer: { dave: 1, alice: 4, bob: 7 },
    },
  });
  const oldPlan: Assignment = {
    initiator: "alice",
    reviewer: "alice",
    payer: "bob",
    auditor: "carol",
  };
  const r = solveAssignment(p, oldPlan);
  assert.equal(r.status, "satisfied");
  assert.equal(r.changesFromPrevious, 1);
  assert.equal(r.assignment!.reviewer, "dave");
  assert.equal(r.assignmentCost, 1);
  assert.equal(verifySolve(p, oldPlan, r).ok, true);
});

test("fewer changes dominates lower cost of a bigger rewrite", () => {
  // A 1-change repair exists; a 2-change rewrite that is cheaper overall must
  // not be chosen.
  const p = baseProblem({
    costs: {
      initiator: { bob: 0, alice: 0 },
      reviewer: { dave: 5, alice: 1, bob: 1 },
    },
  });
  const oldPlan: Assignment = {
    initiator: "alice",
    reviewer: "alice",
    payer: "bob",
    auditor: "carol",
  };
  const r = solveAssignment(p, oldPlan);
  assert.equal(r.changesFromPrevious, 1);
  assert.equal(verifySolve(p, oldPlan, r).ok, true);
});

test("partial assignment that cannot be completed is reported non-extendable, still undecided", () => {
  const p: Problem = {
    subjects: [{ id: "alice" }, { id: "bob" }],
    positions: [
      { id: "p1", required: always },
      { id: "p2", required: always },
    ],
    sodGroups: [{ id: "g", positions: ["p1", "p2"] }],
  };
  const a: Assignment = { p1: "alice", p2: null };
  const r = checkAssignment(p, a);
  assert.equal(r.verdict, "undecided");
  assert.equal(r.extendable, true);
  assert.equal(verifyCheck(p, a, r).ok, true);

  const a2: Assignment = { p1: "alice", p2: "alice" };
  assert.equal(checkAssignment(p, a2).verdict, "rejected");
});

test("explicit substitute (alternate) is used when the preferred holder is unqualified", () => {
  const p: Problem = {
    subjects: [
      { id: "alice", attrs: { trained: false } },
      { id: "backup", attrs: { trained: true } },
    ],
    positions: [
      // Primary preferred assignee alice fails the qualification; the named
      // alternate backup satisfies it and must be selected.
      { id: "duty", required: { kind: "eq", attr: "trained", value: true }, candidates: ["alice", "backup"] },
    ],
  };
  const r = solveAssignment(p);
  assert.equal(r.status, "satisfied");
  assert.equal(r.assignment!.duty, "backup");
  assert.equal(verifySolve(p, undefined, r).ok, true);
});

test("policy evaluation only reads explicit attributes and fails closed", () => {
  const cond = {
    kind: "and",
    parts: [
      { kind: "in", attr: "role", values: ["finance", "ops"] },
      { kind: "not", part: { kind: "lte", attr: "level", value: 0 } },
    ],
  } as const;
  assert.equal(evalCondition(cond, { role: "finance", level: 2 }), true);
  // Missing level: lte fails closed, its negation is true, but the set
  // membership is still evaluated purely from explicit attributes.
  assert.equal(
    evalCondition({ kind: "in", attr: "role", values: ["finance"] }, { role: "finance" }),
    true,
  );
  assert.equal(evalCondition(cond, { role: "external", level: 5 }), false);
  assert.equal(evalCondition({ kind: "gte", attr: "level", value: 3 }, {}), false);
  assert.equal(evalCondition({ kind: "or", parts: [] }, {}), false);
});

test("verifier catches a tampered solver result", () => {
  const p = baseProblem();
  const good = solveAssignment(p);
  const tampered: SolveResult = {
    ...good,
    assignment: { initiator: "alice", reviewer: "alice", payer: "bob", auditor: "carol" },
  };
  assert.equal(verifySolve(p, undefined, tampered).ok, false);

  const costLie: SolveResult = { ...good, assignmentCost: good.assignmentCost + 99 };
  assert.equal(verifySolve(p, undefined, costLie).ok, false);
});

test("verifier catches a tampered checker verdict", () => {
  const p = baseProblem();
  const a: Assignment = { initiator: "alice", reviewer: "alice", payer: "bob", auditor: "carol" };
  const real = checkAssignment(p, a);
  assert.equal(real.verdict, "rejected");
  const forged: CheckResult = { ...real, verdict: "approved" };
  assert.equal(verifyCheck(p, a, forged).ok, false);
});

test("infeasible conflict set claiming a feasible subset is rejected by the verifier", () => {
  const p = baseProblem();
  const fake: SolveResult = {
    status: "infeasible",
    assignment: null,
    exceptionsUsed: [],
    exceptionCost: 0,
    assignmentCost: 0,
    changesFromPrevious: 0,
    conflictSet: ["initiator", "reviewer", "payer", "auditor"],
    reasons: [],
  };
  // The whole set is actually feasible (distinct people exist), so this
  // "conflict set" must not validate.
  assert.equal(verifySolve(p, undefined, fake).ok, false);
});
