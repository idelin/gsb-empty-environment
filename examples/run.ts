/**
 * Runnable walkthrough:
 *   node examples/run.ts
 *
 * Scenarios:
 *  1. One-operation checks — same initiator/reviewer, alias merge, unassigned.
 *  2. Hall gap — exact solver reports the minimum conflicting position set.
 *  3. Alias merge creates a conflict after canonicalisation.
 *  4. Withdrawing an exception repairs the plan: fewest changes, then cost.
 *  5. Budget exhausted -> undecided only.
 */
import {
  type Assignment,
  type CheckResult,
  type Problem,
  checkAssignment,
  solveAssignment,
  verifyCheck,
  verifySolve,
} from "../src/index.ts";

const always = { kind: "always" } as const;
const senior = { kind: "gte", attr: "level", value: 2 } as const;

function heading(title: string): void {
  console.log(`\n=== ${title} ===`);
}

function showCheck(label: string, problem: Problem, assignment: Assignment): CheckResult {
  const result = checkAssignment(problem, assignment);
  const verification = verifyCheck(problem, assignment, result);
  console.log(`- ${label}: ${result.verdict.toUpperCase()}`);
  for (const reason of result.reasons) console.log(`    ${reason}`);
  console.log(`    verifier: ${verification.ok ? "ok" : `FAIL ${verification.errors.join("; ")}`}`);
  return result;
}

const paymentProblem: Problem = {
  subjects: [
    { id: "alice", attrs: { level: 3, role: "finance" } },
    { id: "bob", attrs: { level: 2, role: "finance" } },
    { id: "carol", attrs: { level: 1, role: "ops" } },
    { id: "dave", attrs: { level: 2, role: "finance" } },
  ],
  aliases: { "a.li": "alice", robert: "bob" },
  positions: [
    { id: "initiator", required: always, candidates: ["alice", "bob", "carol"] },
    { id: "reviewer", required: always, candidates: ["alice", "bob", "dave"] },
    { id: "payer", required: senior },
    { id: "auditor", required: always },
  ],
  sodGroups: [
    { id: "g-init-review", positions: ["initiator", "reviewer"] },
    { id: "g-pay-audit", positions: ["payer", "auditor"] },
  ],
  exceptionBudget: 0,
};

heading("1. one-operation checks");
const good: Assignment = { initiator: "alice", reviewer: "dave", payer: "bob", auditor: "carol" };
showCheck("distinct people", paymentProblem, good);

const samePerson: Assignment = { initiator: "alice", reviewer: "alice", payer: "bob", auditor: "carol" };
showCheck("same initiator and reviewer, no exception", paymentProblem, samePerson);

const aliasBypass: Assignment = { initiator: "a.li", reviewer: "alice", payer: "bob", auditor: "carol" };
showCheck("alias a.li vs alice (same entity)", paymentProblem, aliasBypass);

const partial: Assignment = { initiator: "alice", reviewer: null, payer: null, auditor: null };
const partialResult = showCheck("partial assignment (rest unassigned)", paymentProblem, partial);
console.log(`    extendable: ${partialResult.extendable}, witness: ${JSON.stringify(partialResult.witness)}`);

heading("2. Hall gap -> minimum conflicting position set");
const hallProblem: Problem = {
  subjects: [
    { id: "alice", attrs: { level: 3 } },
    { id: "bob", attrs: { level: 1 } },
  ],
  positions: [
    { id: "approval-a", required: senior },
    { id: "approval-b", required: senior },
    { id: "note", required: always },
  ],
  sodGroups: [{ id: "g-approval", positions: ["approval-a", "approval-b"] }],
};
const hall = solveAssignment(hallProblem);
console.log(`- status: ${hall.status}`);
console.log(`- conflict set: ${JSON.stringify(hall.conflictSet)}`);
for (const reason of hall.reasons) console.log(`    ${reason}`);
console.log(`- verifier: ${verifySolve(hallProblem, undefined, hall).ok ? "ok" : "FAIL"}`);

heading("3. alias merge surfaces a new conflict");
const merged: Assignment = { initiator: "bob", reviewer: "robert", payer: "alice", auditor: "carol" };
showCheck("bob vs robert before canonicalisation looked distinct", paymentProblem, merged);
const replanned = solveAssignment(paymentProblem, merged);
console.log(`- replan status: ${replanned.status}, assignment: ${JSON.stringify(replanned.assignment)}`);
console.log(`- changed positions: ${replanned.changesFromPrevious}`);
console.log(`- verifier: ${verifySolve(paymentProblem, merged, replanned).ok ? "ok" : "FAIL"}`);

heading("4. withdraw exception -> repair with fewest changes, then lowest cost");
const funded: Problem = {
  ...paymentProblem,
  exceptions: [{ id: "ex-ir", group: "g-init-review", subject: "alice", cost: 2 }],
  exceptionBudget: 5,
  costs: { reviewer: { dave: 1, alice: 4, bob: 7 } },
};
const oldPlan: Assignment = { initiator: "alice", reviewer: "alice", payer: "bob", auditor: "carol" };
showCheck("old plan while exception exists", funded, oldPlan);
const withdrawn: Problem = { ...paymentProblem, costs: funded.costs };
showCheck("old plan after exception withdrawn", withdrawn, oldPlan);
const repaired = solveAssignment(withdrawn, oldPlan);
console.log(`- repaired: ${JSON.stringify(repaired.assignment)}`);
console.log(`- changed: ${repaired.changesFromPrevious}, assignment cost: ${repaired.assignmentCost}`);
console.log(`- verifier: ${verifySolve(withdrawn, oldPlan, repaired).ok ? "ok" : "FAIL"}`);

heading("5. forced exception but budget exhausted -> undecided only");
const expensive: Problem = {
  ...paymentProblem,
  exceptions: [{ id: "ex-ir", group: "g-init-review", subject: "alice", cost: 8 }],
  exceptionBudget: 2,
};
showCheck("collision needs cost 8, budget 2", expensive, samePerson);
