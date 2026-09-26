import { test } from "node:test";
import assert from "node:assert/strict";
import { compileSchema, type Schema } from "../src/schema.ts";
import { compileRules, type Rule } from "../src/rules.ts";
import { validate } from "../src/evaluate.ts";
import type { Issue } from "../src/issues.ts";

function mustSchema(raw: unknown): Schema {
  const r = compileSchema(raw);
  if (!r.ok) throw new Error(`schema should compile: ${JSON.stringify(r.issues)}`);
  return r.value;
}

function mustRules(raw: unknown): Rule[] {
  const r = compileRules(raw);
  if (!r.ok) throw new Error(`rules should compile: ${JSON.stringify(r.issues)}`);
  return r.value;
}

function kinds(issues: readonly Issue[]): string[] {
  return issues.map((i) => i.kind);
}

const orderSchema = mustSchema({
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

const orderRules = mustRules([
  { kind: "cmp", op: "le", left: { path: "/start" }, right: { path: "/end" } },
  {
    kind: "requiredIf",
    condition: { op: "eq", left: { path: "/member" }, right: { const: true } },
    target: "/discount",
  },
  { kind: "sum", list: "/items", field: "price", op: "le", value: { const: 100 } },
]);

test("end < start: violation carries both instance paths and the rule path", () => {
  const r = validate({ start: 9, end: 5, member: false, items: [] }, orderSchema, orderRules);
  assert.equal(r.phase, "relation");
  assert.equal(r.issues.length, 1);
  const issue = r.issues[0]!;
  assert.equal(issue.kind, "relation");
  assert.equal(issue.instancePath, "/start");
  assert.equal(issue.rulePath, "/rules/0");
  const relatedPaths = issue.related.map((rel) => rel.instancePath);
  assert.ok(relatedPaths.includes("/start"), "left path reported");
  assert.ok(relatedPaths.includes("/end"), "right path reported");
});

test("conditional required: true condition forces the target", () => {
  const missing = validate({ start: 1, end: 2, member: true, items: [] }, orderSchema, orderRules);
  assert.deepEqual(kinds(missing.issues), ["missing-required"]);
  assert.equal(missing.issues[0]!.instancePath, "/discount");
  assert.equal(missing.issues[0]!.rulePath, "/rules/1");

  const present = validate({ start: 1, end: 2, member: true, discount: 10, items: [] }, orderSchema, orderRules);
  assert.equal(present.issues.length, 0);
});

test("conditional required: false condition imposes nothing", () => {
  const r = validate({ start: 1, end: 2, member: false, items: [] }, orderSchema, orderRules);
  assert.equal(r.issues.length, 0);
});

test("unknown condition is reported, never silently treated as false", () => {
  const r = validate({ start: 1, end: 2, items: [] }, orderSchema, orderRules); // member absent
  assert.deepEqual(kinds(r.issues), ["unknown-condition"]);
  assert.equal(r.issues[0]!.instancePath, "/member");
  assert.equal(r.issues[0]!.rulePath, "/rules/1");
});

test("empty list sums to 0; missing list is unknown, not 0", () => {
  const empty = validate({ start: 1, end: 2, member: false, items: [] }, orderSchema, orderRules);
  assert.equal(empty.issues.length, 0, "sum of empty list is 0 and passes le 100");

  const missing = validate({ start: 1, end: 2, member: false }, orderSchema, orderRules);
  assert.ok(kinds(missing.issues).includes("missing-required"), "items is required by the schema");
  assert.ok(kinds(missing.issues).includes("unknown-operand"), "sum over a missing list is unknown");
  const sumIssue = missing.issues.find((i) => i.kind === "unknown-operand")!;
  assert.equal(sumIssue.instancePath, "/items");
  assert.equal(sumIssue.rulePath, "/rules/2");
});

test("missing item field is not zero-filled", () => {
  const r = validate(
    { start: 1, end: 2, member: false, items: [{}] },
    orderSchema,
    orderRules,
  );
  assert.ok(kinds(r.issues).includes("missing-required"), "price is required per item");
  assert.ok(kinds(r.issues).includes("unknown-operand"), "sum is unknown when an item field is missing");
});

test("sum violation reports the total and the bound", () => {
  const r = validate(
    { start: 1, end: 2, member: false, items: [{ price: 60 }, { price: 50 }] },
    orderSchema,
    orderRules,
  );
  assert.deepEqual(kinds(r.issues), ["relation"]);
  assert.match(r.issues[0]!.message, /110/);
  assert.equal(r.issues[0]!.instancePath, "/items");
});

test("type checking runs before relation evaluation", () => {
  const r = validate({ start: "nine", end: 5, member: false, items: [] }, orderSchema, orderRules);
  assert.equal(r.phase, "type");
  assert.deepEqual(kinds(r.issues), ["type"]);
  assert.equal(r.issues[0]!.instancePath, "/start");
  assert.equal(r.issues[0]!.rulePath, "/schema/fields/start");
});

test("range violations are reported with instance and rule paths", () => {
  const r = validate({ start: 1, end: 2, member: true, discount: 500, items: [] }, orderSchema, orderRules);
  assert.equal(r.phase, "type");
  assert.deepEqual(kinds(r.issues), ["range"]);
  assert.equal(r.issues[0]!.instancePath, "/discount");
  assert.equal(r.issues[0]!.rulePath, "/schema/fields/discount/max");
});

test("nested type errors carry full instance and rule paths", () => {
  const r = validate({ start: 1, end: 2, member: false, items: [{ price: "free" }] }, orderSchema, orderRules);
  assert.equal(r.phase, "type");
  const issue = r.issues[0]!;
  assert.equal(issue.instancePath, "/items/0/price");
  assert.equal(issue.rulePath, "/schema/fields/items/items/fields/price");
});

test("rules referencing paths outside the schema are rejected up front", () => {
  const rules = mustRules([{ kind: "cmp", op: "le", left: { path: "/start" }, right: { path: "/nope" } }]);
  const r = validate({ start: 1, end: 2, member: false, items: [] }, orderSchema, rules);
  assert.equal(r.phase, "coverage");
  assert.deepEqual(kinds(r.issues), ["uncovered-path"]);
  assert.equal(r.issues[0]!.instancePath, "/nope");
  assert.equal(r.issues[0]!.rulePath, "/rules/0/right");
});

test("eq rule: violation, satisfaction, and missing operand", () => {
  const schema = mustSchema({
    kind: "object",
    fields: { a: { kind: "int" }, b: { kind: "int" } },
    required: [],
  });
  const rules = mustRules([{ kind: "eq", left: { path: "/a" }, right: { path: "/b" } }]);
  assert.equal(validate({ a: 1, b: 1 }, schema, rules).issues.length, 0);
  assert.deepEqual(kinds(validate({ a: 1, b: 2 }, schema, rules).issues), ["relation"]);
  assert.deepEqual(kinds(validate({ a: 1 }, schema, rules).issues), ["unknown-operand"]);
});

test("when: conditional branch taken, not taken, and undecidable", () => {
  const schema = mustSchema({
    kind: "object",
    fields: { x: { kind: "int" }, y: { kind: "int" } },
    required: ["y"],
  });
  const rules = mustRules([
    {
      kind: "when",
      condition: { op: "le", left: { path: "/x" }, right: { const: 0 } },
      then: { kind: "cmp", op: "ge", left: { path: "/y" }, right: { const: 1 } },
    },
  ]);
  const taken = validate({ x: -1, y: 0 }, schema, rules);
  assert.deepEqual(kinds(taken.issues), ["relation"]);
  assert.equal(taken.issues[0]!.rulePath, "/rules/0/then");

  const notTaken = validate({ x: 1, y: 0 }, schema, rules);
  assert.equal(notTaken.issues.length, 0);

  const undecidable = validate({ y: 0 }, schema, rules); // x missing
  assert.deepEqual(kinds(undecidable.issues), ["unknown-condition"]);
});

test("unsupported schema keywords are rejected with their rule path", () => {
  const r = compileSchema({ kind: "int", minimum: 0 });
  assert.equal(r.ok, false);
  if (!r.ok) {
    assert.deepEqual(kinds(r.issues), ["unsupported-feature"]);
    assert.equal(r.issues[0]!.rulePath, "/schema/minimum");
  }
});

test("unsupported rule kinds are rejected", () => {
  const r = compileRules([{ kind: "pattern", regex: ".*" }]);
  assert.equal(r.ok, false);
  if (!r.ok) assert.deepEqual(kinds(r.issues), ["unsupported-feature"]);
});

test("schema with min > max is invalid and reports both sides", () => {
  const r = compileSchema({ kind: "int", min: 10, max: 3 });
  assert.equal(r.ok, false);
  if (!r.ok) {
    assert.deepEqual(kinds(r.issues), ["invalid-schema"]);
    assert.equal(r.issues[0]!.rulePath, "/schema/min");
    assert.equal(r.issues[0]!.related[0]!.rulePath, "/schema/max");
  }
});
