/**
 * Differential tests: the main engine is cross-checked against the
 * independent finite scaled-integer reference (src/reference.ts) over
 * randomly generated schemas and an exhaustive value window.
 */
import { test } from "node:test";
import assert from "node:assert/strict";
import { parseDecimal } from "../src/decimal.ts";
import { compileSchema } from "../src/schema.ts";
import type { DecimalSchema } from "../src/schema.ts";
import { evaluate, validate } from "../src/validate.ts";
import { intersect } from "../src/intersect.ts";
import { counterexample } from "../src/counterexample.ts";
import { compileReference, enumerateAccepted, referenceViolations } from "../src/reference.ts";

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const BOUND_POOL = ["-1", "-0.5", "-0.25", "-0.01", "0", "0.01", "0.25", "0.5", "0.99", "1"];
const MULT_POOL = ["0.01", "0.1", "0.2", "0.25", "0.5", "1", "2"];

function randomSchema(rnd: () => number): DecimalSchema {
  const pick = <T>(pool: readonly T[]): T => pool[Math.floor(rnd() * pool.length)]!;
  const schema: DecimalSchema = {};
  if (rnd() < 0.75) {
    const lo = pick(BOUND_POOL);
    const hi = pick(BOUND_POOL);
    if (rnd() < 0.5) schema.minimum = lo;
    else schema.exclusiveMinimum = lo;
    if (rnd() < 0.5) schema.maximum = hi;
    else schema.exclusiveMaximum = hi;
  }
  if (rnd() < 0.5) schema.multipleOf = pick(MULT_POOL);
  if (rnd() < 0.4) schema.maxTotalDigits = 1 + Math.floor(rnd() * 4);
  if (rnd() < 0.4) schema.maxFractionDigits = Math.floor(rnd() * 4);
  return schema;
}

const WINDOW_SCALE = 4;
const WINDOW_LIMIT = 20000; // |v| <= 2, covers every witness these schemas can produce

test("differential: validate agrees with the reference on an exhaustive window", () => {
  const rnd = mulberry32(20261003);
  const limit = 12000; // smaller window: agreement does not need witness coverage
  for (let trial = 0; trial < 12; trial += 1) {
    const schema = randomSchema(rnd);
    const compiled = compileSchema(schema);
    const reference = compileReference(schema);
    for (let k = -limit; k <= limit; k += 1) {
      const text = `${k}e-${WINDOW_SCALE}`;
      const main = evaluate(parseDecimal(text), compiled).length === 0;
      const ref = referenceViolations(reference, text).length === 0;
      assert.equal(main, ref, `schema=${JSON.stringify(schema)} value=${text}`);
    }
  }
});

test("differential: intersect witness is feasible, infeasible means empty window", () => {
  const rnd = mulberry32(987654321);
  let feasibleCount = 0;
  let infeasibleCount = 0;
  for (let trial = 0; trial < 24; trial += 1) {
    const schema = randomSchema(rnd);
    const reference = compileReference(schema);
    const res = intersect(schema);
    if (res.feasible) {
      feasibleCount += 1;
      assert.deepEqual(
        referenceViolations(reference, res.witness),
        [],
        `witness ${res.witness} must satisfy ${JSON.stringify(schema)}`,
      );
      assert.ok(
        enumerateAccepted(reference, WINDOW_SCALE, WINDOW_LIMIT).length > 0,
        "feasible schema must have values in the window",
      );
    } else {
      infeasibleCount += 1;
      assert.deepEqual(
        enumerateAccepted(reference, WINDOW_SCALE, WINDOW_LIMIT),
        [],
        `infeasible schema ${JSON.stringify(schema)} must have an empty window; evidence: ${res.summary}`,
      );
    }
  }
  assert.ok(feasibleCount > 0 && infeasibleCount > 0, "expected both outcomes across trials");
});

test("differential: counterexamples fail according to the reference too", () => {
  const rnd = mulberry32(1122334455);
  for (let trial = 0; trial < 40; trial += 1) {
    const schema = randomSchema(rnd);
    const reference = compileReference(schema);
    const res = counterexample(schema);
    if (res.found) {
      assert.ok(
        referenceViolations(reference, res.value).length > 0,
        `counterexample ${res.value} must violate ${JSON.stringify(schema)}`,
      );
    } else {
      assert.deepEqual(schema, {}, "only the empty schema may lack a counterexample");
    }
  }
});

test("differential: string-level validate() matches reference on sampled pairs", () => {
  const rnd = mulberry32(555);
  for (let trial = 0; trial < 300; trial += 1) {
    const schema = randomSchema(rnd);
    const reference = compileReference(schema);
    const k = Math.floor(rnd() * 2 * WINDOW_LIMIT) - WINDOW_LIMIT;
    const text = `${k}e-${WINDOW_SCALE}`;
    assert.equal(
      validate(text, schema).valid,
      referenceViolations(reference, text).length === 0,
      `schema=${JSON.stringify(schema)} value=${text}`,
    );
  }
});
