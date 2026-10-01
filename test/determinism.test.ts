import { test } from "node:test";
import assert from "node:assert/strict";
import { generate } from "../src/generator.ts";
import { allCounterexamples } from "../src/counterexample.ts";
import { makeRng } from "../src/rng.ts";
import type { Schema } from "../src/schema.ts";

const schema: Schema = {
  type: "object",
  properties: {
    name: { type: "string", minLength: 1, maxLength: 20, pattern: "^[A-Z][a-z]+$" },
    age: { type: "integer", minimum: 0, maximum: 150 },
    tags: { type: "array", minItems: 1, maxItems: 3, items: { enum: ["red", "green", "blue"] } },
  },
  required: ["name", "age"],
  additionalProperties: false,
};

test("same seed produces identical positive samples", () => {
  const a = generate(schema, makeRng(42));
  const b = generate(schema, makeRng(42));
  assert.deepEqual(a, b);
});

test("same seed produces identical counterexample suites", () => {
  const a = JSON.stringify(allCounterexamples(schema, 42));
  const b = JSON.stringify(allCounterexamples(schema, 42));
  assert.equal(a, b);
});

test("different seeds explore different samples", () => {
  const samples = new Set(
    [1, 2, 3, 4, 5, 6].map((s) => {
      const r = generate(schema, makeRng(s));
      return JSON.stringify(r.ok ? r.value : null);
    }),
  );
  assert.ok(samples.size > 1, "expected some variety across seeds");
});
