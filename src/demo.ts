/**
 * Runnable demonstration:
 *
 *   node --experimental-strip-types src/demo.ts
 */
import type { Node, Schema } from "./index.ts";
import {
  boolRef,
  cmpNode,
  constNode,
  freshSrc,
  inNode,
  notNode,
  andNode,
  orNode,
  render,
} from "./index.ts";
import { simplify } from "./simplify.ts";
import { checkEquivalence, formatReport, testSimplification } from "./tester.ts";
import { evaluate } from "./eval.ts";
import { triName } from "./trivalue.ts";

const schema: Schema = {
  isAdmin: { kind: "bool" },
  mfa: { kind: "bool" },
  age: { kind: "int", min: 0, max: 120 },
  tier: { kind: "finite", values: ["free", "pro", "ent"] },
};

function header(title: string): void {
  console.log("\n=== " + title + " ===");
}

function show(label: string, expr: Node): void {
  const { expr: simplified, lineage } = simplify(expr, schema);
  console.log(`[${label}]`);
  console.log("  before: " + render(expr));
  console.log("  after : " + render(simplified));
  console.log(formatReport(testSimplification(expr, schema)));
  if (lineage.length === 0) console.log("  (no rewrite applied)");
}

// 1. x OR NOT x on a nullable boolean: must stay intact.
header("1. excluded middle on a nullable domain (MUST NOT collapse)");
{
  const s = freshSrc("isAdmin");
  const expr = orNode([boolRef("isAdmin", s), notNode(boolRef("isAdmin"))]);
  const simplified = simplify(expr, schema).expr;
  console.log("  before: " + render(expr));
  console.log("  after : " + render(simplified));
  console.log("  isAdmin MISSING -> " + triName(evaluate(expr, {}, schema)) +
    " (NOT true: law of excluded middle fails on lossy domain)");
  const unsound = constNode(1); // the tempting rewrite x OR NOT x -> true
  console.log("  if a tool rewrote it to `true`:");
  console.log(
    formatReport(checkEquivalence(expr, unsound, schema))
      .split("\n")
      .map((l) => "    " + l)
      .join("\n"),
  );
}

// 2. false AND ... eliminates the branch (provenance kept).
header("2. false branch elimination in AND (with provenance)");
show(
  "false AND isAdmin AND age>=18",
  andNode([
    constNode(0, new Set([freshSrc("deny-rule")])),
    boolRef("isAdmin", freshSrc("isAdmin")),
    cmpNode("age", ">=", 18, freshSrc("age-policy")),
  ]),
);

// 3. interval intersection: age>=18 AND age<30 -> [18,29]; unsatisfiable -> empty.
header("3. integer interval algebra (intersection / union)");
show("age>=18 AND age<30", andNode([cmpNode("age", ">=", 18, freshSrc("a1")), cmpNode("age", "<", 30, freshSrc("a2"))]));
show("age<10 AND age>20 (empty box, missing stays MISSING)", andNode([cmpNode("age", "<", 10, freshSrc("a3")), cmpNode("age", ">", 20, freshSrc("a4"))]));
show("age<18 OR age>=65", orNode([cmpNode("age", "<", 18, freshSrc("a5")), cmpNode("age", ">=", 65, freshSrc("a6"))]));

// 4. finite-set membership: intersection / union / complement.
header("4. finite-set algebra");
show("tier∈{pro,ent} AND tier∈{ent,free}", andNode([inNode("tier", ["pro", "ent"], freshSrc("t1")), inNode("tier", ["ent", "free"], freshSrc("t2"))]));
show("NOT(tier∈{free})", notNode(inNode("tier", ["free"], freshSrc("t3"))));
show("tier∈{free} OR tier∈{pro,ent} (whole universe, missing stays MISSING)", orNode([inNode("tier", ["free"], freshSrc("t4")), inNode("tier", ["pro", "ent"], freshSrc("t5"))]));

// 5. double negation, idempotence, De Morgan, constants.
header("5. boolean algebra moves (all three-valued safe)");
show("NOT(NOT mfa)", notNode(notNode(boolRef("mfa", freshSrc("m1")))));
show("isAdmin AND isAdmin", andNode([boolRef("isAdmin", freshSrc("b1")), boolRef("isAdmin", freshSrc("b2"))]));
show("NOT(isAdmin AND mfa)", notNode(andNode([boolRef("isAdmin", freshSrc("b3")), boolRef("mfa", freshSrc("m2"))])));
show("true OR isAdmin", orNode([constNode(1), boolRef("isAdmin", freshSrc("b4"))]));

console.log("\nDone.");
