import assert from "node:assert/strict";
import test from "node:test";

import { FixEngine, type FixRule } from "../src/engine.ts";
import { canonicalStringify, deepEqual, type JsonValue } from "../src/json.ts";
import { formatPointer, getAt, parsePointer } from "../src/pointer.ts";
import { coerceBooleanStrings, rewriteValue, trimStrings } from "../src/rules.ts";

test("reaches a fixpoint and applies every applicable rule exactly once", () => {
  const input = {
    features: { darkMode: "true", beta: "false" },
    name: "  alice  ",
  };
  const engine = new FixEngine(input, [
    coerceBooleanStrings("coerce-bool", 10, ["/features/darkMode", "/features/beta"]),
    trimStrings("trim-name", 5, ["/name"]),
  ]);
  const report = engine.run();
  assert.equal(report.termination, "fixpoint");
  assert.equal(report.steps, 3);
  assert.deepEqual(report.state, {
    features: { darkMode: true, beta: false },
    name: "alice",
  });
});

test("higher priority rules are applied before lower priority ones", () => {
  const input = { a: 1, b: 0 };
  const engine = new FixEngine(input, [
    rewriteValue("low", 1, "/a", 1, 2),
    rewriteValue("high", 10, "/b", 0, 1),
  ]);
  const report = engine.run();
  assert.equal(report.termination, "fixpoint");
  assert.deepEqual(
    report.journal.map((entry) => entry.ruleId),
    ["high", "low"],
  );
});

test("a rule whose precondition is not satisfied is never executed", () => {
  const input = { n: 3 };
  const lyingRule: FixRule = {
    id: "lying-rule",
    priority: 10,
    // Proposes a match even though the guard value does not hold;
    // the engine must re-check `expect` and refuse to execute it.
    match: () => [{ path: "/n", expect: 5, replacement: 6 }],
  };
  const engine = new FixEngine(input, [lyingRule]);
  const report = engine.run();
  assert.equal(report.termination, "fixpoint");
  assert.equal(report.steps, 0);
  assert.deepEqual(report.state, { n: 3 });
  assert.equal(report.skipped.length, 1);
  assert.equal(report.skipped[0].reason, "precondition-not-met");
  assert.equal(report.skipped[0].path, "/n");
  assert.equal(report.skipped[0].expected, 5);
  assert.equal(report.skipped[0].actual, 3);
});

test("a match pointing at a missing path is skipped with a located error", () => {
  const rule: FixRule = {
    id: "bad-path",
    priority: 1,
    match: () => [{ path: "/does/not/exist", replacement: 1 }],
  };
  const report = new FixEngine({ a: 1 }, [rule]).run();
  assert.equal(report.termination, "fixpoint");
  assert.equal(report.steps, 0);
  assert.equal(report.skipped[0].reason, "path-not-found");
  assert.equal(report.skipped[0].path, "/does/not/exist");
});

test("detects oscillation between rule A (x->y) and rule B (y->x) and stops", () => {
  const input = { mode: "x" };
  const engine = new FixEngine(input, [
    rewriteValue("A:x-to-y", 10, "/mode", "x", "y"),
    rewriteValue("B:y-to-x", 10, "/mode", "y", "x"),
  ]);
  const report = engine.run();
  assert.equal(report.termination, "oscillation");
  assert.equal(report.steps, 2);
  const oscillation = report.oscillation!;
  assert.deepEqual(
    oscillation.cycle.map((c) => c.ruleId),
    ["A:x-to-y", "B:y-to-x"],
  );
  assert.deepEqual(
    oscillation.cycle.map((c) => c.path),
    ["/mode", "/mode"],
  );
  assert.equal(oscillation.periodStartStep, 1);
  // State at stop equals the input again (one full cycle).
  assert.deepEqual(report.state, input);
});

test("detects a conflict when same-priority rules rewrite one path differently", () => {
  const input = { theme: "light" };
  const engine = new FixEngine(input, [
    rewriteValue("set-theme-dark", 10, "/theme", "light", "dark"),
    rewriteValue("set-theme-blue", 10, "/theme", "light", "blue"),
  ]);
  const report = engine.run();
  assert.equal(report.termination, "conflict");
  assert.equal(report.steps, 0);
  const conflict = report.conflict!;
  assert.equal(conflict.path, "/theme");
  assert.equal(conflict.current, "light");
  assert.deepEqual(conflict.proposals, [
    { ruleId: "set-theme-blue", replacement: "blue" },
    { ruleId: "set-theme-dark", replacement: "dark" },
  ]);
  // Nothing was applied: the state is untouched.
  assert.deepEqual(report.state, input);
});

test("a higher-priority rule wins over a conflicting lower-priority rule", () => {
  const input = { theme: "light" };
  const engine = new FixEngine(input, [
    rewriteValue("set-theme-dark", 10, "/theme", "light", "dark"),
    rewriteValue("set-theme-blue", 1, "/theme", "light", "blue"),
  ]);
  const report = engine.run();
  assert.equal(report.termination, "fixpoint");
  assert.deepEqual(report.state, { theme: "dark" });
});

test("terminates on the step budget when no fixpoint is reachable in time", () => {
  const input = { mode: "x" };
  const engine = new FixEngine(
    input,
    [
      rewriteValue("A:x-to-y", 10, "/mode", "x", "y"),
      rewriteValue("B:y-to-x", 10, "/mode", "y", "x"),
    ],
    { maxSteps: 1 },
  );
  const report = engine.run();
  assert.equal(report.termination, "budget");
  assert.equal(report.steps, 1);
  assert.equal(report.budget, 1);
  assert.deepEqual(report.state, { mode: "y" });
});

test("rollback restores the original input, not a normalized copy", () => {
  // Key order deliberately differs from sorted order; arrays stay ordered.
  const input = { z: 1, a: { y: "true", b: [3, 1, 2] }, m: "  pad " };
  const snapshot = JSON.stringify(input);
  const engine = new FixEngine(input, [
    coerceBooleanStrings("coerce-bool", 10, ["/a/y"]),
    trimStrings("trim", 5, ["/m"]),
  ]);
  const report = engine.run();
  assert.equal(report.termination, "fixpoint");
  assert.equal(report.steps, 2);

  const restored = engine.rollback();
  // Byte-for-byte identical to the input, including original key order.
  assert.equal(JSON.stringify(restored), snapshot);
  // The caller's input object was never mutated.
  assert.equal(JSON.stringify(input), snapshot);
  // The working state really did change (rollback is not a no-op).
  assert.notEqual(JSON.stringify(report.state), snapshot);
});

test("journal inverse replay reproduces the input (reversible edits)", () => {
  const input = { mode: "x", items: [" a ", "b "] };
  const engine = new FixEngine(input, [
    rewriteValue("rename", 10, "/mode", "x", "y"),
    trimStrings("trim", 5, ["/items/0", "/items/1"]),
  ]);
  const report = engine.run();
  assert.equal(report.termination, "fixpoint");
  assert.equal(report.journal.length, 3);
  for (const entry of report.journal) {
    assert.ok(entry.path.startsWith("/"));
    assert.ok("before" in entry && "after" in entry);
  }
  assert.deepEqual(engine.rollback(), input);
});

test("object key order is irrelevant, array order is significant", () => {
  assert.equal(
    canonicalStringify({ b: 1, a: 2 }),
    canonicalStringify({ a: 2, b: 1 }),
  );
  assert.notEqual(canonicalStringify([1, 2]), canonicalStringify([2, 1]));
  assert.ok(deepEqual({ x: [1, { q: 0, p: 1 }] }, { x: [1, { p: 1, q: 0 }] }));
});

test("JSON Pointer paths locate errors and support escaping", () => {
  assert.deepEqual(parsePointer("/a/0/b~1c/~0"), ["a", "0", "b/c", "~"]);
  assert.equal(formatPointer(["a", "0", "b/c", "~"]), "/a/0/b~1c/~0");
  const doc = { "a/b": { "~key": [10, 20] } };
  assert.equal(getAt(doc, "/a~1b/~0key/1"), 20);
});

test("oscillation evidence pins the exact paths and rules of the cycle", () => {
  const input: JsonValue = { left: "x", right: "keep" };
  const engine = new FixEngine(input, [
    rewriteValue("A:x-to-y", 10, "/left", "x", "y"),
    rewriteValue("B:y-to-x", 10, "/left", "y", "x"),
  ]);
  const report = engine.run();
  assert.equal(report.termination, "oscillation");
  assert.equal(report.oscillation!.cycle[0].path, "/left");
  assert.equal(typeof report.oscillation!.repeatedStateHash, "string");
  // Rollback still restores the input after an oscillating run.
  assert.deepEqual(engine.rollback(), input);
});
