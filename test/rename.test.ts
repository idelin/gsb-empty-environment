import { test } from "node:test";
import assert from "node:assert/strict";
import { parse, bind, rename, computePaths, verifyIsomorphism } from "../src/index.ts";

function offsetOf(source: string, needle: string, occurrence = 1): number {
  let idx = -1;
  for (let i = 0; i < occurrence; i++) {
    idx = source.indexOf(needle, idx + 1);
    if (idx < 0) throw new Error(`needle not found: ${needle} (#${occurrence})`);
  }
  return idx;
}

test("renames a declaration and all of its references across nested blocks", () => {
  const src = "let x = 1;\nlet y = x + 2;\n{\n  y;\n  x;\n}\n";
  const result = rename(src, offsetOf(src, "x"), "alpha");
  assert.ok(result.ok, result.ok ? "" : result.reason);
  assert.equal(result.source, "let alpha = 1;\nlet y = alpha + 2;\n{\n  y;\n  alpha;\n}\n");
});

test("rename can be triggered from a reference, not only the declaration", () => {
  const src = "let x = 1;\nx;\n";
  const result = rename(src, offsetOf(src, "x", 2), "z");
  assert.ok(result.ok, result.ok ? "" : result.reason);
  assert.equal(result.source, "let z = 1;\nz;\n");
});

test("same-spelled identifiers owned by other scopes are untouched", () => {
  const src = "let x = 1;\nx;\n{\n  let x = 2;\n  x;\n}\n";
  const result = rename(src, offsetOf(src, "x"), "y");
  assert.ok(result.ok, result.ok ? "" : result.reason);
  assert.equal(result.source, "let y = 1;\ny;\n{\n  let x = 2;\n  x;\n}\n");
});

test("renaming an inner binding leaves outer same-spelled references untouched", () => {
  const src = "let x = 1;\nx;\n{\n  let x = 2;\n  x;\n}\n";
  const innerDecl = src.indexOf("x", src.indexOf("{"));
  const result = rename(src, innerDecl, "y");
  assert.ok(result.ok, result.ok ? "" : result.reason);
  assert.equal(result.source, "let x = 1;\nx;\n{\n  let y = 2;\n  y;\n}\n");
});

test("rejects a rename whose references would be captured by an intervening binding", () => {
  const src = "let a = 1;\n{\n  let b = 2;\n  a;\n}\n";
  const result = rename(src, offsetOf(src, "a"), "b");
  assert.ok(!result.ok);
  assert.match(result.reason, /captured/);
});

test("rejects a rename that would capture an existing reference to another binding", () => {
  const src = "let b = 1;\n{\n  let a = 2;\n  b;\n}\n";
  const result = rename(src, offsetOf(src, "a"), "b");
  assert.ok(!result.ok);
  assert.match(result.reason, /capture/);
});

test("rejects a rename that would capture an unbound reference", () => {
  const src = "let a = 1;\n{\n  b;\n}\n";
  const result = rename(src, offsetOf(src, "a"), "b");
  assert.ok(!result.ok);
  assert.match(result.reason, /capture/);
});

test("rejects a rename that changes same-scope shadowing order", () => {
  const src = "let a = 1;\nlet b = 2;\na;\n";
  const result = rename(src, offsetOf(src, "a"), "b");
  assert.ok(!result.ok);
  assert.match(result.reason, /captured/);
});

test("allows a rename that merely shadows in a disjoint scope", () => {
  const src = "let a = 1;\na;\n{\n  let b = 2;\n  b;\n}\n";
  const result = rename(src, offsetOf(src, "a"), "b");
  assert.ok(result.ok, result.ok ? "" : result.reason);
  assert.equal(result.source, "let b = 1;\nb;\n{\n  let b = 2;\n  b;\n}\n");
});

test("references before a declaration are not visible to it", () => {
  const src = "let x = 1;\n{\n  x;\n  let x = 2;\n  x;\n}\n";
  const program = parse(src);
  const bound = bind(program);
  const idents = [...bound.resolutions.keys()].filter((i) => i.text === "x");
  assert.equal(idents.length, 2);
  const [before, after] = idents;
  // The reference before the inner `let` resolves to the outer binding...
  assert.equal(bound.resolutions.get(before)!.nameSpan.start, offsetOf(src, "x"));
  // ...and the one after resolves to the inner binding.
  assert.equal(bound.resolutions.get(after)!.nameSpan.start, src.indexOf("let x = 2") + 4);
});

test("renaming an outer binding skips same-block references that precede the inner declaration", () => {
  const src = "let x = 1;\n{\n  x;\n  let x = 2;\n  x;\n}\n";
  const result = rename(src, offsetOf(src, "x"), "y");
  assert.ok(result.ok, result.ok ? "" : result.reason);
  assert.equal(result.source, "let y = 1;\n{\n  y;\n  let x = 2;\n  x;\n}\n");
});

test("renaming an inner binding skips the reference before its declaration", () => {
  const src = "let x = 1;\n{\n  x;\n  let x = 2;\n  x;\n}\n";
  const innerDecl = src.indexOf("let x = 2") + 4;
  const result = rename(src, innerDecl, "y");
  assert.ok(result.ok, result.ok ? "" : result.reason);
  assert.equal(result.source, "let x = 1;\n{\n  x;\n  let y = 2;\n  y;\n}\n");
});

test("a declaration never captures references that precede it in its own block", () => {
  const src = "let x = 10;\n{\n  x;\n  let a = 1;\n}\n";
  const result = rename(src, offsetOf(src, "a"), "x");
  assert.ok(result.ok, result.ok ? "" : result.reason);
  assert.equal(result.source, "let x = 10;\n{\n  x;\n  let x = 1;\n}\n");
});

test("a declaration does not capture references inside its own initializer", () => {
  const src = "let x = 10;\nlet a = x;\n";
  const result = rename(src, offsetOf(src, "a"), "x");
  assert.ok(result.ok, result.ok ? "" : result.reason);
  assert.equal(result.source, "let x = 10;\nlet x = x;\n");
});

test("rejects invalid new names", () => {
  const src = "let a = 1;\n";
  assert.ok(!rename(src, offsetOf(src, "a"), "let").ok);
  assert.ok(!rename(src, offsetOf(src, "a"), "1abc").ok);
  assert.ok(!rename(src, offsetOf(src, "a"), "a-b").ok);
});

test("renaming to the same name is a no-op", () => {
  const src = "let a = 1;\na;\n";
  const result = rename(src, offsetOf(src, "a"), "a");
  assert.ok(result.ok, result.ok ? "" : result.reason);
  assert.equal(result.source, src);
  assert.deepEqual(result.edits, []);
});

test("reports an error when there is no binding at the offset", () => {
  const src = "let a = 1;\nb;\n";
  const result = rename(src, offsetOf(src, "b"), "c");
  assert.ok(!result.ok);
  assert.match(result.reason, /no binding/);
});

test("reports an error for unparseable source", () => {
  const result = rename("let = ;", 0, "x");
  assert.ok(!result.ok);
  assert.match(result.reason, /unparseable/);
});

test("binding-graph isomorphism check accepts a valid rename", () => {
  const src = "let x = 1;\n{\n  let y = x;\n  y;\n}\n";
  const result = rename(src, offsetOf(src, "x"), "w");
  assert.ok(result.ok, result.ok ? "" : result.reason);
  const oldProgram = parse(src);
  const oldBound = bind(oldProgram);
  const newProgram = parse(result.source);
  const newBound = bind(newProgram);
  const oldPaths = computePaths(oldProgram);
  const edited = new Set<string>();
  for (const b of oldBound.bindings) if (b.name === "x") edited.add(oldPaths.get(b.decl)!);
  for (const [ident, target] of oldBound.resolutions) {
    if (target && target.name === "x") edited.add(oldPaths.get(ident)!);
  }
  assert.equal(verifyIsomorphism(oldProgram, oldBound, newProgram, newBound, edited, "w"), null);
});

test("binding-graph isomorphism check detects a capture-inducing edit", () => {
  const src = "let a = 1;\n{\n  let b = 2;\n  a;\n}\n";
  // Simulate a broken rename that rewrites a -> b without any checks.
  const bad = "let b = 1;\n{\n  let b = 2;\n  b;\n}\n";
  const oldProgram = parse(src);
  const oldBound = bind(oldProgram);
  const newProgram = parse(bad);
  const newBound = bind(newProgram);
  const oldPaths = computePaths(oldProgram);
  const edited = new Set<string>();
  for (const b of oldBound.bindings) if (b.name === "a") edited.add(oldPaths.get(b.decl)!);
  for (const [ident, target] of oldBound.resolutions) {
    if (target && target.name === "a") edited.add(oldPaths.get(ident)!);
  }
  const problem = verifyIsomorphism(oldProgram, oldBound, newProgram, newBound, edited, "b");
  assert.ok(problem);
  assert.match(problem!, /resolution of 'a'.*changed/);
});

test("binding-graph isomorphism check detects edits to non-target identifiers", () => {
  const src = "let x = 1;\nlet y = x;\n";
  const bad = "let w = 1;\nlet y = w;\n".replace("let y", "let z"); // also renames y's declaration
  const oldProgram = parse(src);
  const oldBound = bind(oldProgram);
  const newProgram = parse(bad);
  const newBound = bind(newProgram);
  const oldPaths = computePaths(oldProgram);
  const edited = new Set<string>();
  for (const b of oldBound.bindings) if (b.name === "x") edited.add(oldPaths.get(b.decl)!);
  for (const [ident, target] of oldBound.resolutions) {
    if (target && target.name === "x") edited.add(oldPaths.get(ident)!);
  }
  const problem = verifyIsomorphism(oldProgram, oldBound, newProgram, newBound, edited, "w");
  assert.ok(problem);
  assert.match(problem!, /non-target declaration/);
});
