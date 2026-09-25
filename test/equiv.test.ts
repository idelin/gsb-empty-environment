import assert from "node:assert/strict";
import test from "node:test";
import { and, cmp, inSet, lit, not, or, resetIds } from "../src/ast.ts";
import { checkEquivalence } from "../src/equiv.ts";
import type { AttrDecl } from "../src/policy.ts";
import { simplify, type Domains } from "../src/simplify.ts";

const attrs: AttrDecl[] = [
  { name: "age", min: 0, max: 120, required: true },
  { name: "dept", min: 100, max: 110, required: true },
  { name: "level", min: 1, max: 5, required: false },
];

const domains: Domains = Object.fromEntries(
  attrs.map((a) => [a.name, { min: a.min, max: a.max, required: a.required }]),
);

test("simplified expression agrees with the original on every finite input", () => {
  resetIds();
  const original = or(
    and(cmp("age", ">=", 18), cmp("age", "<=", 65), inSet("dept", [101, 102])),
    and(cmp("level", ">=", 4), cmp("level", "<=", 5)),
    and(lit(false), cmp("age", ">", 100)),
  );
  const simplified = simplify(original, domains);
  const result = checkEquivalence(attrs, original, simplified);
  assert.equal(result.checked, 121 * 11 * 6);
  assert.equal(result.equivalent, true);
  assert.deepEqual(result.mismatches, []);
});

test("tester catches the forbidden fold: x OR NOT x -> TRUE on a nullable domain", () => {
  resetIds();
  const x = cmp("level", ">=", 3);
  const result = checkEquivalence(
    attrs.filter((a) => a.name === "level"),
    or(x, not(x)),
    lit(true),
  );
  assert.equal(result.equivalent, false);
  assert.equal(result.mismatches.length, 1);
  assert.deepEqual(result.mismatches[0], {
    env: { level: undefined },
    a: "unknown",
    b: "true",
  });
});

test("x OR NOT x IS equivalent to TRUE when the attribute is required", () => {
  resetIds();
  const x = cmp("age", ">=", 18);
  const result = checkEquivalence(
    attrs.filter((a) => a.name === "age"),
    or(x, not(x)),
    lit(true),
  );
  assert.equal(result.equivalent, true);
});

test("false AND x == false even when x may be unknown (Kleene domination)", () => {
  resetIds();
  const x = cmp("level", ">=", 3);
  const result = checkEquivalence(
    attrs.filter((a) => a.name === "level"),
    and(lit(false), x),
    lit(false),
  );
  assert.equal(result.equivalent, true);
});

test("tester refuses domains beyond the case limit", () => {
  resetIds();
  const huge: AttrDecl[] = [{ name: "n", min: 0, max: 10_000_000, required: true }];
  assert.throws(() => checkEquivalence(huge, cmp("n", ">", 1), cmp("n", ">", 1)));
});
