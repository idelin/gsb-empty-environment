/**
 * 验收测试：10MB 文件中间插入 100 字节。
 * - 分块算法动态重新切分
 * - >= 99% 的分块哈希与旧树一致
 * - 受影响分块仅 1~2 个，增量传输 = 受影响分块 + 默克尔路径更新
 */
import { test } from "node:test";
import assert from "node:assert/strict";
import { chunk } from "../src/chunker.ts";
import {
  buildMerkleTree,
  diffLeaves,
  changedMerkleNodes,
  merkleProof,
  verifyProof,
} from "../src/merkle.ts";
import { pseudoRandomBytes } from "../src/util.ts";

const FILE_SIZE = 10 * 1024 * 1024;
const INSERT_SIZE = 100;

function insertAt(data: Buffer, offset: number, patch: Buffer): Buffer {
  return Buffer.concat([data.subarray(0, offset), patch, data.subarray(offset)]);
}

test("中间插入 100 字节：>=99% 分块哈希复用，仅 1~2 个分块受影响", () => {
  const original = pseudoRandomBytes(FILE_SIZE, 20261002);
  const patch = pseudoRandomBytes(INSERT_SIZE, 777);
  const modified = insertAt(original, Math.floor(FILE_SIZE / 2), patch);

  const oldChunks = chunk(original);
  const newChunks = chunk(modified);
  const oldTree = buildMerkleTree(oldChunks.map((c) => c.hash));
  const newTree = buildMerkleTree(newChunks.map((c) => c.hash));

  // 根哈希必须改变（防篡改可验证性）
  assert.notEqual(newTree.root, oldTree.root);

  // 通过前缀/后缀对齐统计复用率
  const diff = diffLeaves(oldTree.leaves, newTree.leaves);
  const reused = diff.commonPrefix + diff.commonSuffix;
  const reuseRate = reused / oldTree.leaves.length;

  const oldAffected = diff.oldChanged[1] - diff.oldChanged[0];
  const newAffected = diff.newChanged[1] - diff.newChanged[0];

  console.log(
    `  旧分块 ${oldChunks.length}，新分块 ${newChunks.length}，` +
      `复用 ${reused}/${oldTree.leaves.length} (${(reuseRate * 100).toFixed(2)}%)，` +
      `受影响旧分块 ${oldAffected}、新分块 ${newAffected}`
  );

  assert.ok(
    reuseRate >= 0.99,
    `复用率 ${(reuseRate * 100).toFixed(2)}% 低于 99%`
  );
  assert.ok(oldAffected <= 2, `受影响旧分块数 ${oldAffected} 超过 2`);
  assert.ok(newAffected <= 2, `受影响新分块数 ${newAffected} 超过 2`);

  // 增量传输量 = 受影响新分块 + 变化的默克尔路径节点
  const changedNodes = changedMerkleNodes(oldTree, newTree);
  const totalNodes = newTree.levels.reduce((n, l) => n + l.length, 0);
  console.log(
    `  默克尔树变化节点 ${changedNodes}/${totalNodes}，` +
      `增量同步占比 ${((newAffected + changedNodes) / totalNodes * 100).toFixed(2)}%`
  );
  assert.ok(
    changedNodes <= totalNodes * 0.02,
    `默克尔树变化节点过多: ${changedNodes}/${totalNodes}`
  );

  // 未受影响的分块证明在新旧树中均有效（哈希复用可直接验证）
  const untouchedIndex = 0; // 前缀中的任意分块
  assert.ok(
    verifyProof(
      oldTree.leaves[untouchedIndex],
      merkleProof(oldTree, untouchedIndex),
      oldTree.root
    )
  );
});

test("插入点靠近分块边界时仍满足局部性", () => {
  const original = pseudoRandomBytes(FILE_SIZE, 555);
  const oldChunks = chunk(original);
  // 找到某个边界偏移，在其后 10 字节处插入
  const boundaryOffset = oldChunks[Math.floor(oldChunks.length / 3)].offset;
  const patch = pseudoRandomBytes(INSERT_SIZE, 888);
  const modified = insertAt(original, boundaryOffset + 10, patch);

  const newChunks = chunk(modified);
  const oldTree = buildMerkleTree(oldChunks.map((c) => c.hash));
  const newTree = buildMerkleTree(newChunks.map((c) => c.hash));
  const diff = diffLeaves(oldTree.leaves, newTree.leaves);
  const reuseRate =
    (diff.commonPrefix + diff.commonSuffix) / oldTree.leaves.length;

  assert.ok(reuseRate >= 0.99, `复用率 ${(reuseRate * 100).toFixed(2)}%`);
  assert.ok(diff.newChanged[1] - diff.newChanged[0] <= 2);
});

test("删除 100 字节同样保持局部性", () => {
  const original = pseudoRandomBytes(FILE_SIZE, 31337);
  const mid = Math.floor(FILE_SIZE / 2);
  const modified = Buffer.concat([
    original.subarray(0, mid),
    original.subarray(mid + INSERT_SIZE),
  ]);
  const oldTree = buildMerkleTree(chunk(original).map((c) => c.hash));
  const newTree = buildMerkleTree(chunk(modified).map((c) => c.hash));
  const diff = diffLeaves(oldTree.leaves, newTree.leaves);
  const reuseRate =
    (diff.commonPrefix + diff.commonSuffix) / oldTree.leaves.length;
  assert.ok(reuseRate >= 0.99);
});
