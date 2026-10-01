import type { Schema } from "./schema.ts";
import { makeRng } from "./rng.ts";
import { validate } from "./validator.ts";
import { generate } from "./generator.ts";
import { allCounterexamples } from "./counterexample.ts";

const show = (label: string, value: unknown) =>
  console.log(`${label}\n${JSON.stringify(value, null, 2)}`);

// ---------------------------------------------------------------- schema 1
const userSchema: Schema = {
  type: "object",
  properties: {
    name: { type: "string", minLength: 1, maxLength: 20, pattern: "^[A-Z][a-z]+$" },
    age: { type: "integer", minimum: 0, maximum: 150 },
    score: { type: "number", minimum: 0, maximum: 10, multipleOf: 0.5 },
    tags: { type: "array", minItems: 1, maxItems: 3, items: { enum: ["red", "green", "blue"] } },
    active: { type: "boolean" },
  },
  required: ["name", "age"],
  additionalProperties: false,
};

console.log("=== 1. Satisfying sample (seed 42) ===");
show("schema:", userSchema);
const positive = generate(userSchema, makeRng(42));
if (positive.ok) {
  show("positive sample:", positive.value);
  const errs = validate(positive.value, userSchema);
  console.log(`independent validator: ${errs.length === 0 ? "CLEAN (0 errors)" : JSON.stringify(errs)}`);
} else {
  console.log(`cannot construct: ${positive.reason}`);
}

console.log("\n=== 2. Counterexamples: violate exactly one rule each (seed 42) ===");
for (const r of allCounterexamples(userSchema, 42)) {
  if (r.ok) {
    console.log(`\nrule ${r.target.schemaPath}`);
    show("  instance:", r.instance);
    console.log(
      `  verified single error -> instancePath ${JSON.stringify(r.error.instancePath)}, ` +
        `schemaPath ${JSON.stringify(r.error.schemaPath)}: ${r.error.message}`,
    );
  } else {
    console.log(`\nrule ${r.target.schemaPath}\n  cannot construct: ${r.reason}`);
  }
}

// ---------------------------------------------------------------- schema 2
console.log("\n=== 3. Contradictory schema: no positive example exists ===");
const contradictory: Schema = { type: "integer", minimum: 10, maximum: 5 };
const bad = generate(contradictory, makeRng(1));
console.log(bad.ok ? `BUG: ${JSON.stringify(bad.value)}` : `reported unsatisfiable: ${bad.reason}`);
for (const r of allCounterexamples(contradictory, 1))
  console.log(`rule ${r.target.schemaPath}: ${r.ok ? "unexpected" : r.reason}`);

// ---------------------------------------------------------------- schema 3
console.log("\n=== 4. Unconstructable cases are explained, not faked ===");
const backref: Schema = { type: "string", pattern: "^(a+)\\1$" };
const r3 = generate(backref, makeRng(7));
console.log(`unsupported pattern -> ${r3.ok ? "BUG" : r3.reason}`);

const pinned: Schema = { const: 5, minimum: 5, maximum: 5 };
for (const r of allCounterexamples(pinned, 3))
  console.log(`rule ${r.target.schemaPath}: ${r.ok ? JSON.stringify(r.instance) : `cannot construct: ${r.reason}`}`);

// ---------------------------------------------------------------- determinism
console.log("\n=== 5. Determinism: same seed, same output ===");
const runA = JSON.stringify(allCounterexamples(userSchema, 42));
const runB = JSON.stringify(allCounterexamples(userSchema, 42));
console.log(`two runs with seed 42 identical: ${runA === runB}`);
