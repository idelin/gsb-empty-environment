import assert from "node:assert/strict";
import test from "node:test";
import {
  acceptsPath,
  areDisjointDFA,
  areEquivalentDFA,
  complementDFA,
  differenceDFA,
  emptyDFA,
  enumerateDFA,
  exactPathDFA,
  intersectDFA,
  isEmptyDFA,
  minimizeDFA,
  prefixDFA,
  unionDFA,
  universalDFA,
} from "../src/index.ts";

test("path-segment DFA operations preserve set semantics", () => {
  const prodA = exactPathDFA(["prod", "a"]);
  const prodB = exactPathDFA(["prod", "b"]);
  const prod = prefixDFA(["prod"]);

  assert.equal(acceptsPath(prodA, ["prod", "a"]), true);
  assert.equal(acceptsPath(prodA, ["prod", "b"]), false);
  assert.equal(acceptsPath(prod, ["prod", "a", "b"]), true);
  assert.equal(isEmptyDFA(intersectDFA(prodA, prodB)), true);
  assert.equal(acceptsPath(unionDFA(prodA, prodB), ["prod", "b"]), true);
  assert.equal(acceptsPath(differenceDFA(prod, prodA), ["prod", "b"]), true);
  assert.equal(acceptsPath(differenceDFA(prod, prodA), ["prod", "a"]), false);
  assert.equal(acceptsPath(complementDFA(prodA), ["prod", "a"]), false);
  assert.equal(acceptsPath(complementDFA(prodA), ["prod", "c"]), true);
  assert.equal(areDisjointDFA(prodA, prodB), true);
  assert.equal(areEquivalentDFA(minimizeDFA(prodA), prodA), true);
  assert.equal(isEmptyDFA(emptyDFA(["a"])), true);
  assert.equal(acceptsPath(universalDFA(), ["anything", "at", "all"]), true);
});

test("bounded enumeration is exhaustive for a tiny alphabet", () => {
  const universe = prefixDFA(["prod"]);
  const words = enumerateDFA(universe, {
    maxDepth: 2,
    maxWords: 100,
  });
  assert.deepEqual(
    words.map((word) => word.join("/")),
    ["prod", "prod/prod"],
  );

  const finite = unionDFA(exactPathDFA(["prod", "a"]), exactPathDFA(["prod", "b"]));
  assert.deepEqual(
    enumerateDFA(finite, { maxDepth: 4 }).map((word) => word.join("/")),
    ["prod/a", "prod/b"],
  );
});
