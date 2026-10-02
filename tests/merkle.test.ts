import test from "node:test";
import assert from "node:assert/strict";
import {
  buildTree,
  getProof,
  hashLeaf,
  verifyProof,
} from "../src/merkle.ts";

const data = ["alpha", "beta", "gamma", "delta", "epsilon"];

test("root is deterministic for the same leaves", () => {
  const leaves = data.map(hashLeaf);
  const a = buildTree(leaves);
  const b = buildTree(leaves);
  assert.ok(a.root.equals(b.root));
});

test("proof verifies for every leaf", () => {
  const leaves = data.map(hashLeaf);
  const tree = buildTree(leaves);
  for (let i = 0; i < leaves.length; i++) {
    assert.ok(verifyProof(leaves[i], getProof(tree, i), tree.root));
  }
});

test("odd number of leaves is handled", () => {
  const leaves = data.map(hashLeaf);
  const tree = buildTree(leaves);
  assert.ok(verifyProof(leaves[4], getProof(tree, 4), tree.root));
});

test("tampered leaf fails verification", () => {
  const leaves = data.map(hashLeaf);
  const tree = buildTree(leaves);
  const forged = hashLeaf("alpha-tampered");
  assert.equal(verifyProof(forged, getProof(tree, 0), tree.root), false);
});

test("empty tree is rejected", () => {
  assert.throws(() => buildTree([]));
});
