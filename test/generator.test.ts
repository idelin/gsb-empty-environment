import { test } from "node:test";
import assert from "node:assert/strict";
import { generate } from "../src/generator.ts";
import { validate } from "../src/validator.ts";
import { makeRng } from "../src/rng.ts";
import type { Schema } from "../src/schema.ts";

const satisfiable: Array<[string, Schema]> = [
  ["bounded integer with multipleOf", { type: "integer", minimum: 2, maximum: 20, multipleOf: 2 }],
  ["exclusive float range", { type: "number", exclusiveMinimum: 0, exclusiveMaximum: 1 }],
  ["pattern + length window", { type: "string", minLength: 3, maxLength: 8, pattern: "^[a-z]+$" }],
  ["alternation pattern", { type: "string", pattern: "^(cat|dog)-[0-9]{2}$" }],
  ["array of enum", { type: "array", minItems: 1, maxItems: 3, items: { enum: ["a", "b"] } }],
  ["const object", { const: { a: [1, 2] } }],
  ["degenerate integer range", { type: "integer", minimum: 5, maximum: 5 }],
  [
    "nested object",
    {
      type: "object",
      properties: {
        name: { type: "string", minLength: 1, maxLength: 10 },
        age: { type: "integer", minimum: 0, maximum: 150 },
        tags: { type: "array", minItems: 1, maxItems: 2, items: { type: "string", pattern: "^[a-z]{3}$" } },
      },
      required: ["name", "age"],
      additionalProperties: false,
    },
  ],
];

for (const [label, schema] of satisfiable) {
  test(`positive sample validates clean: ${label}`, () => {
    for (let seed = 1; seed <= 8; seed++) {
      const r = generate(schema, makeRng(seed));
      assert.ok(r.ok, `seed ${seed}: expected a sample, got reason: ${r.ok ? "" : r.reason}`);
      if (r.ok) assert.deepEqual(validate(r.value, schema), [], `seed ${seed}`);
    }
  });
}

const unsatisfiable: Array<[string, Schema, string]> = [
  ["inverted bounds", { type: "integer", minimum: 10, maximum: 5 }, "empty numeric range"],
  ["no multiple in range", { type: "integer", minimum: 1, maximum: 3, multipleOf: 5 }, "no multiple of 5"],
  ["inverted string lengths", { type: "string", minLength: 5, maxLength: 2 }, "minLength 5 exceeds maxLength 2"],
  ["inverted array sizes", { type: "array", minItems: 3, maxItems: 1 }, "minItems 3 exceeds maxItems 1"],
  ["const conflicts with minimum", { const: 5, minimum: 10 }, "conflicts"],
  ["enum emptied by minimum", { enum: [1, 2], minimum: 5 }, "no enum value"],
  [
    "nested contradiction propagates",
    { type: "object", properties: { x: { type: "integer", minimum: 10, maximum: 5 } }, required: ["x"] },
    'property "x"',
  ],
];

for (const [label, schema, reasonPart] of unsatisfiable) {
  test(`unsatisfiable schema is reported, not faked: ${label}`, () => {
    const r = generate(schema, makeRng(1));
    assert.ok(!r.ok, "expected failure");
    if (!r.ok) assert.ok(r.reason.includes(reasonPart), `reason ${r.reason} should mention ${reasonPart}`);
  });
}

test("unsupported pattern is explained, not fabricated", () => {
  const r = generate({ type: "string", pattern: "^(a+)\\1$" }, makeRng(1));
  assert.ok(!r.ok);
  if (!r.ok) assert.match(r.reason, /outside the supported generation subset/);
});
