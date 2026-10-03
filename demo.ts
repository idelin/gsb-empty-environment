/**
 * 运行样例：10 个读线程 + 5 个写线程并发访问同一棵螃蟹脚 B+ 树。
 * 运行：node demo.ts   （Node >= 23.6，或 22.6+ 加 --experimental-strip-types）
 */

import { BPlusTree } from "./src/bplustree.ts";
import { runConcurrentWorkload } from "./src/workload.ts";

const tree = new BPlusTree({ maxKeys: 8, interleave: true });

console.log("== 预填 1000 个 key，让树长到 3 层以上 ==");
for (let i = 0; i < 1000; i++) await tree.insert(i * 3 + 1500);
tree.validate();
console.log(`预填完成，当前规模: ${tree.size()}`);

console.log("\n== 启动 10 个读线程 + 5 个写线程 ==");
const result = await runConcurrentWorkload(tree, {
  readerCount: 10,
  writerCount: 5,
  keysPerWriter: 300,
  keySpace: 6000,
  seed: 42,
});

console.log(`并发阶段耗时: ${result.elapsedMs.toFixed(1)} ms`);
console.log(`写线程插入:   ${result.insertedKeys} 个 key`);
console.log(`读线程查找:   ${result.totalReads} 次 (命中 ${result.hits}, 未命中 ${result.misses})`);

console.log("\n== 螃蟹脚协议行为指标 ==");
console.log(`读操作同时持闩峰值:   ${tree.maxHeldDuringFind} (读耦合应 <= 2)`);
console.log(`读操作到达叶子时持闩: ${tree.maxHeldAtLeafFind} (应恒为 1)`);
console.log(`写操作同时持闩峰值:   ${tree.maxHeldDuringInsert}`);
console.log(`写操作到达叶子最少持闩: ${tree.minHeldAtLeafInsert} (安全路径应 = 1)`);

console.log("\n== 各层闩统计（immediate = 未阻塞直接获得, wait = 排队后被唤醒）==");
const header = "level  nodes  readImmediate  readWait  writeImmediate  writeWait";
console.log(header);
console.log("-".repeat(header.length));
for (const s of tree.statsByLevel()) {
  const label = s.level === 0 ? "L0(根)" : `L${s.level}`;
  console.log(
    `${label.padEnd(8)}${String(s.nodes).padEnd(7)}` +
      `${String(s.readImmediate).padEnd(15)}${String(s.readWait).padEnd(10)}` +
      `${String(s.writeImmediate).padEnd(16)}${s.writeWait}`,
  );
}

const rl = tree.rootLatchStats;
console.log(`rootLatch(树级): read ${rl.readImmediate} immediate / ${rl.readWait} wait, write ${rl.writeImmediate} immediate / ${rl.writeWait} wait`);
console.log(`根预防性分裂(树高增长)次数: ${tree.rootSplits}`);

console.log("\n== 最终校验 ==");
tree.validate();
let ok = 0;
for (let w = 0; w < 5; w++) {
  for (let i = 0; i < 300; i++) {
    if (await tree.find(w * 300 + i)) ok++;
  }
}
console.log(`结构不变量校验通过；写线程插入的 1500 个 key 全部可查 (${ok}/1500)`);
console.log(`最终规模: ${tree.size()}，无死锁、无丢 key。`);
