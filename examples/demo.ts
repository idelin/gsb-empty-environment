/**
 * 运行样例：10MB 文件中间插入 100 字节，演示增量同步。
 * 用法：node examples/demo.ts   （Node >= 22.6，需 --experimental-strip-types；Node >= 23.6 直接运行）
 */
import { chunk } from "../src/chunker.ts";
import {
  buildMerkleTree,
  diffLeaves,
  changedMerkleNodes,
} from "../src/merkle.ts";
import { pseudoRandomBytes } from "../src/util.ts";

const SIZE = 10 * 1024 * 1024;
const original = pseudoRandomBytes(SIZE, 20261002);
const patch = pseudoRandomBytes(100, 777);
const mid = Math.floor(SIZE / 2);
const modified = Buffer.concat([
  original.subarray(0, mid),
  patch,
  original.subarray(mid),
]);

console.log("== 内容定义分块 + 默克尔树 增量同步演示 ==\n");
console.log(`原始文件: ${SIZE} 字节, 在中间偏移 ${mid} 处插入 100 字节\n`);

const t0 = performance.now();
const oldChunks = chunk(original);
const t1 = performance.now();
const newChunks = chunk(modified);
const t2 = performance.now();

const oldTree = buildMerkleTree(oldChunks.map((c) => c.hash));
const newTree = buildMerkleTree(newChunks.map((c) => c.hash));

console.log(`分块耗时: 旧 ${(t1 - t0).toFixed(0)}ms, 新 ${(t2 - t1).toFixed(0)}ms`);
console.log(`分块数量: 旧 ${oldChunks.length}, 新 ${newChunks.length}`);
const sizes = oldChunks.map((c) => c.length);
console.log(
  `分块大小: min=${Math.min(...sizes)}, max=${Math.max(...sizes)}, ` +
    `avg=${(SIZE / oldChunks.length).toFixed(0)}\n`
);

console.log(`旧默克尔根: ${oldTree.root}`);
console.log(`新默克尔根: ${newTree.root}\n`);

const diff = diffLeaves(oldTree.leaves, newTree.leaves);
const reused = diff.commonPrefix + diff.commonSuffix;
console.log(`共同前缀分块: ${diff.commonPrefix}`);
console.log(`共同后缀分块: ${diff.commonSuffix}`);
console.log(
  `哈希复用率:   ${reused}/${oldTree.leaves.length} = ` +
    `${((reused / oldTree.leaves.length) * 100).toFixed(2)}%`
);
console.log(
  `受影响分块:   旧 [${diff.oldChanged[0]}, ${diff.oldChanged[1]}), ` +
    `新 [${diff.newChanged[0]}, ${diff.newChanged[1]})`
);

const changedNodes = changedMerkleNodes(oldTree, newTree);
const totalNodes = newTree.levels.reduce((n, l) => n + l.length, 0);
const newAffected = diff.newChanged[1] - diff.newChanged[0];
console.log(`\n== 增量传输清单 ==`);
console.log(`需传输的新分块数:     ${newAffected}`);
console.log(`需更新的默克尔节点数: ${changedNodes}/${totalNodes}`);
console.log(
  `增量数据占比:         ${((newAffected + changedNodes) / totalNodes * 100).toFixed(2)}%`
);
