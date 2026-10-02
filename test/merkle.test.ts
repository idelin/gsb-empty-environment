import { test } from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import {
  buildMerkleTree,
  merkleProof,
  verifyProof,
} from "../src/merkle.ts";

const leaf = (s: string) => createHash("sha256").update(s).digest("hex");

test("单叶子树根即叶子", () => {
  const tree = buildMerkleTree([leaf("a")]);
  assert.equal(tree.root, leaf("a"));
  assert.equal(tree.levels.length, 1);
});

test("所有叶子的默克尔证明均可验证", () => {
  const leaves = Array.from({ length: 17 }, (_, i) => leaf(`chunk-${i}`));
  const tree = buildMerkleTree(leaves);
  for (let i = 0; i < leaves.length; i++) {
    const proof = merkleProof(tree, i);
    assert.ok(verifyProof(leaves[i], proof, tree.root), `叶子 ${i} 验证失败`);
  }
});

test("篡改叶子后证明验证失败", () => {
  const leaves = Array.from({ length: 8 }, (_, i) => leaf(`chunk-${i}`));
  const tree = buildMerkleTree(leaves);
  const proof = merkleProof(tree, 3);
  assert.equal(verifyProof(leaf("evil"), proof, tree.root), false);
});

test("奇数叶子晋升：树结构确定且可复现", () => {
  const leaves = Array.from({ length: 5 }, (_, i) => leaf(`x${i}`));
  const a = buildMerkleTree(leaves);
  const b = buildMerkleTree(leaves);
  assert.equal(a.root, b.root);
  assert.equal(a.levels[0].length, 5);
  assert.equal(a.levels[1].length, 3); // 2 对 + 1 晋升
});
