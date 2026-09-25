import assert from "node:assert/strict";
import test from "node:test";
import {
  and,
  cmp,
  inSet,
  lit,
  not,
  or,
  resetIds,
  show,
  type Expr,
} from "../src/ast.ts";
import { evalExpr } from "../src/eval.ts";
import { simplify, type Domains } from "../src/simplify.ts";

const domains: Domains = {
  age: { min: 0, max: 120, required: true },
  dept: { min: 100, max: 110, required: true },
  level: { min: 1, max: 5, required: false },
};

function setup(): void {
  resetIds();
}

test("constant false eliminates an AND branch; dropped branch leaves the source map", () => {
  setup();
  const dead = cmp("age", ">", 100);
  const e = and(lit(false), dead);
  const s = simplify(e, domains);
  assert.deepEqual(s.kind, "const");
  assert.equal(show(s), "FALSE");
  assert.ok(!s.from.includes(dead.id), "eliminated branch id must not appear in sources");
});

test("identity laws: true AND x -> x, false OR x -> x, true OR x -> true", () => {
  setup();
  const x = cmp("age", ">=", 18);
  assert.equal(show(simplify(and(lit(true), x), domains)), "age IN [18, +∞]");
  assert.equal(show(simplify(or(lit(false), x), domains)), "age IN [18, +∞]");
  assert.equal(show(simplify(or(lit(true), x), domains)), "TRUE");
});

test("double negation elimination", () => {
  setup();
  const x = cmp("age", ">=", 18);
  assert.equal(show(simplify(not(not(x)), domains)), "age IN [18, +∞]");
});

test("FORBIDDEN: x OR NOT x is not folded to TRUE on a nullable domain", () => {
  setup();
  const x = cmp("level", ">=", 3);
  const s = simplify(or(x, not(x)), domains);
  assert.notEqual(s.kind, "const");
  assert.equal(evalExpr(s, {}), "unknown", "missing level must stay unknown");
});

test("FORBIDDEN: x AND NOT x is not folded to FALSE on a nullable domain", () => {
  setup();
  const x = cmp("level", ">=", 3);
  const s = simplify(and(x, not(x)), domains);
  assert.notDeepEqual(s.kind, "const");
  assert.equal(evalExpr(s, {}), "unknown", "missing level must stay unknown");
});

test("excluded middle DOES fold to TRUE when the attribute is required", () => {
  setup();
  const x = cmp("age", ">=", 18);
  assert.equal(show(simplify(or(x, not(x)), domains)), "TRUE");
  assert.equal(show(simplify(and(x, not(x)), domains)), "FALSE");
});

test("interval merge: age >= 18 AND age <= 65 -> age IN [18, 65]", () => {
  setup();
  const s = simplify(and(cmp("age", ">=", 18), cmp("age", "<=", 65)), domains);
  assert.equal(show(s), "age IN [18, 65]");
});

test("contradiction on a required attribute collapses to FALSE", () => {
  setup();
  const s = simplify(and(cmp("age", "<", 10), cmp("age", ">", 20)), domains);
  assert.equal(show(s), "FALSE");
});

test("contradiction on an optional attribute stays a condition (unknown if missing)", () => {
  setup();
  const s = simplify(and(cmp("level", "<", 2), cmp("level", ">", 3)), domains);
  assert.equal(s.kind, "cond");
  assert.equal(show(s), "level IN ∅");
  assert.equal(evalExpr(s, {}), "unknown");
  assert.equal(evalExpr(s, { level: 4 }), "false");
});

test("OR merge covering the required domain collapses to TRUE", () => {
  setup();
  const s = simplify(or(cmp("age", "<=", 17), cmp("age", ">=", 18)), domains);
  assert.equal(show(s), "TRUE");
});

test("finite-set membership merges via interval algebra", () => {
  setup();
  const s = simplify(
    and(inSet("dept", [101, 102]), inSet("dept", [102, 103])),
    domains,
  );
  assert.equal(show(s), "dept IN [102, 102]");
});

test("idempotence: x AND x -> x, provenance unions both occurrences", () => {
  setup();
  const x1 = cmp("age", ">=", 18);
  const x2 = cmp("age", ">=", 18);
  const s = simplify(and(x1, x2), domains);
  assert.equal(show(s), "age IN [18, +∞]");
  assert.ok(s.from.includes(x1.id) && s.from.includes(x2.id));
});

test("NOT of a condition complements its intervals", () => {
  setup();
  const s = simplify(not(cmp("age", ">=", 18)), domains);
  assert.equal(show(s), "age IN [-∞, 17]");
});

test("source map of a kept node references only original ids", () => {
  setup();
  const a = cmp("age", ">=", 18);
  const b = cmp("age", "<=", 65);
  const root = and(a, b);
  const s = simplify(root, domains);
  assert.deepEqual([...s.from].sort((m, n) => m - n), [a.id, b.id, root.id]);
});
