import { test } from "node:test";
import assert from "node:assert/strict";
import { frac } from "../src/fraction.ts";
import { RationalDAG } from "../src/expr.ts";

test("DAG evaluates a shared expression and reduces only at the root", () => {
  const dag = new RationalDAG();
  const a = dag.frac(frac(1n, 6n));
  const b = dag.frac(frac(1n, 7n));
  const sum = dag.add(a, b);
  const twice = dag.add(sum, sum);

  const r = dag.evaluate(twice);
  assert.equal(r.status, "ok");
  assert.deepEqual(r.value, frac(13n, 21n));

  assert.deepEqual(dag.evaluate(sum).value, frac(13n, 42n));
});

test("DAG four operations + negation", () => {
  const dag = new RationalDAG();
  const x = dag.leaf(2n, 3n);
  const y = dag.leaf(3n, 4n);
  assert.deepEqual(dag.evaluate(dag.add(x, y)).value, frac(17n, 12n));
  assert.deepEqual(dag.evaluate(dag.sub(x, y)).value, frac(-1n, 12n));
  assert.deepEqual(dag.evaluate(dag.mul(x, y)).value, frac(1n, 2n));
  assert.deepEqual(dag.evaluate(dag.div(x, y)).value, frac(8n, 9n));
  assert.deepEqual(dag.evaluate(dag.neg(x)).value, frac(-2n, 3n));
});

test("raw leaf pairs are sign-normalized but not reduced until evaluation", () => {
  const dag = new RationalDAG();
  const leaf = dag.leaf(4n, -6n);
  // raw intermediate keeps magnitude; final value is canonical
  assert.deepEqual(dag.evaluate(leaf).value, frac(-2n, 3n));
  const zero = dag.leaf(0n, -9n);
  assert.deepEqual(dag.evaluate(zero).value, frac(0n));
});

test("comparison uses cross multiplication: no decimal expansion needed", () => {
  const dag = new RationalDAG();
  const a = dag.frac(frac(10n ** 100n + 3n, 10n ** 100n + 1n));
  const b = dag.frac(frac(10n ** 100n + 2n, 10n ** 100n + 1n));
  const r = dag.compare(a, b);
  assert.equal(r.status, "ok");
  assert.equal(r.result, 1);
  assert.deepEqual(dag.evaluate(dag.sub(a, b)).value, frac(1n, 10n ** 100n + 1n));
});

test("shared node is evaluated once (memoization) and reused by two parents", () => {
  const dag = new RationalDAG();
  const base = dag.leaf(1n, 3n);
  const p = dag.mul(base, base);
  const s = dag.add(base, base);
  const root = dag.div(p, s);
  assert.deepEqual(dag.evaluate(root).value, frac(1n, 6n));
});

test("bit budget: intermediates over budget mark the result incomplete", () => {
  const dag = new RationalDAG();
  const big = dag.leaf(2n ** 100n, 1n);
  const r64 = dag.evaluate(big, 64);
  assert.equal(r64.status, "incomplete");
  assert.equal(r64.value, null);
  assert.ok(r64.overBudget.includes(big));

  const roomy = dag.evaluate(big, 1000);
  assert.equal(roomy.status, "ok");
  assert.deepEqual(roomy.value, frac(2n ** 100n));
});

test("bit budget covers products whose intermediates grow", () => {
  const dag = new RationalDAG();
  let node = dag.leaf(2n ** 20n);
  for (let i = 0; i < 8; i += 1) {
    node = dag.mul(node, dag.leaf(2n ** 20n));
  }
  const tight = dag.evaluate(node, 100);
  assert.equal(tight.status, "incomplete");
  assert.ok(tight.overBudget.length > 0);
  const loose = dag.evaluate(node, 1000);
  assert.equal(loose.status, "ok");
  assert.deepEqual(loose.value, frac(2n ** 180n));
});

test("division by zero is reported, not thrown", () => {
  const dag = new RationalDAG();
  const z = dag.leaf(0n);
  const bad = dag.div(dag.leaf(1n), z);
  const r = dag.evaluate(bad);
  assert.equal(r.status, "incomplete");
  assert.deepEqual(r.divisionByZero, [bad]);
  const c = dag.compare(bad, bad);
  assert.equal(c.status, "incomplete");
  assert.equal(c.result, null);
});

test("zero-denominator leaf is rejected at construction", () => {
  const dag = new RationalDAG();
  assert.throws(() => dag.leaf(1n, 0n), /Zero denominator/);
});

test("unknown node ids are rejected", () => {
  const dag = new RationalDAG();
  assert.throws(() => dag.evaluate(99), /Unknown node/);
});
