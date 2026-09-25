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
import { evaluate } from "../src/eval.ts";
import { FALSE, MISSING, TRUE } from "../src/trivalue.ts";
import { SchemaError } from "../src/schema.ts";
import type { Schema } from "../src/schema.ts";

const schema: Schema = {
  flag: { kind: "bool" },
  n: { kind: "int", min: 0, max: 5 },
  role: { kind: "finite", values: ["a", "b"] },
};

test("absent boolean / finite / int leaves evaluate to MISSING", () => {
  assert.equal(evaluate(boolRef("flag"), {}, schema), MISSING);
  assert.equal(evaluate(inNode("role", ["a"]), {}, schema), MISSING);
  assert.equal(evaluate(cmpNode("n", ">", 2), {}, schema), MISSING);
});

test("present leaves evaluate classically", () => {
  assert.equal(evaluate(boolRef("flag"), { flag: true }, schema), TRUE);
  assert.equal(evaluate(inNode("role", ["a"]), { role: "b" }, schema), FALSE);
  assert.equal(evaluate(cmpNode("n", ">", 2), { n: 3 }, schema), TRUE);
  assert.equal(evaluate(cmpNode("n", "!=", 2), { n: 2 }, schema), FALSE);
  assert.equal(evaluate(cmpNode("n", "!=", 2), { n: 1 }, schema), TRUE);
});

test("missing propagates but constants decide when they can", () => {
  const x = boolRef("flag");
  assert.equal(evaluate(orNode([x, notNode(x)]), {}, schema), MISSING);
  assert.equal(evaluate(andNode([x, constNode(0)]), {}, schema), FALSE);
  assert.equal(evaluate(orNode([x, constNode(1)]), {}, schema), TRUE);
  assert.equal(evaluate(andNode([x, constNode(1)]), {}, schema), MISSING);
  assert.equal(evaluate(orNode([x, constNode(0)]), {}, schema), MISSING);
});

test("undeclared attribute reads are rejected", () => {
  assert.throws(
    () => evaluate(boolRef("sub"), {}, schema),
    (e: unknown) => e instanceof SchemaError && /not declared/.test(String(e)),
  );
});

test("out-of-domain assignments are rejected", () => {
  assert.throws(() => evaluate(boolRef("flag"), { n: 99 }, schema), SchemaError);
});
