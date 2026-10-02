// 运行样例：node demo.ts  （需 Node >= 23.6，原生运行 TypeScript）
import { BufferPool } from "./src/buffer_pool.ts";
import { BPlusTree, LEAF_MAX } from "./src/bplustree.ts";

const TOTAL = 10_000;
const pool = new BufferPool(8); // 仅 8 个页框，强制跨页置换
const tree = new BPlusTree(pool);

console.log(`插入 ${TOTAL} 条连续记录 (key: 0..${TOTAL - 1}, value: key*10) ...`);
for (let i = 0; i < TOTAL; i++) {
  tree.insert(i, i * 10);
}
console.log(
  `插入完成: 页框 ${pool.frameCount}/${pool.capacity}, 置换 ${pool.evictionCount} 次, 残留 Pin = ${pool.totalPins}`,
);

console.log(`\n执行 Scan(2500, 7500)，叶子页容量 = ${LEAF_MAX} 条/页 ...`);
const fetchesBefore = pool.fetchCount;
const evictionsBefore = pool.evictionCount;

let count = 0;
let firstKey = -1;
let lastKey = -1;
for (const [key, value] of tree.scan(2500, 7500)) {
  if (count === 0) firstKey = key;
  lastKey = key;
  if (count < 3 || count >= 4997) {
    console.log(`  #${count}: key=${key}, value=${value}`);
  }
  count++;
}

console.log(`\n扫描结果:`);
console.log(`  输出记录数     : ${count} (期望 5000)`);
console.log(`  首条 / 末条 Key: ${firstKey} / ${lastKey}`);
console.log(`  扫描期间取页   : ${pool.fetchCount - fetchesBefore} 次`);
console.log(`  扫描期间置换   : ${pool.evictionCount - evictionsBefore} 次 (跨页证据)`);
console.log(`  结束后残留 Pin : ${pool.totalPins} (0 = 无页面泄漏)`);
console.log(`  页框占用       : ${pool.frameCount}/${pool.capacity} (未溢出)`);
