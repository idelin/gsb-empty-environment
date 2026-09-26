import assert from "node:assert/strict";
import test from "node:test";
import {
  explore,
  findInvariantViolation,
  findUnreachableStates,
  proveUnreachable,
} from "../src/checker.ts";
import { editorSpecV1 } from "../src/editorSpec.ts";
import type { Spec } from "../src/spec.ts";

const counterSpec: Spec = {
  name: "counter",
  version: 1,
  states: [{ id: "root" }],
  initialData: () => ({ n: 0 }),
  transitions: [{ event: "inc", action: (d) => ({ n: (d.n as number) + 1 }) }],
  invariants: [{ name: "n-at-most-2", check: (d) => (d.n as number) <= 2 }],
  eventDomains: { inc: [{}] },
};

test("finds the shortest counterexample to a broken invariant", () => {
  const cx = findInvariantViolation(counterSpec);
  assert.ok(cx);
  assert.equal(cx.invariant, "n-at-most-2");
  assert.equal(cx.trace.length, 3); // BFS 最短：inc x3
  assert.equal(cx.finalConfig.data.n, 3);
  assert.ok(cx.trace.every((s) => s.event.type === "inc"));
});

test("sound spec yields no counterexample", () => {
  assert.equal(findInvariantViolation(editorSpecV1()), null);
});

test("all editor states are reachable under finite domains", () => {
  const report = findUnreachableStates(editorSpecV1());
  assert.equal(report.truncated, false);
  assert.deepEqual(report.unreachable, []);
});

const islandSpec: Spec = {
  name: "island",
  version: 1,
  states: [
    { id: "root", initial: "home" },
    { id: "home", parent: "root" },
    { id: "away", parent: "root" },
    { id: "never", parent: "root" },
  ],
  initialData: () => ({ flag: false }),
  transitions: [
    { event: "go", from: "home", to: "away" },
    { event: "back", from: "away", to: "home" },
  ],
  invariants: [],
  eventDomains: { go: [{}], back: [{}] },
};

test("unreachable states are reported", () => {
  const report = findUnreachableStates(islandSpec);
  assert.deepEqual(report.unreachable, ["never"]);
});

test("proveUnreachable confirms truly unreachable states", () => {
  const proof = proveUnreachable(islandSpec, "never");
  assert.equal(proof.unreachable, true);
  assert.equal(proof.trace, null);
});

test("proveUnreachable returns the shortest trace for reachable states", () => {
  const proof = proveUnreachable(islandSpec, "away");
  assert.equal(proof.unreachable, false);
  assert.equal(proof.trace!.length, 1);
  assert.equal(proof.trace![0]!.event.type, "go");
});

test("explore enumerates the finite reachable state space", () => {
  const result = explore(editorSpecV1());
  assert.equal(result.truncated, false);
  assert.ok(result.states.size > 0);
  assert.ok(result.states.size < 1000);
  for (const id of ["idle", "clean", "dirty", "locked"]) {
    assert.ok(result.visitedStateIds.has(id), `expected ${id} to be visited`);
  }
});
