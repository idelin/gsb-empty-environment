import { test } from "node:test";
import assert from "node:assert/strict";
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
} from "../src/ast.ts";
import { simplify } from "../src/simplify.ts";
import { evaluate } from "../src/eval.ts";
import { checkEquivalence, testSimplification } from "../src/tester.ts";
import { MISSING, TRUE } from "../src/trivalue.ts";
import type { Schema } from "../src/schema.ts";

const schema: Schema = {
  a: { kind: "bool" },
  b: { kind: "bool" },
  age: { kind: "int", min: 0, max: 100 },
  role: { kind: "finite", values: ["free", "pro", "ent"] },
};

function simp(e: Parameters<typeof simplify>[0]) {
  return simplify(e, schema);
}

test("false AND branch is eliminated keeping the false branch's source", () => {
  const src = freshSrc("deny");
  const out = simp(andNode([constNode(0, new Set([src])), boolRef("a", freshSrc("a"))]));
  assert.equal(out.expr.kind, "const");
  assert.deepEqual([...(out.expr.kind === "const" ? out.expr.src ?? [] : [])], [src]);
  assert.ok(out.lineage.some((l) => l.rule === "and-false-annihilator"));
});

test("true identity / false identity", () => {
  assert.equal(simp(andNode([constNode(1), boolRef("a")])).expr.kind, "bool");
  const orFalse = simp(orNode([constNode(0), boolRef("a")]));
  assert.equal(orFalse.expr.kind, "bool");
  assert.equal(simp(orNode([constNode(1), boolRef("a")])).expr.kind, "const");
});

test("x OR NOT x is NOT simplified on a nullable domain", () => {
  const expr = orNode([boolRef("a"), notNode(boolRef("a"))]);
  const out = simp(expr);
  assert.equal(out.expr.kind, "or", render(out.expr));
  assert.equal(evaluate(out.expr, {}, schema), MISSING);
  assert.equal(evaluate(expr, { a: true }, schema), TRUE);
});

test("double negation collapses", () => {
  const src = freshSrc("s");
  const out = simp(notNode(notNode(boolRef("a", src))));
  assert.equal(out.expr.kind, "bool");
  assert.deepEqual([...(out.expr.src ?? [])], [src]);
});

test("idempotence merges duplicates and unions sources", () => {
  const s1 = freshSrc("x");
  const s2 = freshSrc("y");
  const out = simp(andNode([boolRef("a", s1), boolRef("a", s2)]));
  assert.equal(out.expr.kind, "bool");
  assert.deepEqual([...(out.expr.src ?? [])].sort(), [s1, s2].sort());
});

test("integer intervals intersect and union", () => {
  const inter = simp(andNode([cmpNode("age", ">=", 18, "r1"), cmpNode("age", "<", 30, "r2")]));
  assert.equal(inter.expr.kind, "cmp");
  if (inter.expr.kind === "cmp") {
    assert.deepEqual(inter.expr.ranges, [{ lo: 18, hi: 29 }]);
    assert.deepEqual([...(inter.expr.src ?? [])].sort(), ["r1", "r2"]);
  }
  const uni = simp(orNode([cmpNode("age", "<", 18, "r3"), cmpNode("age", ">=", 65, "r4")]));
  if (uni.expr.kind === "cmp") {
    assert.deepEqual(uni.expr.ranges, [
      { lo: -Infinity, hi: 17 },
      { lo: 65, hi: Infinity },
    ]);
  }
});

test("unsatisfiable interval is an empty predicate, not constant false", () => {
  const out = simp(andNode([cmpNode("age", "<", 10, "r5"), cmpNode("age", ">", 20, "r6")]));
  assert.equal(out.expr.kind, "cmp");
  if (out.expr.kind === "cmp") assert.deepEqual(out.expr.ranges, []);
  // absent attribute still yields MISSING — this is why no collapse to false happened
  assert.equal(evaluate(out.expr, {}, schema), MISSING);
});

test("finite sets intersect, union and complement", () => {
  const inter = simp(andNode([inNode("role", ["pro", "ent"], "f1"), inNode("role", ["ent", "free"], "f2")]));
  if (inter.expr.kind === "in") assert.deepEqual([...inter.expr.set], ["ent"]);

  const uni = simp(orNode([inNode("role", ["free"], "f3"), inNode("role", ["pro", "ent"], "f4")]));
  if (uni.expr.kind === "in") assert.deepEqual([...uni.expr.set].sort(), ["ent", "free", "pro"]);
  assert.equal(evaluate(uni.expr, {}, schema), MISSING, "full-cover set still misses when absent");

  const comp = simp(notNode(inNode("role", ["free"], "f5")));
  if (comp.expr.kind === "in") assert.deepEqual([...comp.expr.set].sort(), ["ent", "pro"]);
});

test("De Morgan is applied and stays three-valued sound", () => {
  const expr = notNode(andNode([boolRef("a", "d1"), boolRef("b", "d2")]));
  const out = simp(expr);
  assert.equal(out.expr.kind, "or");
  const report = checkEquivalence(expr, out.expr, schema);
  assert.equal(report.equivalent, true, JSON.stringify(report.mismatches));
});

test("flattening nested associative operators", () => {
  const out = simp(andNode([andNode([boolRef("a"), boolRef("b")]), boolRef("a")]));
  assert.equal(out.expr.kind, "and");
  if (out.expr.kind === "and") {
    assert.deepEqual(out.expr.args.map((x) => (x.kind === "bool" ? x.name : x.kind)).sort(), ["a", "b"]);
  }
});

test("all shipped rewrites are exhaustively equivalent incl. absent inputs", () => {
  const cases = [
    andNode([constNode(0, new Set(["c0"])), boolRef("a"), cmpNode("age", ">=", 0, "c1")]),
    orNode([boolRef("a"), boolRef("a"), boolRef("b")]),
    andNode([cmpNode("age", ">", 10, "i1"), cmpNode("age", "<=", 90, "i2"), inNode("role", ["pro", "ent"], "i3")]),
    notNode(orNode([boolRef("a"), cmpNode("age", "==", 42, "i4")])),
    orNode([inNode("role", ["free"], "i5"), notNode(inNode("role", ["free"], "i6"))]),
  ];
  for (const expr of cases) {
    const report = testSimplification(expr, schema);
    assert.equal(report.equivalent, true, `${render(expr)}\n${JSON.stringify(report.mismatches, null, 2)}`);
    assert.equal(report.checked, Number(report.domainSize), "whole finite domain must be checked");
    assert.deepEqual(report.inventedSources, []);
  }
});
