import { test } from "node:test";
import assert from "node:assert/strict";
import {
  boolRef,
  cmpNode,
  constNode,
  inNode,
  notNode,
  andNode,
  orNode,
} from "../src/ast.ts";
import { simplify } from "../src/simplify.ts";
import {
  checkEquivalence,
  checkTransform,
  testSimplification,
} from "../src/tester.ts";
import { MISSING, TRUE } from "../src/trivalue.ts";
import type { Node } from "../src/ast.ts";
import type { Schema } from "../src/schema.ts";

const smallSchema: Schema = {
  x: { kind: "bool" },
  y: { kind: "bool" },
  n: { kind: "int", min: 1, max: 4 },
  r: { kind: "finite", values: ["p", "q"] },
};

test("tester REJECTS x OR NOT x -> true on nullable domain", () => {
  const expr = orNode([boolRef("x"), notNode(boolRef("x"))]);
  const report = checkEquivalence(expr, constNode(1), smallSchema);
  assert.equal(report.equivalent, false);
  assert.ok(report.mismatches.some((m) => m.before === MISSING && m.after === TRUE));
  assert.equal(report.checked, Number(report.domainSize));
});

test("tester REJECTS x OR NOT x -> true even against cmp leaves", () => {
  const expr = orNode([cmpNode("n", ">=", 3), notNode(cmpNode("n", ">=", 3))]);
  const report = checkEquivalence(expr, constNode(1), smallSchema);
  assert.equal(report.equivalent, false, "missing n makes it MISSING");
});

test("tester accepts identity transform over the whole domain", () => {
  const expr = andNode([boolRef("x"), orNode([boolRef("y"), cmpNode("n", "<", 3)])]);
  const report = checkEquivalence(expr, expr, smallSchema);
  assert.equal(report.equivalent, true);
});

test("checkTransform flags the unsound rule and declines null", () => {
  const unsoundLEM = (e: Node): Node | null =>
    e.kind === "or" &&
    e.args.length === 2 &&
    e.args[1].kind === "not" &&
    e.args[0].kind !== "const"
      ? constNode(1)
      : null;
  const report = checkTransform(
    () => orNode([boolRef("x"), notNode(boolRef("x"))]),
    unsoundLEM,
    smallSchema,
  );
  assert.equal(report.declined, false);
  assert.equal(report.equivalent, false);

  const declined = checkTransform(() => boolRef("x"), unsoundLEM, smallSchema);
  assert.equal(declined.declined, true);
});

test("provenance: simplification never invents sources", () => {
  const expr = andNode([boolRef("x", "src-x"), constNode(0, new Set(["src-f"]))]);
  const report = testSimplification(expr, smallSchema);
  assert.equal(report.equivalent, true);
  assert.deepEqual(report.inventedSources, []);
  assert.ok(report.lineage.every((l) => l.sources.every((s) => s === "src-f")));
});

test("FUZZ: hundreds of random expressions simplify equivalently across the domain", () => {
  function rng(seed: number): () => number {
    let state = seed >>> 0;
    return () => {
      state = (state * 1664525 + 1013904223) >>> 0;
      return state / 4294967296;
    };
  }
  const random = rng(1234567);
  const atoms: Node[] = [
    boolRef("x"),
    boolRef("y"),
    cmpNode("n", "<", 3),
    cmpNode("n", ">=", 2),
    cmpNode("n", "==", 4),
    inNode("r", ["p"]),
    inNode("r", ["q"]),
    constNode(0, new Set(["cf"])),
    constNode(1, new Set(["ct"])),
  ];
  function build(depth: number): Node {
    if (depth <= 0 || random() < 0.45) {
      const atom = atoms[Math.floor(random() * atoms.length)];
      return random() < 0.3 ? notNode(atom) : atom;
    }
    const kids = [build(depth - 1), build(depth - 1), build(depth - 1)];
    return random() < 0.5 ? andNode(kids) : orNode(kids);
  }
  for (let i = 0; i < 2000; i++) {
    const expr = build(3);
    const report = testSimplification(expr, smallSchema);
    assert.equal(
      report.equivalent,
      true,
      `fuzz case ${i}: ${JSON.stringify(report.mismatches.slice(0, 3), null, 2)}`,
    );
    assert.deepEqual(report.inventedSources, [], `case ${i}: invented sources`);
  }
});
