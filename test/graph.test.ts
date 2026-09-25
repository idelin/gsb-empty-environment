import { test } from "node:test";
import assert from "node:assert/strict";
import { MigrationError } from "../src/errors.ts";
import { VersionGraph, type MigrationStep } from "../src/graph.ts";

const passthrough = (
  name: string,
  from: string,
  to: string,
  cost?: number,
): MigrationStep => ({
  name,
  from,
  to,
  cost,
  inputSchema: { type: "any" },
  outputSchema: { type: "any" },
  apply: (d) => d,
});

function catchMigration(fn: () => unknown): MigrationError {
  try {
    fn();
  } catch (e) {
    assert.ok(e instanceof MigrationError, "expected MigrationError");
    return e;
  }
  assert.fail("expected function to throw");
}

test("selects the unique cheapest route deterministically", () => {
  const g = new VersionGraph();
  g.addStep(passthrough("a-b", "a", "b"));
  g.addStep(passthrough("b-c", "b", "c"));
  g.addStep(passthrough("a-c-direct", "a", "c", 5));
  const route = g.findPath("a", "c");
  assert.deepEqual(route.steps.map((s) => s.name), ["a-b", "b-c"]);
  assert.equal(route.totalCost, 2);
});

test("rejects two equal-cost routes as ambiguous", () => {
  const g = new VersionGraph();
  g.addStep(passthrough("a-x", "a", "x"));
  g.addStep(passthrough("x-c", "x", "c"));
  g.addStep(passthrough("a-y", "a", "y"));
  g.addStep(passthrough("y-c", "y", "c"));
  const err = catchMigration(() => g.findPath("a", "c"));
  assert.equal(err.code, "AMBIGUOUS_PATH");
  assert.match(err.message, /route1|a-x|x-c/);
  assert.match(err.message, /a-y/);
  assert.equal(err.path, "a -> c");
});

test("equal-cost parallel edges on the same hop are ambiguous too", () => {
  const g = new VersionGraph();
  g.addStep(passthrough("one", "a", "b", 3));
  g.addStep(passthrough("two", "a", "b", 3));
  assert.throws(() => g.findPath("a", "b"), /equal cost 3/);
});

test("different costs are not ambiguous: cheaper wins", () => {
  const g = new VersionGraph();
  g.addStep(passthrough("one", "a", "b", 2));
  g.addStep(passthrough("two", "a", "b", 3));
  assert.deepEqual(g.findPath("a", "b").steps.map((s) => s.name), ["one"]);
});

test("missing route raises NO_PATH", () => {
  const g = new VersionGraph();
  g.addStep(passthrough("a-b", "a", "b"));
  const err = catchMigration(() => g.findPath("a", "z"));
  assert.equal(err.code, "NO_PATH");
});

test("same source and target is an empty route", () => {
  const g = new VersionGraph();
  const route = g.findPath("a", "a");
  assert.deepEqual(route.steps, []);
  assert.equal(route.totalCost, 0);
});

test("non-positive cost is rejected", () => {
  const g = new VersionGraph();
  assert.throws(() => g.addStep(passthrough("bad", "a", "b", 0)), /invalid cost/);
});

test("duplicate step names are rejected", () => {
  const g = new VersionGraph();
  g.addStep(passthrough("dup", "a", "b"));
  assert.throws(() => g.addStep(passthrough("dup", "b", "c")), /duplicate step name/);
});
