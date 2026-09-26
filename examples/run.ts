/**
 * Runnable tour of the library. Run with: npm run example
 */

import { compileSchema } from "../src/schema.ts";
import { compileRules } from "../src/rules.ts";
import { validate } from "../src/evaluate.ts";
import { formatIssue, type Issue } from "../src/issues.ts";
import { solve, type SolverConstraint } from "../src/solver.ts";
import { IncrementalSolver } from "../src/incremental.ts";

function show(title: string): void {
  console.log(`\n=== ${title} ===`);
}

function printIssues(issues: readonly Issue[]): void {
  if (issues.length === 0) {
    console.log("  (no issues)");
    return;
  }
  for (const issue of issues) console.log("  " + formatIssue(issue));
}

// ---------------------------------------------------------------------------
// Validation: schema + rules over a concrete instance
// ---------------------------------------------------------------------------

const schemaResult = compileSchema({
  kind: "object",
  fields: {
    start: { kind: "int" },
    end: { kind: "int" },
    member: { kind: "bool" },
    discount: { kind: "int", min: 0, max: 100 },
    items: {
      kind: "list",
      items: { kind: "object", fields: { price: { kind: "int", min: 0 } }, required: ["price"] },
    },
  },
  required: ["start", "end", "items"],
});
if (!schemaResult.ok) throw new Error("schema failed to compile");
const schema = schemaResult.value;

const rulesResult = compileRules([
  { kind: "cmp", op: "le", left: { path: "/start" }, right: { path: "/end" } },
  {
    kind: "requiredIf",
    condition: { op: "eq", left: { path: "/member" }, right: { const: true } },
    target: "/discount",
  },
  { kind: "sum", list: "/items", field: "price", op: "le", value: { const: 100 } },
]);
if (!rulesResult.ok) throw new Error("rules failed to compile");
const rules = rulesResult.value;

show("1. end < start: violation reports BOTH instance paths and the rule path");
{
  const r = validate({ start: 9, end: 5, member: false, items: [] }, schema, rules);
  console.log(`  phase reached: ${r.phase}`);
  printIssues(r.issues);
}

show("2. condition unknown (member missing): reported, not silently skipped");
{
  const r = validate({ start: 1, end: 2, items: [{ price: 10 }] }, schema, rules);
  printIssues(r.issues);
}

show("3. empty list sums to 0 (passes le 100); sum over budget is a violation");
{
  const okEmpty = validate({ start: 1, end: 2, member: true, discount: 5, items: [] }, schema, rules);
  console.log("  empty items, sum <= 100:");
  printIssues(okEmpty.issues);
  const over = validate({ start: 1, end: 2, member: true, discount: 5, items: [{ price: 60 }, { price: 50 }] }, schema, rules);
  console.log("  items 60+50, sum <= 100:");
  printIssues(over.issues);
}

show("4. missing list is NOT zero-filled: sum is unknown, not 0");
{
  const instance = { start: 1, end: 2, member: false }; // no "items" key at all
  const r = validate(instance, schema, rules);
  printIssues(r.issues);
}

show("5. type checking runs before relation evaluation");
{
  const r = validate({ start: "nine", end: 5, member: false, items: [] }, schema, rules);
  console.log(`  phase reached: ${r.phase} (relation phase skipped)`);
  printIssues(r.issues);
}

show("6. unsupported schema keywords are rejected, not ignored");
{
  const r = compileSchema({ kind: "int", minimum: 0 });
  if (!r.ok) printIssues(r.issues);
}

// ---------------------------------------------------------------------------
// Solving: finite integer domains
// ---------------------------------------------------------------------------

show("7. sat: difference + sum constraints solved together");
{
  const vars = [
    { name: "s1", lo: 0, hi: 10 },
    { name: "s2", lo: 0, hi: 10 },
    { name: "s3", lo: 0, hi: 10 },
  ];
  const constraints: SolverConstraint[] = [
    { kind: "diff", id: "gap1", a: "s1", b: "s2", c: -2 }, // s2 - s1 >= 2
    { kind: "diff", id: "gap2", a: "s2", b: "s3", c: -1 }, // s3 - s2 >= 1
    { kind: "sum", id: "total", vars: ["s1", "s2", "s3"], op: "eq", value: 12 },
    { kind: "when", id: "early", cond: { op: "le", a: "s1", b: 2 }, then: { kind: "diff", id: "early.d", a: "s3", b: "s1", c: 6 } },
  ];
  const r = solve(vars, constraints);
  console.log(" ", JSON.stringify(r));
}

show("8. unsat: subset-minimal conflict (irrelevant constraints excluded)");
{
  const vars = [
    { name: "x", lo: 0, hi: 5 },
    { name: "y", lo: 0, hi: 5 },
  ];
  const constraints: SolverConstraint[] = [
    { kind: "eqConst", id: "fix-x", a: "x", value: 5 },
    { kind: "diff", id: "x-before-y", a: "x", b: "y", c: -1 }, // y >= x + 1
    { kind: "sum", id: "noise", vars: ["x", "y"], op: "ge", value: 0 }, // irrelevant
  ];
  const r = solve(vars, constraints);
  console.log(" ", JSON.stringify(r));
}

show("9. budget-exceeded is distinct from unsat");
{
  const vars = [{ name: "x", lo: 0, hi: 1_000_000 }];
  console.log("  tiny budget: ", JSON.stringify(solve(vars, [], { maxNodes: 1 })));
  console.log("  same problem, normal budget: ", JSON.stringify(solve(vars, [])));
}

// ---------------------------------------------------------------------------
// Incremental correction + independent enumeration
// ---------------------------------------------------------------------------

show("10. incremental: retracting a constraint withdraws its influence");
{
  const store = new IncrementalSolver([
    { name: "x", lo: 0, hi: 4 },
    { name: "y", lo: 0, hi: 4 },
  ]);
  store.assert({ kind: "diff", id: "gap", a: "x", b: "y", c: -3 }); // y >= x + 3
  const fixId = store.assert({ kind: "eqConst", id: "fix", a: "x", value: 4 }); // then y >= 7: unsat
  console.log("  with both:      ", JSON.stringify(store.solve()));
  console.log("  enumeration:    ", JSON.stringify(store.verifyByEnumeration()));
  store.retract(fixId); // correction: withdraw the bad constraint
  console.log("  after retract:  ", JSON.stringify(store.solve()));
  console.log("  enumeration:    ", JSON.stringify(store.verifyByEnumeration()));
}
