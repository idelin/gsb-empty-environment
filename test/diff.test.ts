import { test } from "node:test";
import assert from "node:assert/strict";
import { compareSchemas, type Sample } from "../src/diff.ts";

const oldSchema = {
  type: "object",
  required: ["name"],
  properties: {
    name: { type: "string", maxLength: 5 },
    age: { type: "integer" },
    tag: { type: "string" },
  },
  additionalProperties: false,
};

const newSchema = {
  type: "object",
  required: ["name", "age"],
  properties: {
    name: { type: "string", maxLength: 20 },
    age: { type: "integer", minimum: 0 },
    tag: { type: "string", pattern: "^[a-z]+$" },
    email: { type: "string", format: "email" },
  },
  additionalProperties: false,
};

const samples: Sample[] = [
  { id: "pass-both", data: { name: "amy", age: 3, tag: "abc" } },
  { id: "new-fail-required", data: { name: "bo" } },
  { id: "new-fail-minimum", data: { name: "cy", age: -2 } },
  { id: "fixed-name-relaxed", data: { name: "alexander", age: 5 } },
  { id: "reason-shift", data: { name: "toolongname", age: -1 } },
  { id: "same-failure", data: { name: 42, age: 1 } },
];

test("new failures are listed separately from old failures", () => {
  const { report } = compareSchemas(oldSchema, newSchema, samples, { cacheDir: undefined });
  assert.deepEqual(report.newFailures.map((f) => f.sampleId), ["new-fail-required", "new-fail-minimum"]);
  assert.deepEqual(report.fixed, ["fixed-name-relaxed"]);
  assert.deepEqual(report.stillPassing, ["pass-both"]);
});

test("new failure errors include instance and rule paths", () => {
  const { report } = compareSchemas(oldSchema, newSchema, samples, { cacheDir: undefined });
  const required = report.newFailures.find((f) => f.sampleId === "new-fail-required");
  assert.ok(required);
  assert.equal(required.errors[0].keyword, "required");
  assert.equal(required.errors[0].rulePath, "/required");
  assert.equal(required.errors[0].instancePath, "");
});

test("fail-on-both with different rules is recorded as reason change", () => {
  const { report } = compareSchemas(oldSchema, newSchema, samples, { cacheDir: undefined });
  const shift = report.reasonChanged.find((r) => r.sampleId === "reason-shift");
  assert.ok(shift);
  assert.deepEqual(shift.removed, ["maxLength@/name"]);
  assert.deepEqual(shift.added, ["minimum@/age"]);
});

test("fail-on-both with identical rules stays in unchanged failures", () => {
  const { report } = compareSchemas(oldSchema, newSchema, samples, { cacheDir: undefined });
  assert.deepEqual(report.unchangedFailures.map((f) => f.sampleId), ["same-failure"]);
});

test("unsupported keyword is surfaced as a warning rather than enforced", () => {
  const { report } = compareSchemas(oldSchema, newSchema, samples, { cacheDir: undefined });
  assert.match(report.warnings.new.join("\n"), /format/);
});
